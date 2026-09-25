package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// Templates are returned as summaries rather than as the API's documents. A
// template service is identified by a field named "key", which the response
// redaction in jsonText treats as a secret; and what an assistant needs to
// choose and explain a template is its services and their size, not its
// variables and files, which the API applies on its own.

const (
	templateSourceSimplifyd = "simplifyd"
	templateSourceWorkspace = "workspace"
)

type templateSummary struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`
	Services    int    `json:"services"`
	VCPUs       uint   `json:"vcpus"`
	MemoryMB    uint   `json:"memory_mb"`
	DiskGB      int    `json:"disk_gb,omitempty"`
}

type templateServiceSummary struct {
	Service  string `json:"service"`
	Type     string `json:"type"`
	Image    string `json:"image,omitempty"`
	VCPUs    uint   `json:"vcpus"`
	MemoryMB uint   `json:"memory_mb"`
	DiskGB   int    `json:"disk_gb,omitempty"`
	Public   bool   `json:"public"`
}

type templateDetail struct {
	templateSummary
	ServiceDetails []templateServiceSummary `json:"service_details"`
}

func summarizeTemplate(t cloud.Template, source string) templateSummary {
	vcpus, memory, disk := t.Size()
	return templateSummary{
		Slug: t.Slug, Name: t.Name, Description: t.Description, Source: source,
		Services: len(t.Services), VCPUs: vcpus, MemoryMB: memory, DiskGB: disk,
	}
}

func describeTemplate(t cloud.Template, source string) templateDetail {
	d := templateDetail{templateSummary: summarizeTemplate(t, source)}
	for _, s := range t.Services {
		vcpus, memory, disk := s.Size()
		image := ""
		if s.Docker != nil {
			image = s.Docker.Image
			if s.Docker.Tag != "" {
				image += ":" + s.Docker.Tag
			}
		}
		d.ServiceDetails = append(d.ServiceDetails, templateServiceSummary{
			Service: s.Key, Type: string(s.Type), Image: image,
			VCPUs: vcpus, MemoryMB: memory, DiskGB: disk, Public: s.Public(),
		})
	}
	return d
}

type sourcedTemplate struct {
	template cloud.Template
	source   string
}

// availableTemplates returns Simplifyd's templates, then the workspace's own
// published ones when a workspace is given. A template that is both is listed
// once, as Simplifyd's.
func availableTemplates(ctx context.Context, api *cloud.Client, workspace string) ([]sourcedTemplate, error) {
	catalog, err := api.Templates().List(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].Name < catalog[j].Name })
	out := make([]sourcedTemplate, 0, len(catalog))
	seen := map[string]bool{}
	for _, t := range catalog {
		out = append(out, sourcedTemplate{t, templateSourceSimplifyd})
		seen[t.Slug] = true
	}
	if workspace == "" {
		return out, nil
	}
	own, err := api.Workspace(workspace).Templates().List(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(own, func(i, j int) bool { return own[i].Name < own[j].Name })
	for _, t := range own {
		if t.Status == "published" && !seen[t.Slug] {
			out = append(out, sourcedTemplate{t, templateSourceWorkspace})
		}
	}
	return out, nil
}

// findTemplate resolves a template by slug or, when unambiguous, by name.
func findTemplate(ctx context.Context, api *cloud.Client, workspace, ref string) (*sourcedTemplate, error) {
	templates, err := availableTemplates(ctx, api, workspace)
	if err != nil {
		return nil, err
	}
	var byName []*sourcedTemplate
	names := make([]string, 0, len(templates))
	for i := range templates {
		t := &templates[i]
		if t.template.Slug == ref {
			return t, nil
		}
		if strings.EqualFold(t.template.Name, ref) {
			byName = append(byName, t)
		}
		names = append(names, t.template.Name)
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		// A workspace's draft is not listed but may still be deployed by slug.
		if workspace != "" && isSlug(ref) {
			if t, err := api.Workspace(workspace).Templates().Get(ctx, ref); err == nil {
				return &sourcedTemplate{*t, templateSourceWorkspace}, nil
			}
		}
		return nil, notFound("template", ref, names)
	default:
		slugs := make([]string, len(byName))
		for i, t := range byName {
			slugs[i] = fmt.Sprintf("%s (%s)", t.template.Slug, t.source)
		}
		return nil, fmt.Errorf("more than one template is named %q; pass its slug: %s", ref, strings.Join(slugs, ", "))
	}
}

// ---- list-templates ----

type listTemplatesArgs struct {
	Workspace string `json:"workspace,omitempty" jsonschema:"Workspace slug or name. Optional: adds the workspace's own published templates to Simplifyd's"`
}

func handleListTemplates(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args listTemplatesArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	templates, err := availableTemplates(ctx, api, args.Workspace)
	if err != nil {
		return apiErr("list templates", err), nil, nil
	}
	out := make([]templateSummary, len(templates))
	for i, t := range templates {
		out[i] = summarizeTemplate(t.template, t.source)
	}
	return jsonText(out), nil, nil
}

// ---- get-template ----

type getTemplateArgs struct {
	Workspace string `json:"workspace,omitempty" jsonschema:"Workspace slug or name. Needed only for the workspace's own templates"`
	Template  string `json:"template"            jsonschema:"Template slug or name, e.g. Supabase"`
}

func handleGetTemplate(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args getTemplateArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	t, err := findTemplate(ctx, api, args.Workspace, args.Template)
	if err != nil {
		return toolError(err.Error()), nil, nil
	}
	return jsonText(describeTemplate(t.template, t.source)), nil, nil
}

// ---- deploy-template ----

type deployTemplateArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name to create the services in"`
	Template  string `json:"template"  jsonschema:"Template slug or name, e.g. Supabase"`
}

type deployTemplateResult struct {
	Template string              `json:"template"`
	Services []createdSvcSummary `json:"services"`
	Next     string              `json:"next"`
}

type createdSvcSummary struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

func handleDeployTemplate(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args deployTemplateArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	t, err := findTemplate(ctx, api, args.Workspace, args.Template)
	if err != nil {
		return toolError(err.Error()), nil, nil
	}
	result, err := api.Workspace(args.Workspace).Templates().Deploy(ctx, t.template.Slug, cloud.DeployTemplateInput{
		Project: args.Project,
		Env:     args.Env,
	})
	if err != nil {
		return apiErr("deploy template", err), nil, nil
	}
	out := deployTemplateResult{
		Template: t.template.Name,
		Next:     "The services are created but not running. Start each with deploy-service.",
	}
	for _, s := range result.Services {
		out.Services = append(out.Services, createdSvcSummary{Slug: s.Slug, Name: s.Name})
	}
	return jsonText(out), nil, nil
}

// RegisterTemplateTools registers the template tools on s.
func RegisterTemplateTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "list-templates",
		Description: "List templates that can be deployed: Simplifyd's own (such as Supabase), plus a workspace's published templates when a workspace is given. Each shows how many services it creates and their total vCPU, memory and disk.",
	}, handleListTemplates)

	addTool(s, &mcp.Tool{
		Name:        "get-template",
		Description: "Show the services a template creates: each service's name, image, vCPU, memory, disk and whether it is public.",
	}, handleGetTemplate)

	addTool(s, &mcp.Tool{
		Name:        "deploy-template",
		Description: "Create every service in a template in one environment. This adds billed services: show the user what get-template reports (services, vCPU, memory, disk) and get their go-ahead first. Services are created, not started; start each with deploy-service. Secrets are generated for this deployment only. Fails if the environment already has a service with one of the template's names.",
	}, handleDeployTemplate)
}
