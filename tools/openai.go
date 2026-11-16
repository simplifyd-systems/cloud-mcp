package tools

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// RegisterOpenAITools is the complete catalog for the directory endpoint.
// Register operations individually so additions to the general server cannot
// grant the listing access to new capabilities or broader results.
func RegisterOpenAITools(s *mcp.Server, challenge string) {
	addOpenAITool(s, challenge, "list-workspaces", "List the names and slugs of workspaces shared with this connection.", openAIListWorkspaces)
	addOpenAITool(s, challenge, "list-projects", "List the project names and slugs in an existing workspace.", openAIListProjects)
	addOpenAITool(s, challenge, "list-environments", "List the environment names and slugs in an existing project.", openAIListEnvironments)
	addOpenAITool(s, challenge, "list-services", "List existing services with their names, types, status, and compute allocation.", openAIListServices)
	addOpenAITool(s, challenge, "get-service", "Get an existing service's type, status, and compute allocation.", openAIGetService)
	addOpenAITool(s, challenge, "list-deployments", "List an existing service's deployments, including status, deployment time, and which is active.", openAIListDeployments)
	addOpenAITool(s, challenge, "get-deployment", "Get the status, deployment time, and active state of a specific deployment.", openAIGetDeployment)
	addOpenAITool(s, challenge, "get-service-metrics", "Summarize CPU, memory, and disk usage for an existing service over the selected period.", openAIServiceMetrics)
	addOpenAITool(s, challenge, "list-postgres-extensions", "List the installed and supported extensions for a database on an existing managed PostgreSQL service.", openAIListPostgresExtensions)
	addOpenAITool(s, challenge, "list-domains", "List a workspace's existing registered domains with status, expiration, and auto-renewal status.", openAIListDomains)
	addOpenAITool(s, challenge, "get-domain", "Get an existing registered domain's status, expiration, auto-renewal status, nameservers, and transfer lock.", openAIGetDomain)
	addOpenAITool(s, challenge, "list-dns-zones", "List a workspace's hosted DNS zones, their activation status, and Simplifyd Cloud nameservers.", openAIListDNSZones)
	addOpenAITool(s, challenge, "check-dns-zone", "Look up public nameservers for an existing hosted DNS zone. If a pending zone is delegated to Simplifyd Cloud, activate it and publish its stored records, replacing any records left on the Simplifyd nameservers for the zone. Confirm with the user before calling.", openAICheckDNSZone)
}

func addOpenAITool[In any](s *mcp.Server, challenge, name, description string, query func(context.Context, *cloud.Client, In) (any, error)) {
	tool := &mcp.Tool{
		Name:        name,
		Description: description,
		Meta:        mcp.Meta{"securitySchemes": []any{map[string]any{"type": "oauth2", "scopes": []string{"workspace"}}}},
	}
	annotate(tool)
	mcp.AddTool(s, tool, func(ctx context.Context, req *mcp.CallToolRequest, args In) (*mcp.CallToolResult, any, error) {
		api, _, ok := sdkFor(req)
		if !ok {
			return openAIError(http.StatusUnauthorized, challenge), nil, nil
		}
		if err := resolveArgs(ctx, api, &args); err != nil {
			return openAIFailure(err, challenge), nil, nil
		}
		data, err := query(ctx, api, args)
		if err != nil {
			return openAIFailure(err, challenge), nil, nil
		}
		return jsonText(data), nil, nil
	})
}

func openAIFailure(err error, challenge string) *mcp.CallToolResult {
	var apiError *cloud.APIError
	var missing *missError
	switch {
	case errors.As(err, &apiError):
		return openAIError(apiError.StatusCode, challenge)
	case errors.As(err, &missing):
		return openAIError(http.StatusNotFound, challenge)
	case errors.Is(err, errConfirmDNSActivation):
		return toolError(errConfirmDNSActivation.Error())
	default:
		return toolError("The operation could not be completed. Check the resource name and try again.")
	}
}

