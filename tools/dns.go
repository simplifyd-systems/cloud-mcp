package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// DNS hosting tools: a workspace's zones and their records.
//
// Records are rendered as text rather than through jsonText, whose redaction
// drops any field called "value" — and a DNS record's value is public by
// nature, and the one part of it a person needs to see.

type dnsZoneArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Zone      string `json:"zone"      jsonschema:"The zone's domain name (acme.com) or id"`
}

type addDNSZoneArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Domain    string `json:"domain"    jsonschema:"A whole registrable domain, e.g. acme.com. Subdomains are records in its zone, not zones of their own."`
}

type addDNSRecordArgs struct {
	Workspace string  `json:"workspace"          jsonschema:"Workspace slug or name"`
	Zone      string  `json:"zone"               jsonschema:"The zone's domain name (acme.com) or id"`
	Name      string  `json:"name"               jsonschema:"Relative to the domain: @ for the domain itself, www, _dmarc, *.api for a wildcard"`
	Type      string  `json:"type"               jsonschema:"A, AAAA, CNAME, ALIAS, MX, TXT, SRV, CAA or NS. ALIAS is a CNAME that can sit at @."`
	Value     string  `json:"value"              jsonschema:"An address, a hostname, TXT text without quotes, 'weight port target' for SRV, or 'flags tag \"value\"' for CAA"`
	TTL       uint32  `json:"ttl,omitempty"      jsonschema:"Seconds, 60 to 86400. Default 300."`
	Priority  *uint16 `json:"priority,omitempty" jsonschema:"Required for MX and SRV"`
}

type updateDNSRecordArgs struct {
	Workspace string  `json:"workspace"          jsonschema:"Workspace slug or name"`
	Zone      string  `json:"zone"               jsonschema:"The zone's domain name (acme.com) or id"`
	Record    string  `json:"record"             jsonschema:"The record id, from list-dns-records"`
	Name      *string `json:"name,omitempty"     jsonschema:"New name; unchanged when left out"`
	Type      *string `json:"type,omitempty"     jsonschema:"New type; unchanged when left out"`
	Value     *string `json:"value,omitempty"    jsonschema:"New value; unchanged when left out"`
	TTL       *uint32 `json:"ttl,omitempty"      jsonschema:"New TTL; unchanged when left out"`
	Priority  *uint16 `json:"priority,omitempty" jsonschema:"New priority, for MX and SRV; unchanged when left out"`
}

type dnsRecordArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Zone      string `json:"zone"      jsonschema:"The zone's domain name (acme.com) or id"`
	Record    string `json:"record"    jsonschema:"The record id, from list-dns-records"`
}

// findZone resolves a zone's domain name to the workspace's zone.
func findZone(ctx context.Context, dns *cloud.DNSClient, ref string) (*cloud.DNSZone, error) {
	list, err := dns.Zones(ctx)
	if err != nil {
		return nil, err
	}
	ref = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(ref)), ".")
	names := make([]string, 0, len(list.Zones))
	for i, z := range list.Zones {
		if z.Name == ref || z.Slug == ref {
			return &list.Zones[i], nil
		}
		names = append(names, z.Name)
	}
	return nil, &missError{notFound("DNS zone", ref, names)}
}

func describeZone(z *cloud.DNSZone) string {
	if z.Status == cloud.DNSZoneActive {
		return fmt.Sprintf("%s (id %s) is live on Simplifyd Cloud's nameservers.", z.Name, z.Slug)
	}
	return fmt.Sprintf("%s (id %s) is pending: nothing is served until the domain's nameservers are changed, at "+
		"the registrar where it was bought, to %s. That change is the person's to make. Records can be added "+
		"meanwhile. Once changed it can take a few hours to be seen; check-dns-zone checks now. A zone nobody "+
		"delegates is removed after 14 days.", z.Name, z.Slug, strings.Join(z.Nameservers, " and "))
}

func describeRecord(r cloud.DNSRecord) string {
	value := r.Value
	if r.Priority != nil {
		value = fmt.Sprintf("%d %s", *r.Priority, value)
	}
	line := fmt.Sprintf("%s %s %s (ttl %d, id %s)", r.Name, r.Type, value, r.TTL, r.Slug)
	if r.Owner == "platform" {
		line += " [platform-managed, read-only]"
	}
	return line
}

