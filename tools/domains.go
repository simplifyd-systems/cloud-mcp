package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// Domain purchase tools.
//
// Buying and renewing spend the workspace wallet, so neither happens on the
// first call. register-domain answers a call without confirm_total with the
// price, and only charges when called again with that exact total, which the
// model can only have by showing it. A price that changed in between is refused
// rather than charged. renew-domain likewise needs confirm set.
//
// The registrant, the domain's legal owner, is not an argument. A workspace
// saves it once in the console's settings and every purchase uses it, so the
// tools show it for the person to check and send them to settings to change
// it; a model never types someone's legal details in.
//
// Revealing the transfer code is left to the console and CLI. Anyone holding it
// can move the domain to another registrar, and it has no business in a
// conversation transcript.

type workspaceArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
}

type searchDomainsArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Query     string `json:"query"     jsonschema:"A word (acme) or a full name (acme.com) to look for"`
}

type quoteDomainArgs struct {
	Workspace string `json:"workspace"       jsonschema:"Workspace slug or name"`
	Name      string `json:"name"            jsonschema:"The full domain name, e.g. acme.com"`
	Years     int    `json:"years,omitempty" jsonschema:"Years to register for, 1 to 10. Default 1."`
}

type domainArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Domain    string `json:"domain"    jsonschema:"The domain's name (acme.com) or id"`
}

type registerDomainArgs struct {
	Workspace string `json:"workspace"            jsonschema:"Workspace slug or name"`
	Name      string `json:"name"                 jsonschema:"The full domain name to buy, e.g. acme.com"`
	Years     int    `json:"years,omitempty"      jsonschema:"Years to register for, 1 to 10. Default 1."`
	AutoRenew *bool  `json:"auto_renew,omitempty" jsonschema:"Renew from the wallet 30 days before expiry. Default true."`
	// ConfirmTotal is the guard on spending: see the file comment.
	ConfirmTotal int64 `json:"confirm_total,omitempty" jsonschema:"Leave out on the first call, which only returns the price. After the person has agreed to that price, call again with the confirm_total it gave."`
}

type updateDomainArgs struct {
	Workspace string `json:"workspace"            jsonschema:"Workspace slug or name"`
	Domain    string `json:"domain"               jsonschema:"The domain's name (acme.com) or id"`
	AutoRenew *bool  `json:"auto_renew,omitempty" jsonschema:"Turn automatic renewal on or off"`
	Locked    *bool  `json:"locked,omitempty"     jsonschema:"Turn the registrar transfer lock on or off"`
}

type renewDomainArgs struct {
	Workspace string `json:"workspace"       jsonschema:"Workspace slug or name"`
	Domain    string `json:"domain"          jsonschema:"The domain's name (acme.com) or id"`
	Years     int    `json:"years,omitempty" jsonschema:"Years to add, 1 to 10. Default 1."`
	Confirm   bool   `json:"confirm,omitempty" jsonschema:"Set true only after the person has agreed to the renewal being charged to the wallet"`
}

// findDomain resolves a domain's name to the workspace's domain.
func findDomain(ctx context.Context, domains *cloud.DomainsClient, ref string) (*cloud.Domain, error) {
	list, err := domains.List(ctx)
	if err != nil {
		return nil, err
	}
	ref = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(ref)), ".")
	names := make([]string, 0, len(list.Domains))
	for i, d := range list.Domains {
		if d.Name == ref || d.Slug == ref {
			return &list.Domains[i], nil
		}
		names = append(names, d.Name)
	}
	return nil, &missError{notFound("domain", ref, names)}
}

// missError is a name that matched nothing in the workspace. Its message
// lists what does exist, for the model to retry with.
type missError struct{ error }

// customerErr reports a failed domain or DNS call. These APIs explain a
// refusal in words written for the customer — "a CNAME cannot be at the root
// of the domain", "your wallet is ₦2,000.00 short" — and without them a model
// cannot correct the call or tell the person what to do. Anything else falls
// back to apiErr's generic report.
func customerErr(op string, err error) *mcp.CallToolResult {
	var miss *missError
	if errors.As(err, &miss) {
		return toolError(miss.Error())
	}
	var apiError *cloud.APIError
	if errors.As(err, &apiError) && apiError.Message != "" && !strings.HasPrefix(apiError.Message, "HTTP ") {
		switch apiError.StatusCode {
		case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity:
			return errResult("%s: %s", op, apiError.Message)
		}
	}
	return apiErr(op, err)
}

// registrantSettingsURL is where a workspace's registrant is saved and
// changed. With a name, saving there returns the person to buying it.
func registrantSettingsURL(name string) string {
	u := "https://console.cloud.simplifyd.com/project/settings/domains"
	if name != "" {
		u += "?return=" + url.QueryEscape(name)
	}
	return u
}

