package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// ---- deploy-static-site ----

// staticSiteFileArgs mirrors cloud.StaticSiteFile. It exists so the MCP schema
// can carry its own descriptions — the field docs are what tell a caller that
// binary files need base64 rather than raw text.
type staticSiteFileArgs struct {
	Path        string `json:"path"                   jsonschema:"File path within the site, e.g. index.html or assets/app.js"`
	Content     string `json:"content"                jsonschema:"File contents: UTF-8 text, or base64 when encoding is base64"`
	Encoding    string `json:"encoding,omitempty"     jsonschema:"utf8 (default) or base64. Binary files (images, fonts, wasm) must use base64."`
	ContentType string `json:"content_type,omitempty" jsonschema:"Optional Content-Type override; inferred from the file extension when omitted"`
}

type deployStaticSiteArgs struct {
	Workspace string               `json:"workspace" jsonschema:"Workspace slug"`
	Project   string               `json:"project"   jsonschema:"Project slug"`
	Env       string               `json:"env"       jsonschema:"Environment slug"`
	Name      string               `json:"name"      jsonschema:"Site name. An existing static site with this name is reused; otherwise one is created."`
	Files     []staticSiteFileArgs `json:"files"     jsonschema:"The site's files. Must include the index document (index.html by default)."`
	Prune     *bool                `json:"prune,omitempty" jsonschema:"Remove files not in this request, making the publish a full replace. Default true. Set false to patch individual files."`
	// Domain is applied before the deploy so a single call can take a new site
	// all the way to serving on the caller's own hostname.
	Domain        string `json:"domain,omitempty"         jsonschema:"Optional custom domain to serve the site on. Requires a CNAME pointing at the returned domain_cname_target."`
	IndexDocument string `json:"index_document,omitempty" jsonschema:"Object served for a directory request, default index.html"`
	ErrorDocument string `json:"error_document,omitempty" jsonschema:"Object served when nothing matches. Point it at the index document for a client-side router."`
}

func handleDeployStaticSite(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args deployStaticSiteArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	if strings.TrimSpace(args.Name) == "" {
		return text("name is required"), nil, nil
	}
	if len(args.Files) == 0 {
		return text("files is required — a site needs at least its index document"), nil, nil
	}

	svcs := services(api, args.Workspace, args.Project, args.Env)

	// Reuse an existing site of the same name so repeated calls update one site
	// rather than creating a new one each time.
	existing, err := svcs.List(ctx)
	if err != nil {
		return apiErr("list services", err), nil, nil
	}
	var siteSlug string
	for _, s := range existing {
		if s.Type == cloud.ServiceTypeStaticSite && strings.EqualFold(s.Name, args.Name) {
			siteSlug = s.Slug
			break
		}
	}

	created := false
	if siteSlug == "" {
		svc, err := svcs.Create(ctx, cloud.CreateServiceInput{
			Name: args.Name,
			Type: cloud.ServiceTypeStaticSite,
			StaticSite: &cloud.StaticSiteInput{
				Name:          args.Name,
				IndexDocument: args.IndexDocument,
				ErrorDocument: args.ErrorDocument,
			},
		})
		if err != nil {
			return apiErr("create static site", err), nil, nil
		}
		siteSlug = svc.Slug
		created = true
	}

	site := svcs.StaticSite(siteSlug)

	// Only push document settings on an existing site; a freshly created one
	// already has them from the create call.
	if !created && (args.IndexDocument != "" || args.ErrorDocument != "") {
		if _, err := site.SetDocuments(ctx, cloud.UpdateStaticSiteDocumentsInput{
			IndexDocument: args.IndexDocument,
			ErrorDocument: args.ErrorDocument,
		}); err != nil {
			return apiErr("set static site documents", err), nil, nil
		}
	}

	files := make([]cloud.StaticSiteFile, 0, len(args.Files))
	for _, f := range args.Files {
		files = append(files, cloud.StaticSiteFile{
			Path:        f.Path,
			Content:     f.Content,
			Encoding:    f.Encoding,
			ContentType: f.ContentType,
		})
	}

	result, err := site.Publish(ctx, cloud.PublishStaticSiteInput{
		Files: files,
		Prune: args.Prune,
	})
	if err != nil {
		return apiErr("publish static site", err), nil, nil
	}

	out := map[string]any{
		"service":        siteSlug,
		"created":        created,
		"files_uploaded": result.FilesUploaded,
		"files_deleted":  result.FilesDeleted,
		"bytes_uploaded": result.BytesUploaded,
		"url":            result.URL,
	}

	// A custom domain needs a deploy: the routing that terminates TLS for it is
	// a cluster resource, unlike the platform URL which serves immediately.
	if strings.TrimSpace(args.Domain) != "" {
		updated, err := site.SetCustomDomain(ctx, args.Domain)
		if err != nil {
			return apiErr("set static site domain", err), nil, nil
		}
		if _, err := svcs.Deploy(ctx, siteSlug); err != nil {
			return apiErr("deploy static site", err), nil, nil
		}
		out["custom_domain"] = updated.CustomDomain
		out["domain_cname_target"] = updated.DomainCNAMETarget
		out["next_step"] = fmt.Sprintf(
			"point a CNAME for %s at %s; the site serves on %s until DNS propagates",
			updated.CustomDomain, updated.DomainCNAMETarget, updated.DefaultURL)
	}

	return jsonText(out), nil, nil
}