func handleListDNSZones(ctx context.Context, req *mcp.CallToolRequest, args workspaceArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	list, err := api.Workspace(args.Workspace).DNS().Zones(ctx)
	if err != nil {
		return customerErr("list DNS zones", err), nil, nil
	}
	if len(list.Zones) == 0 {
		return text("This workspace hosts no DNS zones. Add one with add-dns-zone, or buy a domain with register-domain."), nil, nil
	}
	var b strings.Builder
	for i := range list.Zones {
		z := &list.Zones[i]
		fmt.Fprintf(&b, "- %s: %s (id %s)\n", z.Name, z.Status, z.Slug)
	}
	fmt.Fprintf(&b, "\nNameservers: %s", strings.Join(list.Nameservers, ", "))
	return text(b.String()), nil, nil
}

func handleAddDNSZone(ctx context.Context, req *mcp.CallToolRequest, args addDNSZoneArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	zone, err := api.Workspace(args.Workspace).DNS().CreateZone(ctx, args.Domain)
	if err != nil {
		return customerErr("add DNS zone", err), nil, nil
	}
	return text("Added. " + describeZone(zone)), nil, nil
}

func handleCheckDNSZone(ctx context.Context, req *mcp.CallToolRequest, args dnsZoneArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	dns := api.Workspace(args.Workspace).DNS()
	zone, err := findZone(ctx, dns, args.Zone)
	if err != nil {
		return customerErr("check DNS zone", err), nil, nil
	}
	if zone.Status != cloud.DNSZoneActive {
		if zone, err = dns.CheckZone(ctx, zone.Slug); err != nil {
			return customerErr("check DNS zone", err), nil, nil
		}
	}
	return text(describeZone(zone)), nil, nil
}

func handleDeleteDNSZone(ctx context.Context, req *mcp.CallToolRequest, args dnsZoneArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	dns := api.Workspace(args.Workspace).DNS()
	zone, err := findZone(ctx, dns, args.Zone)
	if err != nil {
		return customerErr("delete DNS zone", err), nil, nil
	}
	if err := dns.DeleteZone(ctx, zone.Slug); err != nil {
		return customerErr("delete DNS zone", err), nil, nil
	}
	return text(fmt.Sprintf("Deleted the zone %s and its records.", zone.Name)), nil, nil
}

