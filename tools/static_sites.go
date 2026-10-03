package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
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
	Workspace string               `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string               `json:"project"   jsonschema:"Project slug or name"`
	Env       string               `json:"env"       jsonschema:"Environment slug or name"`
	Name      string               `json:"name"      jsonschema:"Site name. An existing static site with this name is reused; otherwise one is created."`
	Files     []staticSiteFileArgs `json:"files,omitempty" jsonschema:"The site's files, inline. Must include the index document (index.html by default). Provide exactly one of files, archive, archive_path or archive_key."`
	// The archive forms exist so a whole build output goes up in one piece
	// rather than as one inline entry per file; the server expands it.
	Archive     string `json:"archive,omitempty"      jsonschema:"The whole site as a base64-encoded .zip. A single wrapping directory (e.g. dist/) is removed so the site serves at its root. Prefer archive_path when the zip is on the machine running this server."`
	ArchivePath string `json:"archive_path,omitempty" jsonschema:"Absolute path to a .zip of the site on the machine running this MCP server. Uploaded straight to storage, so the bytes never pass through the conversation and the site's size is not bounded by it."`
	ArchiveKey  string `json:"archive_key,omitempty"  jsonschema:"archive_key returned by create-static-site-upload, once the .zip has been PUT to its upload_url. Use the same name as that call."`
	Prune       *bool  `json:"prune,omitempty" jsonschema:"Remove files not in this request or archive, making the publish a full replace. Default true. Set false to patch individual files."`
	// Domain is applied before the deploy so a single call can take a new site
	// all the way to serving on the caller's own hostname.
	Domain        string `json:"domain,omitempty"         jsonschema:"Optional custom domain to serve the site on. Requires a CNAME pointing at the returned domain_cname_target, unless the domain is in a zone the workspace hosts here, when it is added automatically."`
	IndexDocument string `json:"index_document,omitempty" jsonschema:"Object served for a directory request, default index.html"`
	ErrorDocument string `json:"error_document,omitempty" jsonschema:"Object served when nothing matches. Point it at the index document for a client-side router."`
	SPAFallback   *bool  `json:"spa_fallback,omitempty"   jsonschema:"Set true for a single-page app (React, Vue, Svelte, …) so links to any of its pages load with status 200 rather than 404. Missing files under assets/ and static/ still 404. Omit to keep an existing site's setting."`
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
	// Resolve the source before touching the site, so a bad archive does not
	// leave an empty site behind.
	archivePath, cleanup, msg := stageStaticSiteArchive(args)
	if msg != "" {
		return text(msg), nil, nil
	}
	defer cleanup()

	svcs := services(api, args.Workspace, args.Project, args.Env)

	// A staged archive already lives in a site's bucket, so it can only be
	// published to that site; creating a new one here would publish nothing.
	siteSlug, created, failure := resolveStaticSite(ctx, svcs, args.Name,
		siteDocuments{args.IndexDocument, args.ErrorDocument, args.SPAFallback}, args.ArchiveKey == "")
	if failure != nil {
		return failure, nil, nil
	}

	site := svcs.StaticSite(siteSlug)
	var result *cloud.StaticSitePublishResult
	var err error
	switch {
	case args.ArchiveKey != "":
		result, err = site.PublishStagedArchive(ctx, cloud.PublishStaticSiteArchiveInput{
			Key:   args.ArchiveKey,
			Prune: args.Prune,
		})
	case archivePath != "":
		result, err = site.PublishArchive(ctx, archivePath, cloud.ArchivePublishOptions{Prune: args.Prune})
	default:
		files := make([]cloud.StaticSiteFile, 0, len(args.Files))
		for _, f := range args.Files {
			files = append(files, cloud.StaticSiteFile{
				Path:        f.Path,
				Content:     f.Content,
				Encoding:    f.Encoding,
				ContentType: f.ContentType,
			})
		}
		result, err = site.Publish(ctx, cloud.PublishStaticSiteInput{
			Files: files,
			Prune: args.Prune,
		})
	}
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
	if result.FilesSkipped > 0 {
		out["files_skipped"] = result.FilesSkipped
	}
	if result.StrippedPrefix != "" {
		out["stripped_prefix"] = result.StrippedPrefix
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
		if updated.DNSZone != "" && updated.DNSError == "" {
			// The domain is in a zone the workspace hosts with us, so its
			// record was written for it; telling the user to add one would be
			// wrong.
			out["dns_zone"] = updated.DNSZone
			out["next_step"] = fmt.Sprintf(
				"nothing to do: %s is in the workspace's %s zone, so its DNS record was added automatically",
				updated.CustomDomain, updated.DNSZone)
		} else {
			if updated.DNSError != "" {
				out["dns_error"] = updated.DNSError
			}
			out["next_step"] = fmt.Sprintf(
				"point a CNAME for %s at %s; the site serves on %s until DNS propagates",
				updated.CustomDomain, updated.DomainCNAMETarget, updated.DefaultURL)
		}
	}

	return jsonText(out), nil, nil
}

// siteDocuments are the document settings a call may give. Empty strings and a
// nil SPAFallback mean "leave as it is".
type siteDocuments struct {
	Index       string
	Error       string
	SPAFallback *bool
}

// resolveStaticSite finds a static site by name, creating one when create is
// set and there is none. Reusing by name is what makes repeated calls update
// one site rather than creating a new one each time. The document settings are
// applied to an existing site too, so a call can change them.
func resolveStaticSite(
	ctx context.Context,
	svcs *cloud.ServicesClient,
	name string,
	docs siteDocuments,
	create bool,
) (slug string, created bool, failure *mcp.CallToolResult) {
	existing, err := svcs.List(ctx)
	if err != nil {
		return "", false, apiErr("list services", err)
	}
	for _, s := range existing {
		if s.Type == cloud.ServiceTypeStaticSite && strings.EqualFold(s.Name, name) {
			slug = s.Slug
			break
		}
	}

	if slug == "" {
		if !create {
			return "", false, text(fmt.Sprintf(
				"no static site named %q — use the same name that was passed to create-static-site-upload", name))
		}
		svc, err := svcs.Create(ctx, cloud.CreateServiceInput{
			Name: name,
			Type: cloud.ServiceTypeStaticSite,
			StaticSite: &cloud.StaticSiteInput{
				Name:          name,
				IndexDocument: docs.Index,
				ErrorDocument: docs.Error,
				SPAFallback:   docs.SPAFallback != nil && *docs.SPAFallback,
			},
		})
		if err != nil {
			return "", false, apiErr("create static site", err)
		}
		// A freshly created site already has its documents from the create call.
		return svc.Slug, true, nil
	}

	if docs.Index != "" || docs.Error != "" || docs.SPAFallback != nil {
		if _, err := svcs.StaticSite(slug).SetDocuments(ctx, cloud.UpdateStaticSiteDocumentsInput{
			IndexDocument: docs.Index,
			ErrorDocument: docs.Error,
			SPAFallback:   docs.SPAFallback,
		}); err != nil {
			return "", false, apiErr("set static site documents", err)
		}
	}
	return slug, false, nil
}

// stagedArchivePrefix is where the SDK stages archives in a site's bucket, and
// the only place the API will expand one from.
const stagedArchivePrefix = ".uploads/"

// maxInlineArchiveBytes caps a decoded inline archive. Anything larger belongs
// in archive_path, where it streams to storage instead of sitting in memory.
const maxInlineArchiveBytes = 32 << 20

// stageStaticSiteArchive settles which source a deploy publishes from. It
// returns the path of a zip to publish (empty for inline files and an already
// staged archive), a cleanup to run once the publish is done, and a message for
// the caller when the arguments are unusable. An inline archive is written to a
// temporary file because the SDK publishes archives from disk.
func stageStaticSiteArchive(args deployStaticSiteArgs) (string, func(), string) {
	noop := func() {}

	sources := 0
	for _, set := range []bool{
		len(args.Files) > 0,
		args.Archive != "",
		strings.TrimSpace(args.ArchivePath) != "",
		args.ArchiveKey != "",
	} {
		if set {
			sources++
		}
	}
	switch {
	case sources == 0:
		return "", noop, "provide files, archive, archive_path or archive_key — a site needs at least its index document"
	case sources > 1:
		return "", noop, "provide only one of files, archive, archive_path or archive_key"
	case len(args.Files) > 0:
		return "", noop, ""
	case args.ArchiveKey != "":
		// The server enforces this too; checking here gives a clearer message
		// than a rejected publish.
		if !strings.HasPrefix(args.ArchiveKey, stagedArchivePrefix) ||
			!strings.HasSuffix(strings.ToLower(args.ArchiveKey), ".zip") {
			return "", noop, "archive_key must be the archive_key returned by create-static-site-upload"
		}
		return "", noop, ""
	}

	if p := strings.TrimSpace(args.ArchivePath); p != "" {
		if !strings.EqualFold(filepath.Ext(p), ".zip") {
			return "", noop, "archive_path must be a .zip file"
		}
		info, err := os.Stat(p)
		switch {
		case err != nil:
			return "", noop, fmt.Sprintf("archive_path %s cannot be read on the machine running this server: %v", p, err)
		case info.IsDir():
			return "", noop, "archive_path is a directory — zip it first"
		case info.Size() == 0:
			return "", noop, "archive_path is empty"
		}
		return p, noop, ""
	}

	// Shell base64 tools wrap their output, so whitespace is dropped rather
	// than treated as corruption.
	encoded := strings.Join(strings.Fields(args.Archive), "")
	if base64.StdEncoding.DecodedLen(len(encoded)) > maxInlineArchiveBytes+3 {
		return "", noop, fmt.Sprintf("archive is larger than %d MiB — use archive_path instead", maxInlineArchiveBytes>>20)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", noop, "archive is not valid base64"
	}
	if len(data) > maxInlineArchiveBytes {
		return "", noop, fmt.Sprintf("archive is larger than %d MiB — use archive_path instead", maxInlineArchiveBytes>>20)
	}
	if !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return "", noop, "archive is not a zip file"
	}

	f, err := os.CreateTemp("", "static-site-*.zip")
	if err != nil {
		return "", noop, "could not stage the archive for upload"
	}
	cleanup := func() { os.Remove(f.Name()) }
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		cleanup()
		return "", noop, "could not stage the archive for upload"
	}
	return f.Name(), cleanup, ""
}

// ---- create-static-site-upload ----

// The upload-URL flow is for an agent whose zip is on its own machine while
// this server runs elsewhere, as a hosted connector does. The agent PUTs the
// bytes straight to storage with its own shell, so they never pass through the
// conversation, then publishes with deploy-static-site.

type createStaticSiteUploadArgs struct {
	Workspace     string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project       string `json:"project"   jsonschema:"Project slug or name"`
	Env           string `json:"env"       jsonschema:"Environment slug or name"`
	Name          string `json:"name"      jsonschema:"Site name. An existing static site with this name is reused; otherwise one is created. Pass the same name to deploy-static-site."`
	IndexDocument string `json:"index_document,omitempty" jsonschema:"Object served for a directory request, default index.html"`
	ErrorDocument string `json:"error_document,omitempty" jsonschema:"Object served when nothing matches. Point it at the index document for a client-side router."`
	SPAFallback   *bool  `json:"spa_fallback,omitempty"   jsonschema:"Set true for a single-page app (React, Vue, Svelte, …) so links to any of its pages load with status 200 rather than 404. Missing files under assets/ and static/ still 404. Omit to keep an existing site's setting."`
}

func handleCreateStaticSiteUpload(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args createStaticSiteUploadArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	if strings.TrimSpace(args.Name) == "" {
		return text("name is required"), nil, nil
	}

	svcs := services(api, args.Workspace, args.Project, args.Env)
	siteSlug, created, failure := resolveStaticSite(ctx, svcs, args.Name,
		siteDocuments{args.IndexDocument, args.ErrorDocument, args.SPAFallback}, true)
	if failure != nil {
		return failure, nil, nil
	}

	staged, err := svcs.StaticSite(siteSlug).StageArchiveUpload(ctx)
	if err != nil {
		return apiErr("create static site upload", err), nil, nil
	}

	return jsonText(map[string]any{
		"service":     siteSlug,
		"created":     created,
		"archive_key": staged.Key,
		"upload_url":  staged.URL,
		"expires_at":  staged.ExpiresAt,
		"upload_command": fmt.Sprintf(
			"curl -fsS -X PUT -H 'Content-Type: application/zip' --upload-file site.zip '%s'", staged.URL),
		"next_step": fmt.Sprintf(
			"PUT the .zip to upload_url (replace site.zip in upload_command with its path), then call "+
				"deploy-static-site with name %q and archive_key %q", args.Name, staged.Key),
	}), nil, nil
}

// ---- get-static-site ----

type getStaticSiteArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
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

// ---- list-static-site-files ----

type listStaticSiteFilesArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Service   string `json:"service"   jsonschema:"Static site service slug"`
	Prefix    string `json:"prefix,omitempty" jsonschema:"Optional path prefix to list, e.g. assets/. Lists the whole site when omitted."`
}

func handleListStaticSiteFiles(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args listStaticSiteFilesArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	list, err := services(api, args.Workspace, args.Project, args.Env).
		StaticSite(args.Service).ListFiles(ctx, args.Prefix)
	if err != nil {
		return apiErr("list static site files", err), nil, nil
	}
	return jsonText(list), nil, nil
}

// ---- get-static-site-files ----

type getStaticSiteFilesArgs struct {
	Workspace string   `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string   `json:"project"   jsonschema:"Project slug or name"`
	Env       string   `json:"env"       jsonschema:"Environment slug or name"`
	Service   string   `json:"service"   jsonschema:"Static site service slug"`
	Paths     []string `json:"paths"     jsonschema:"Paths of the files to download, e.g. [\"index.html\", \"assets/app.js\"]. Use list-static-site-files to discover them."`
}