// ---- get-static-site ----

type getStaticSiteArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
	Env       string `json:"env"       jsonschema:"Environment slug"`
	Service   string `json:"service"   jsonschema:"Static site service slug"`
}

func handleGetStaticSite(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args getStaticSiteArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	site, err := services(api, args.Workspace, args.Project, args.Env).StaticSite(args.Service).Get(ctx)
	if err != nil {
		return apiErr("get static site", err), nil, nil
	}
	return jsonText(site), nil, nil
}

// ---- set-static-site-domain ----

type setStaticSiteDomainArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
	Env       string `json:"env"       jsonschema:"Environment slug"`
	Service   string `json:"service"   jsonschema:"Static site service slug"`
	Domain    string `json:"domain"    jsonschema:"Custom domain to serve the site on. Pass an empty string to detach the current domain."`
}

func handleSetStaticSiteDomain(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args setStaticSiteDomainArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	svcs := services(api, args.Workspace, args.Project, args.Env)
	site, err := svcs.StaticSite(args.Service).SetCustomDomain(ctx, args.Domain)
	if err != nil {
		return apiErr("set static site domain", err), nil, nil
	}
	// Routing for the domain is a cluster resource, so it only takes effect on
	// the next deploy.
	if _, err := svcs.Deploy(ctx, args.Service); err != nil {
		return apiErr("deploy static site", err), nil, nil
	}
	return jsonText(site), nil, nil
}

// RegisterStaticSiteTools adds the static site tools to the server.
func RegisterStaticSiteTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "deploy-static-site",
		Description: "Deploy an HTML/CSS/JS site to Simplifyd in one call. File contents are passed inline, " +
			"so no Docker image, build step or file upload is needed. Creates the site if one with this name " +
			"does not exist, then publishes the files. By default the publish is a full replace, so calling " +
			"this again with the updated files redeploys the site. Returns the live URL. " +
			"Binary files (images, fonts) must be sent with encoding=base64.",
	}, handleDeployStaticSite)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-static-site",
		Description: "Get a static site's configuration, serving URLs, custom domain status and storage usage.",
	}, handleGetStaticSite)

	mcp.AddTool(s, &mcp.Tool{
		Name: "set-static-site-domain",
		Description: "Attach a custom domain to a static site, or detach it by passing an empty domain. " +
			"Deploys the site so routing takes effect, and returns the CNAME target the domain must point at.",
	}, handleSetStaticSiteDomain)
}