// Return actionable errors without echoing backend payloads, user inputs, or
// request IDs into model context.
func openAIError(status int, challenge string) *mcp.CallToolResult {
	switch status {
	case http.StatusUnauthorized:
		result := toolError("Reconnect your Simplifyd Cloud account to continue.")
		result.Meta = mcp.Meta{"mcp/www_authenticate": []string{challenge}}
		return result
	case http.StatusForbidden:
		return toolError("Your connection does not have permission for this resource or operation.")
	case http.StatusNotFound:
		return toolError("The resource was not found in the connected workspace.")
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return toolError("The request was rejected. Check the input and try again.")
	case http.StatusConflict:
		return toolError("The request conflicts with the resource's current state.")
	case http.StatusTooManyRequests:
		return toolError("The request was rate limited. Try again shortly.")
	default:
		return toolError("Simplifyd Cloud could not complete the operation. Try again shortly.")
	}
}

type resourceName struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

func openAIListWorkspaces(ctx context.Context, api *cloud.Client, _ struct{}) (any, error) {
	workspaces, err := api.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]resourceName, len(workspaces))
	for i, workspace := range workspaces {
		result[i] = resourceName{Slug: workspace.Slug, Name: workspace.Name}
	}
	return result, nil
}

func openAIListProjects(ctx context.Context, api *cloud.Client, args listProjectsArgs) (any, error) {
	projects, err := api.Workspace(args.Workspace).ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]resourceName, len(projects))
	for i, project := range projects {
		result[i] = resourceName{Slug: project.Slug, Name: project.Name}
	}
	return result, nil
}

func openAIListEnvironments(ctx context.Context, api *cloud.Client, args envListArgs) (any, error) {
	envs, err := api.Workspace(args.Workspace).Project(args.Project).ListEnvs(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]resourceName, len(envs))
	for i, env := range envs {
		result[i] = resourceName{Slug: env.Slug, Name: env.Name}
	}
	return result, nil
}

type serviceStatus struct {
	resourceName
	Type     cloud.ServiceType   `json:"type"`
	Status   cloud.ServiceStatus `json:"status"`
	VCPUs    uint                `json:"vcpus"`
	Memory   uint                `json:"memory_mib"`
	Replicas uint                `json:"replicas"`
}

func summariseService(s cloud.Service) serviceStatus {
	return serviceStatus{
		resourceName: resourceName{Slug: s.Slug, Name: s.Name},
		Type:         s.Type, Status: s.Status, VCPUs: s.VCPUs, Memory: s.Memory, Replicas: s.Replicas,
	}
}

func openAIListServices(ctx context.Context, api *cloud.Client, args listServicesArgs) (any, error) {
	svcs, err := services(api, args.Workspace, args.Project, args.Env).List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]serviceStatus, len(svcs))
	for i, svc := range svcs {
		result[i] = summariseService(svc)
	}
	return result, nil
}

func openAIGetService(ctx context.Context, api *cloud.Client, args svcArgs) (any, error) {
	svc, err := services(api, args.Workspace, args.Project, args.Env).Get(ctx, args.Service)
	if err != nil {
		return nil, err
	}
	return summariseService(*svc), nil
}

type deploymentStatus struct {
	Slug       string                 `json:"slug"`
	Status     cloud.DeploymentStatus `json:"status"`
	Active     bool                   `json:"active"`
	DeployedAt time.Time              `json:"deployed_at"`
}

func summariseDeployment(d cloud.Deployment) deploymentStatus {
	return deploymentStatus{Slug: d.Slug, Status: d.Status, Active: d.Active, DeployedAt: d.DeployedAt}
}

func openAIListDeployments(ctx context.Context, api *cloud.Client, args svcArgs) (any, error) {
	deployments, err := services(api, args.Workspace, args.Project, args.Env).ListDeployments(ctx, args.Service)
	if err != nil {
		return nil, err
	}
	result := make([]deploymentStatus, len(deployments))
	for i, deployment := range deployments {
		result[i] = summariseDeployment(deployment)
	}
	return result, nil
}

func openAIGetDeployment(ctx context.Context, api *cloud.Client, args deploymentArgs) (any, error) {
	d, err := services(api, args.Workspace, args.Project, args.Env).GetDeployment(ctx, args.Service, args.Deployment)
	if err != nil {
		return nil, err
	}
	return summariseDeployment(*d), nil
}