func handleGetStaticSiteFiles(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args getStaticSiteFilesArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	if len(args.Paths) == 0 {
		return text("paths is required — name at least one file to download"), nil, nil
	}
	result, err := services(api, args.Workspace, args.Project, args.Env).
		StaticSite(args.Service).Fetch(ctx, args.Paths)
	if err != nil {
		return apiErr("get static site files", err), nil, nil
	}
	return jsonText(result), nil, nil
}

// ---- set-static-site-domain ----

type setStaticSiteDomainArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
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
	addTool(s, &mcp.Tool{
		Name: "deploy-static-site",
		Description: "Deploy an HTML/CSS/JS site to Simplifyd in one call. Creates the site if one with this name " +
			"does not exist, then publishes it. The site can be given as inline files, or as a single .zip — " +
			"archive_path for a zip on the machine running this server, archive_key for a zip uploaded via " +
			"create-static-site-upload, or archive for a base64-encoded zip. If the zip is on your machine and " +
			"you can run shell commands, use create-static-site-upload rather than archive so the bytes never " +
			"pass through the conversation. " +
			"Prefer a zip over listing files one by one for anything beyond a few files, such as a build " +
			"output directory; a single wrapping directory like dist/ is removed so the site serves at its root. " +
			"By default the publish is a full replace, so calling this again with the updated site redeploys it. " +
			"For a single-page app built with React, Vue, Svelte or similar, set spa_fallback so its deep links load. " +
			"Returns the live URL. Inline binary files (images, fonts) must be sent with encoding=base64.",
	}, handleDeployStaticSite)

	addTool(s, &mcp.Tool{
		Name: "create-static-site-upload",
		Description: "Get a presigned URL to upload a static site as a .zip straight to storage, for when the zip " +
			"is on your machine rather than the one running this server. Creates the site if one with this name " +
			"does not exist. PUT the zip to upload_url (upload_command is a ready curl command), then call " +
			"deploy-static-site with the same name and the returned archive_key to publish it. The bytes never " +
			"pass through the conversation, so the site's size is not bounded by it. The URL expires at expires_at.",
	}, handleCreateStaticSiteUpload)

	addTool(s, &mcp.Tool{
		Name:        "get-static-site",
		Description: "Get a static site's configuration, serving URLs, custom domain status and storage usage.",
	}, handleGetStaticSite)

	addTool(s, &mcp.Tool{
		Name: "list-static-site-files",
		Description: "List the files currently published to a static site, with sizes and modification times. " +
			"Lists nested files too. Use this to see what a site contains before downloading or editing it.",
	}, handleListStaticSiteFiles)

	addTool(s, &mcp.Tool{
		Name: "get-static-site-files",
		Description: "Download the contents of published static site files. Contents come back inline in the " +
			"same shape deploy-static-site accepts, so you can fetch a file, edit it and republish it. " +
			"Text is returned as utf8 and binary files as base64. When republishing edited files, either send " +
			"the complete file set, or set prune=false to patch just the files you changed.",
	}, handleGetStaticSiteFiles)

	addTool(s, &mcp.Tool{
		Name: "set-static-site-domain",
		Description: "Attach a custom domain to a static site, or detach it by passing an empty domain. " +
			"Deploys the site so routing takes effect, and returns the CNAME target the domain must point at.",
	}, handleSetStaticSiteDomain)
}