func describeRegistrant(r *cloud.DomainContact) string {
	name := r.FirstName + " " + r.LastName
	if r.CompanyName != "" {
		name += ", " + r.CompanyName
	}
	var addr []string
	for _, s := range []string{r.Address1, r.Address2, r.City, r.State, r.Zip, r.Country} {
		if s != "" {
			addr = append(addr, s)
		}
	}
	return fmt.Sprintf("%s <%s>, %s, %s", name, r.Email, r.Phone, strings.Join(addr, ", "))
}

func describeQuote(q *cloud.DomainQuote) string {
	if !q.Available {
		reason := q.Reason
		if reason == "" {
			reason = "not available"
		}
		return fmt.Sprintf("%s cannot be bought: %s", q.Name, reason)
	}
	return fmt.Sprintf("%s for %d year(s): %s + %s VAT = %s total. Renews at %s a year plus VAT.",
		q.Name, q.Years, cloud.FormatNaira(q.Price), cloud.FormatNaira(q.VAT), cloud.FormatNaira(q.Total),
		cloud.FormatNaira(q.RenewalPrice))
}

func handleSearchDomains(ctx context.Context, req *mcp.CallToolRequest, args searchDomainsArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	if strings.TrimSpace(args.Query) == "" {
		return text("query is required"), nil, nil
	}
	results, err := api.Workspace(args.Workspace).Domains().Search(ctx, args.Query)
	if err != nil {
		return customerErr("search domains", err), nil, nil
	}
	if len(results) == 0 {
		return text("No names found."), nil, nil
	}
	var b strings.Builder
	b.WriteString("Prices are for the first year, VAT included, paid from the workspace wallet.\n\n")
	for i := range results {
		fmt.Fprintf(&b, "- %s\n", describeQuote(&results[i]))
	}
	return text(b.String()), nil, nil
}

func handleQuoteDomain(ctx context.Context, req *mcp.CallToolRequest, args quoteDomainArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	q, err := api.Workspace(args.Workspace).Domains().Quote(ctx, args.Name, args.Years)
	if err != nil {
		return customerErr("quote domain", err), nil, nil
	}
	return text(describeQuote(q)), nil, nil
}

func handleListDomains(ctx context.Context, req *mcp.CallToolRequest, args workspaceArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	list, err := api.Workspace(args.Workspace).Domains().List(ctx)
	if err != nil {
		return customerErr("list domains", err), nil, nil
	}
	return jsonText(list), nil, nil
}

func handleGetDomain(ctx context.Context, req *mcp.CallToolRequest, args domainArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	domains := api.Workspace(args.Workspace).Domains()
	d, err := findDomain(ctx, domains, args.Domain)
	if err != nil {
		return customerErr("get domain", err), nil, nil
	}
	detail, err := domains.Get(ctx, d.Slug)
	if err != nil {
		return customerErr("get domain", err), nil, nil
	}
	return jsonText(detail), nil, nil
}

func handleRegisterDomain(ctx context.Context, req *mcp.CallToolRequest, args registerDomainArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	domains := api.Workspace(args.Workspace).Domains()

	q, err := domains.Quote(ctx, args.Name, args.Years)
	if err != nil {
		return customerErr("register domain", err), nil, nil
	}
	if !q.Available {
		return toolError(describeQuote(q) + " Nothing was charged."), nil, nil
	}

	reg, err := domains.Registrant(ctx)
	if err != nil {
		return customerErr("register domain", err), nil, nil
	}
	if reg == nil {
		return toolError(fmt.Sprintf("This workspace has not saved its domain registrant, the legal owner of the domains it buys. "+
			"The person adds it once in settings, at %s, and then this purchase can go ahead. Nothing was charged.",
			registrantSettingsURL(q.Name))), nil, nil
	}

	if args.ConfirmTotal != q.Total {
		var b strings.Builder
		if args.ConfirmTotal != 0 {
			fmt.Fprintf(&b, "The price has changed since it was confirmed. Nothing was charged.\n\n")
		}
		fmt.Fprintf(&b, "%s\n\nRegistrant (the domain's legal owner, saved for the workspace): %s\n\n", describeQuote(q), describeRegistrant(reg))
		fmt.Fprintf(&b, "Nothing has been charged. Show the person this price and registrant. To change the registrant they edit it in settings, at %s, which brings them back to this purchase. If they agree to both, call register-domain again with the same arguments and confirm_total=%d.", registrantSettingsURL(q.Name), q.Total)
		return text(b.String()), nil, nil
	}

	d, err := domains.Register(ctx, cloud.RegisterDomainInput{Name: q.Name, Years: q.Years, AutoRenew: args.AutoRenew})
	if err != nil {
		return customerErr("register domain", err), nil, nil
	}
	var b strings.Builder
	if d.Status == cloud.DomainRegistering {
		fmt.Fprintf(&b, "Paid %s for %s. The registrar is finishing the registration; check on it with get-domain.", cloud.FormatNaira(q.Total), d.Name)
	} else {
		fmt.Fprintf(&b, "Registered %s for %s.", d.Name, cloud.FormatNaira(q.Total))
		if d.ExpiresAt != nil {
			fmt.Fprintf(&b, " It expires %s.", d.ExpiresAt.Format("2006-01-02"))
		}
	}
	fmt.Fprintf(&b, " %s will get an email from the registrar to confirm their address. The domain is already served by Simplifyd Cloud's nameservers: add records with add-dns-record.", reg.Email)
	return text(b.String()), nil, nil
}