func openAIServiceMetrics(ctx context.Context, api *cloud.Client, args serviceMetricsArgs) (any, error) {
	rng, err := parseMetricsRange(args.Range)
	if err != nil {
		return nil, &cloud.APIError{StatusCode: http.StatusBadRequest}
	}
	metrics, err := services(api, args.Workspace, args.Project, args.Env).Metrics(ctx, args.Service, rng)
	if err != nil {
		return nil, err
	}
	return summariseMetrics(args.Service, metrics, args.IncludeSeries), nil
}

func openAIListPostgresExtensions(ctx context.Context, api *cloud.Client, args postgresExtensionsArgs) (any, error) {
	database := databaseOrDefault(args.Database)
	extensions, err := services(api, args.Workspace, args.Project, args.Env).ListPostgresExtensions(ctx, args.Service, database)
	if err != nil {
		return nil, err
	}
	return map[string]any{"database": database, "installed": extensions.Installed(), "supported": extensions.Supported}, nil
}

type registeredDomainStatus struct {
	resourceName
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	AutoRenew bool       `json:"auto_renew"`
}

func summariseDomain(d cloud.Domain) registeredDomainStatus {
	return registeredDomainStatus{
		resourceName: resourceName{Slug: d.Slug, Name: d.Name},
		Status:       d.Status, ExpiresAt: d.ExpiresAt, AutoRenew: d.AutoRenew,
	}
}

func openAIListDomains(ctx context.Context, api *cloud.Client, args workspaceArgs) (any, error) {
	list, err := api.Workspace(args.Workspace).Domains().List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]registeredDomainStatus, len(list.Domains))
	for i, domain := range list.Domains {
		result[i] = summariseDomain(domain)
	}
	return result, nil
}

func openAIGetDomain(ctx context.Context, api *cloud.Client, args domainArgs) (any, error) {
	domains := api.Workspace(args.Workspace).Domains()
	domain, err := findDomain(ctx, domains, args.Domain)
	if err != nil {
		return nil, err
	}
	detail, err := domains.Get(ctx, domain.Slug)
	if err != nil {
		return nil, err
	}
	return struct {
		registeredDomainStatus
		Locked      bool     `json:"locked"`
		Nameservers []string `json:"nameservers,omitempty"`
	}{summariseDomain(detail.Domain), detail.Locked, detail.Nameservers}, nil
}

type dnsZoneStatus struct {
	resourceName
	Status      string   `json:"status"`
	Nameservers []string `json:"nameservers,omitempty"`
}

func summariseDNSZone(z cloud.DNSZone) dnsZoneStatus {
	return dnsZoneStatus{resourceName{Slug: z.Slug, Name: z.Name}, z.Status, z.Nameservers}
}

func openAIListDNSZones(ctx context.Context, api *cloud.Client, args workspaceArgs) (any, error) {
	list, err := api.Workspace(args.Workspace).DNS().Zones(ctx)
	if err != nil {
		return nil, err
	}
	zones := make([]dnsZoneStatus, len(list.Zones))
	for i, zone := range list.Zones {
		zones[i] = summariseDNSZone(zone)
	}
	return struct {
		Zones       []dnsZoneStatus `json:"zones"`
		Nameservers []string        `json:"nameservers"`
	}{zones, list.Nameservers}, nil
}

type checkOpenAIDNSZoneArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Zone      string `json:"zone" jsonschema:"Existing DNS zone's name or slug"`
	Confirm   bool   `json:"confirm" jsonschema:"Set true after the user confirms publishing the stored records if the zone is delegated"`
}

func openAICheckDNSZone(ctx context.Context, api *cloud.Client, args checkOpenAIDNSZoneArgs) (any, error) {
	if !args.Confirm {
		return nil, errConfirmDNSActivation
	}
	zone, err := checkDNSZone(ctx, api.Workspace(args.Workspace).DNS(), args.Zone)
	if err != nil {
		return nil, err
	}
	return summariseDNSZone(*zone), nil
}

var errConfirmDNSActivation = errors.New("Confirm that the zone's stored records can be published before checking delegation.")