func handleListDNSRecords(ctx context.Context, req *mcp.CallToolRequest, args dnsZoneArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	dns := api.Workspace(args.Workspace).DNS()
	zone, err := findZone(ctx, dns, args.Zone)
	if err != nil {
		return customerErr("list DNS records", err), nil, nil
	}
	records, err := dns.Records(ctx, zone.Slug)
	if err != nil {
		return customerErr("list DNS records", err), nil, nil
	}
	if len(records) == 0 {
		return text(fmt.Sprintf("%s has no records yet.", zone.Name)), nil, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Records in %s (%s):\n", zone.Name, zone.Status)
	for _, rec := range records {
		fmt.Fprintf(&b, "- %s\n", describeRecord(rec))
	}
	return text(b.String()), nil, nil
}

func handleAddDNSRecord(ctx context.Context, req *mcp.CallToolRequest, args addDNSRecordArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	dns := api.Workspace(args.Workspace).DNS()
	zone, err := findZone(ctx, dns, args.Zone)
	if err != nil {
		return customerErr("add DNS record", err), nil, nil
	}
	rec, err := dns.CreateRecord(ctx, zone.Slug, cloud.DNSRecordInput{
		Name: args.Name, Type: strings.ToUpper(args.Type), Value: args.Value, TTL: args.TTL, Priority: args.Priority,
	})
	if err != nil {
		return customerErr("add DNS record", err), nil, nil
	}
	msg := "Added " + describeRecord(*rec) + "."
	if zone.Status != cloud.DNSZoneActive {
		msg += " " + zone.Name + " is still pending, so it is not served until the domain's nameservers point at Simplifyd Cloud's."
	}
	return text(msg), nil, nil
}

func handleUpdateDNSRecord(ctx context.Context, req *mcp.CallToolRequest, args updateDNSRecordArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	dns := api.Workspace(args.Workspace).DNS()
	zone, err := findZone(ctx, dns, args.Zone)
	if err != nil {
		return customerErr("update DNS record", err), nil, nil
	}
	records, err := dns.Records(ctx, zone.Slug)
	if err != nil {
		return customerErr("update DNS record", err), nil, nil
	}
	var current *cloud.DNSRecord
	for i := range records {
		if records[i].Slug == args.Record {
			current = &records[i]
		}
	}
	if current == nil {
		return toolError(fmt.Sprintf("%s has no record with id %q; list-dns-records shows the ids", zone.Name, args.Record)), nil, nil
	}

	// The API replaces a record whole, so start from what it holds now.
	input := cloud.DNSRecordInput{Name: current.Name, Type: current.Type, Value: current.Value, TTL: current.TTL, Priority: current.Priority}
	if args.Name != nil {
		input.Name = *args.Name
	}
	if args.Type != nil {
		input.Type = strings.ToUpper(*args.Type)
	}
	if args.Value != nil {
		input.Value = *args.Value
	}
	if args.TTL != nil {
		input.TTL = *args.TTL
	}
	if args.Priority != nil {
		input.Priority = args.Priority
	}
	rec, err := dns.UpdateRecord(ctx, zone.Slug, current.Slug, input)
	if err != nil {
		return customerErr("update DNS record", err), nil, nil
	}
	return text("Updated to " + describeRecord(*rec) + "."), nil, nil
}

func handleDeleteDNSRecord(ctx context.Context, req *mcp.CallToolRequest, args dnsRecordArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	dns := api.Workspace(args.Workspace).DNS()
	zone, err := findZone(ctx, dns, args.Zone)
	if err != nil {
		return customerErr("delete DNS record", err), nil, nil
	}
	if err := dns.DeleteRecord(ctx, zone.Slug, args.Record); err != nil {
		return customerErr("delete DNS record", err), nil, nil
	}
	return text("Record deleted."), nil, nil
}

func RegisterDNSTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "list-dns-zones",
		Description: "List the domains whose DNS a workspace hosts on Simplifyd Cloud, whether each is live or pending, and the nameservers.",
	}, handleListDNSZones)

	addTool(s, &mcp.Tool{
		Name: "add-dns-zone",
		Description: "Host DNS for a domain registered elsewhere. The zone is pending until the person changes the " +
			"domain's nameservers at its registrar to the ones returned. A domain bought with register-domain " +
			"already has its zone; do not add one.",
	}, handleAddDNSZone)

	addTool(s, &mcp.Tool{
		Name:        "check-dns-zone",
		Description: "Check now whether a pending zone's domain points at Simplifyd Cloud's nameservers yet, and report its status.",
	}, handleCheckDNSZone)

	addTool(s, &mcp.Tool{
		Name: "delete-dns-zone",
		Description: "Stop hosting a domain's DNS and delete all its records. If the zone is live, the domain " +
			"stops resolving: confirm with the person first.",
	}, handleDeleteDNSZone)

	addTool(s, &mcp.Tool{
		Name: "list-dns-records",
		Description: "List a zone's DNS records with their ids. Records marked platform-managed were written by " +
			"Simplifyd Cloud (custom domains, email DKIM) and cannot be changed or deleted.",
	}, handleListDNSRecords)

	addTool(s, &mcp.Tool{
		Name: "add-dns-record",
		Description: "Add a DNS record to a zone (owners and developers). A name can have one CNAME and nothing " +
			"else; use ALIAS instead of CNAME at @.",
	}, handleAddDNSRecord)

	addTool(s, &mcp.Tool{
		Name:        "update-dns-record",
		Description: "Change a DNS record. Only the fields given change.",
	}, handleUpdateDNSRecord)

	addTool(s, &mcp.Tool{
		Name:        "delete-dns-record",
		Description: "Delete a DNS record from a zone.",
	}, handleDeleteDNSRecord)
}