func handleUpdateDomain(ctx context.Context, req *mcp.CallToolRequest, args updateDomainArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	if args.AutoRenew == nil && args.Locked == nil {
		return toolError("nothing to change: give auto_renew or locked"), nil, nil
	}
	domains := api.Workspace(args.Workspace).Domains()
	d, err := findDomain(ctx, domains, args.Domain)
	if err != nil {
		return customerErr("update domain", err), nil, nil
	}
	detail, err := domains.Update(ctx, d.Slug, cloud.UpdateDomainInput{AutoRenew: args.AutoRenew, Locked: args.Locked})
	if err != nil {
		return customerErr("update domain", err), nil, nil
	}
	return jsonText(detail), nil, nil
}

func handleRenewDomain(ctx context.Context, req *mcp.CallToolRequest, args renewDomainArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	domains := api.Workspace(args.Workspace).Domains()
	d, err := findDomain(ctx, domains, args.Domain)
	if err != nil {
		return customerErr("renew domain", err), nil, nil
	}
	years := max(args.Years, 1)
	if !args.Confirm {
		msg := fmt.Sprintf("Nothing has been charged. Renewing %s for %d year(s) is paid from the workspace wallet at its renewal price plus VAT.", d.Name, years)
		if d.AutoRenew {
			msg += " It is set to renew automatically 30 days before it expires, so this is only needed to add years early."
		}
		msg += " If the person agrees, call renew-domain again with confirm=true."
		return text(msg), nil, nil
	}
	renewed, err := domains.Renew(ctx, d.Slug, years)
	if err != nil {
		return customerErr("renew domain", err), nil, nil
	}
	msg := fmt.Sprintf("Renewed %s.", renewed.Name)
	if renewed.ExpiresAt != nil {
		msg += " It now expires " + renewed.ExpiresAt.Format("2006-01-02") + "."
	}
	return text(msg), nil, nil
}

func RegisterDomainTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name: "search-domains",
		Description: "Search for domain names to buy, with whether each is available and its first-year price " +
			"in naira, VAT included. Purchases are paid from the workspace wallet.",
	}, handleSearchDomains)

	addTool(s, &mcp.Tool{
		Name:        "quote-domain",
		Description: "Price registering one domain name for a number of years, with VAT and the yearly renewal price.",
	}, handleQuoteDomain)

	addTool(s, &mcp.Tool{
		Name: "list-domains",
		Description: "List the domains a workspace has bought, with status, expiry and auto-renewal, and whether " +
			"buying is available right now (sales_enabled).",
	}, handleListDomains)

	addTool(s, &mcp.Tool{
		Name:        "get-domain",
		Description: "Get a domain's details: expiry, auto-renewal, transfer lock, nameservers, registrant and orders.",
	}, handleGetDomain)

	addTool(s, &mcp.Tool{
		Name: "register-domain",
		Description: "Buy a domain name, paid from the workspace wallet (owners and developers only). Takes two " +
			"calls: the first charges nothing and returns the total and the registrant, the workspace's saved legal " +
			"owner of its domains; after the person agrees to both, call again with confirm_total. The registrant is " +
			"changed only by the person, in the console's settings, at the link given. The domain is served by " +
			"Simplifyd Cloud's nameservers straight away, so records can be added with add-dns-record.",
	}, handleRegisterDomain)

	addTool(s, &mcp.Tool{
		Name: "renew-domain",
		Description: "Renew a domain now, paid from the workspace wallet. Domains with auto-renewal on renew " +
			"themselves 30 days before expiry. Charges only when confirm is true, which needs the person's agreement.",
	}, handleRenewDomain)

	addTool(s, &mcp.Tool{
		Name: "update-domain",
		Description: "Turn a domain's automatic renewal or registrar transfer lock on or off. The transfer code " +
			"itself is only shown in the console and CLI.",
	}, handleUpdateDomain)
}
