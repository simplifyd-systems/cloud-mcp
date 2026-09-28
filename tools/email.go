package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Email service tools.
//
// An agent sets up sending in three steps: create an email service with
// create-service, add the domain to send from, and — once the returned DNS
// records are published — verify it. After that, other services send with the
// variables the email service publishes. The part an agent cannot do is
// publish DNS, so the tools say plainly which records the person has to add.
//
// Replacing the key is deliberately left to the console: it cuts off every
// service sending with the old one until they are redeployed, which is not a
// step to take on an agent's say-so.

type emailServiceArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Service   string `json:"service"   jsonschema:"Email service slug"`
}

type emailDomainArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Service   string `json:"service"   jsonschema:"Email service slug"`
	Domain    string `json:"domain"    jsonschema:"For add-email-domain, the domain to send from, e.g. mail.example.com. A subdomain keeps this mail separate from the domain's own. For verify and remove, the domain id returned by add-email-domain or list-email-domains."`
}

func handleListEmailDomains(ctx context.Context, req *mcp.CallToolRequest, args emailServiceArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	domains, err := services(api, args.Workspace, args.Project, args.Env).Email(args.Service).Domains(ctx)
	if err != nil {
		return apiErr("list email domains", err), nil, nil
	}
	return jsonText(domains), nil, nil
}

func handleAddEmailDomain(ctx context.Context, req *mcp.CallToolRequest, args emailDomainArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	if strings.TrimSpace(args.Domain) == "" {
		return text("domain is required"), nil, nil
	}
	d, err := services(api, args.Workspace, args.Project, args.Env).Email(args.Service).AddDomain(ctx, args.Domain)
	if err != nil {
		return apiErr("add email domain", err), nil, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Added %s (id %s). Nothing can be sent from it until these DNS records are published at the domain's DNS provider and verify-email-domain succeeds:\n\n", d.Domain, d.ID)
	for _, rec := range d.Records {
		fmt.Fprintf(&b, "- %s (%s)\n  name:  %s\n  value: %s\n", rec.Type, rec.Purpose, rec.Name, rec.Value)
	}
	b.WriteString("\nDNS changes can take a while to become visible, so a first verification that fails is expected.")
	return text(b.String()), nil, nil
}

func handleVerifyEmailDomain(ctx context.Context, req *mcp.CallToolRequest, args emailDomainArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	d, err := services(api, args.Workspace, args.Project, args.Env).Email(args.Service).VerifyDomain(ctx, args.Domain)
	if err != nil {
		return apiErr("verify email domain", err), nil, nil
	}
	if !d.Verified() {
		return text(fmt.Sprintf("%s is still pending: its DNS records are not visible yet. Check they are published exactly as given, then try again in a few minutes.", d.Domain)), nil, nil
	}
	return text(fmt.Sprintf("%s is verified. Services in this environment can now send from it using the email service's variables, e.g. ${{<email service name>.SMTP_PASSWORD}}.", d.Domain)), nil, nil
}

func handleRemoveEmailDomain(ctx context.Context, req *mcp.CallToolRequest, args emailDomainArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	if err := services(api, args.Workspace, args.Project, args.Env).Email(args.Service).DeleteDomain(ctx, args.Domain); err != nil {
		return apiErr("remove email domain", err), nil, nil
	}
	return text("Domain removed. Email from addresses at it will be refused."), nil, nil
}

func RegisterEmailTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name: "list-email-domains",
		Description: "List an email service's sending domains, whether each is verified, and the DNS records a " +
			"pending one still needs.",
	}, handleListEmailDomains)

	addTool(s, &mcp.Tool{
		Name: "add-email-domain",
		Description: "Add a domain for an email service to send from, and get the DNS records (domain " +
			"verification, DKIM, SPF, DMARC) that must be published before it can be used. The records have " +
			"to be added at the domain's DNS provider by whoever controls it; this tool cannot do that part.",
	}, handleAddEmailDomain)

	addTool(s, &mcp.Tool{
		Name: "verify-email-domain",
		Description: "Check an email domain's DNS records now. Once verified, services in the environment can " +
			"send from it over SMTP or Why.email's API with the email service's variables — SMTP_HOST, " +
			"SMTP_PORT, SMTP_USERNAME, SMTP_PASSWORD, WHY_EMAIL_API_KEY and WHY_EMAIL_API_URL, referenced as " +
			"${{<email service name>.<variable>}}. Sending is billed at 50 kobo per email delivered.",
	}, handleVerifyEmailDomain)

	addTool(s, &mcp.Tool{
		Name:        "remove-email-domain",
		Description: "Stop an email service sending from a domain.",
	}, handleRemoveEmailDomain)
}
