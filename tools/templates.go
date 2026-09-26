package tools

import (
	"context"
	"encoding/json"
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
	// InitSQLService is where deploy-template's init_sql runs; empty when the
	// template takes none.
	InitSQLService string                   `json:"init_sql_service,omitempty"`
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
	d.InitSQLService, _ = t.TakesInitSQL()
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
	InitSQL   string `json:"init_sql,omitempty" jsonschema:"Optional SQL run once, when the template's database is first created, e.g. the app's tables, row level security policies and triggers. Only for a template whose get-template shows init_sql_service. It runs in one transaction after the template's own setup: all of it applies or, on any error, none of it"`
}

type deployTemplateResult struct {
	Template string              `json:"template"`
	Services []createdSvcSummary `json:"services"`
	InitSQL  string              `json:"init_sql,omitempty"`
	Next     string              `json:"next"`
}

// deployOutput is a value the template marks publishable. It is returned
// unredacted, unlike every other value: these are meant for browser code,
// such as a Supabase URL and anon key.
type deployOutput struct {
	Service     string `json:"service"`
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
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
	initSQLService, takesInitSQL := t.template.TakesInitSQL()
	if strings.TrimSpace(args.InitSQL) != "" && !takesInitSQL {
		return toolError(fmt.Sprintf("%s does not take init_sql; deploy without it and create the schema another way", t.template.Name)), nil, nil
	}
	result, err := api.Workspace(args.Workspace).Templates().Deploy(ctx, t.template.Slug, cloud.DeployTemplateInput{
		Project: args.Project,
		Env:     args.Env,
		InitSQL: args.InitSQL,
	})
	if err != nil {
		return apiErr("deploy template", err), nil, nil
	}
	out := deployTemplateResult{
		Template: t.template.Name,
		Next:     "The services are created but not running. Start each with deploy-service. get-publishable-variables returns the outputs again later.",
	}
	for _, s := range result.Services {
		out.Services = append(out.Services, createdSvcSummary{Slug: s.Slug, Name: s.Name})
	}
	if strings.TrimSpace(args.InitSQL) != "" {
		out.InitSQL = fmt.Sprintf("Runs once, when %s first starts. Its deploy logs show \"init_sql applied\", or \"init_sql failed and was not applied\" with the error; then fix the SQL and run it from the dashboard.", initSQLService)
	}

	// Everything but the publishable outputs goes through the usual redaction.
	var redacted map[string]any
	data, err := json.Marshal(out)
	if err != nil || json.Unmarshal(data, &redacted) != nil {
		return toolError("failed to encode response"), nil, nil
	}
	redactSensitiveFields(redacted)
	if len(result.Outputs) > 0 {
		outputs := make([]deployOutput, len(result.Outputs))
		for i, o := range result.Outputs {
			outputs[i] = deployOutput{Service: o.Service, Name: o.Name, Value: o.Value, Description: o.Description}
		}
		redacted["outputs"] = outputs
	}
	return jsonTextRaw(redacted), nil, nil
}

// ---- get-publishable-variables ----

type publishableVariable struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	Value   string `json:"value"`
}

func handleGetPublishableVariables(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args envArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	vars, err := api.Workspace(args.Workspace).Project(args.Project).Env(args.Env).PublishableVariables(ctx)
	if err != nil {
		return apiErr("get publishable variables", err), nil, nil
	}
	if len(vars) == 0 {
		return text("This environment has no publishable variables. They are set by a template deploy, such as Supabase's URL and anon key, and a variable stops being publishable once its value is edited. No other variable's value is ever returned."), nil, nil
	}
	// Returned unredacted: publishable values are meant for browser code, and
	// the API returns no others.
	out := make([]publishableVariable, len(vars))
	for i, v := range vars {
		out[i] = publishableVariable{Service: v.Service, Name: v.Name, Value: v.Value}
	}
	return jsonTextRaw(out), nil, nil
}

// RegisterTemplateTools registers the template tools on s.
func RegisterTemplateTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "list-templates",
		Description: "List templates that can be deployed: Simplifyd's own (such as Supabase), plus a workspace's published templates when a workspace is given. Each shows how many services it creates and their total vCPU, memory and disk.",
	}, handleListTemplates)

	addTool(s, &mcp.Tool{
		Name:        "get-template",
		Description: "Show the services a template creates: each service's name, image, vCPU, memory, disk and whether it is public. init_sql_service, when present, means deploy-template accepts init_sql and runs it on that service.",
	}, handleGetTemplate)

	addTool(s, &mcp.Tool{
		Name:        "get-publishable-variables",
		Description: "Get an environment's publishable variables: the ones a template marked safe for browser code, such as a Supabase deployment's SUPABASE_PUBLIC_URL, ANON_KEY and SUPABASE_PUBLISHABLE_KEY, for configuring a frontend app. Works for templates deployed earlier, including from the dashboard. Secrets such as a service role key or dashboard password are never returned. A variable whose value the user edited is no longer publishable.",
	}, handleGetPublishableVariables)

	addTool(s, &mcp.Tool{
		Name:        "deploy-template",
		Description: "Create every service in a template in one environment. This adds billed services: show the user what get-template reports (services, vCPU, memory, disk) and get their go-ahead first. Services are created, not started; start each with deploy-service. Secrets are generated for this deployment only. The result's outputs hold the values the template marks publishable, such as a Supabase URL and anon key, which are safe to put in a browser app's config; no other secret is returned. get-publishable-variables returns them again later. Pass init_sql to create an app's schema (tables, policies, triggers) when the database first starts, rather than asking the user to run it by hand. Fails if the environment already has a service with one of the template's names.",
	}, handleDeployTemplate)
}
