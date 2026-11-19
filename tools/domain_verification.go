package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// Every custom domain — a service's or gateway's hostname, a static site's,
// bucket's or video library's domain — is served only once the workspace has
// proved it controls the domain's DNS, with a TXT record or by hosting its
// zone here. An agent that only passes on "point a CNAME" leaves the domain
// unserved with no hint why, so every tool that attaches a domain says what
// the user has left to publish, and verify-domain checks it on request.

// domainStep is what one custom domain needs before it is served.
type domainStep struct {
	// Domain is the hostname.
	Domain string
	// CNAMETarget is what the domain's CNAME must point at.
	CNAMETarget string
	// Zone is the workspace's own zone the domain's records were written to,
	// and DNSError why they could not be, when that was tried.
	Zone, DNSError string
	// Verification is the domain's ownership proof; nil from an API that
	// predates it.
	Verification *cloud.DomainVerification
	// Meanwhile says where the thing is served until the domain is, such as
	// "the site serves on https://web.simplifyd.site". Optional.
	Meanwhile string
}

// nextStep says, in one sentence an agent can pass on, what the user has left
// to do for the domain: nothing, the TXT record that verifies it, the CNAME
// that routes it, or both.
func (d domainStep) nextStep() string {
	v := d.Verification
	managed := d.Zone != "" && d.DNSError == ""
	meanwhile := ""
	if d.Meanwhile != "" {
		meanwhile = " Until then " + d.Meanwhile + "."
	}

	var steps []string
	if !v.Verified() {
		steps = append(steps, fmt.Sprintf("publish a TXT record named %s with the value %s", v.RecordName, v.RecordValue))
	}
	if !managed && d.CNAMETarget != "" {
		steps = append(steps, fmt.Sprintf("point a CNAME for %s at %s", d.Domain, d.CNAMETarget))
	}

	switch {
	case managed && v.Verified():
		return fmt.Sprintf("nothing to do: %s is in the workspace's %s zone, so its DNS records were added automatically "+
			"and it is verified", d.Domain, d.Zone)
	case managed:
		return fmt.Sprintf("%s is in the workspace's %s zone, so its DNS records were added automatically. It is not "+
			"served until it is verified, which happens by itself once %s's nameservers point at Simplifyd Cloud "+
			"(see check-dns-zone); or %s at the zone's current DNS provider, then call verify-domain.%s",
			d.Domain, d.Zone, d.Zone, steps[0], meanwhile)
	case v.Verified() && len(steps) == 0:
		return fmt.Sprintf("nothing to do: %s is verified", d.Domain)
	case v.Verified():
		return fmt.Sprintf("%s; the domain is verified as the workspace's.", capitalize(steps[0]))
	}
	return fmt.Sprintf("%s. The domain is not served until the TXT record is found: it is checked automatically, "+
		"or call verify-domain to check now.%s", capitalize(strings.Join(steps, ", and ")), meanwhile)
}

// summary is the domain's entry in a tool result: its verification with the
// record to publish, and the next step.
func (d domainStep) summary(status string) map[string]any {
	entry := map[string]any{"domain": d.Domain, "next_step": d.nextStep()}
	if status != "" {
		entry["status"] = status
	}
	if d.Zone != "" && d.DNSError == "" {
		entry["dns_zone"] = d.Zone
	}
	if d.DNSError != "" {
		entry["dns_error"] = d.DNSError
	}
	if d.Verification != nil {
		entry["verification"] = d.Verification
	}
	return entry
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ---- verify-domain ----

type verifyDomainArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Service   string `json:"service"   jsonschema:"Slug or name of the service the domain is on: an HTTP service or gateway, static site, bucket or video library"`
	Domain    string `json:"domain"    jsonschema:"The custom domain to check, e.g. www.example.com"`
}

func handleVerifyDomain(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args verifyDomainArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(args.Domain)), ".")
	if domain == "" {
		return text("domain is required"), nil, nil
	}
	v, err := services(api, args.Workspace, args.Project, args.Env).CheckDomainVerification(ctx, args.Service, domain)
	if err != nil {
		return apiErr("verify domain", err), nil, nil
	}
	out := map[string]any{"verification": v}
	if v.Verified() {
		out["next_step"] = fmt.Sprintf("nothing to do: %s is verified and live; it needs no deploy", domain)
	} else {
		out["next_step"] = fmt.Sprintf("publish a TXT record named %s with the value %s, then call verify-domain again. "+
			"A record just published can take a few minutes to be seen.", v.RecordName, v.RecordValue)
	}
	return jsonText(out), nil, nil
}

// RegisterDomainVerificationTools adds verify-domain to the server.
func RegisterDomainVerificationTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name: "verify-domain",
		Description: "Check now whether a custom domain's verification TXT record is published, rather than waiting " +
			"for the next automatic check. Every custom domain — an HTTP service's or gateway's hostname, or a static " +
			"site's, bucket's or video library's domain — is not served until it is verified. Once the record is " +
			"found the domain goes live without a deploy; if another workspace was serving it and can no longer " +
			"prove control, it moves to this one. When still pending, the result's verification has last_error " +
			"saying why and the record to publish.",
	}, handleVerifyDomain)
}
