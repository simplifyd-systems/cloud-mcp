package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// ---- common args ----

type svcArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
	Env       string `json:"env"       jsonschema:"Environment slug"`
	Service   string `json:"service"   jsonschema:"Service slug"`
}

// services returns a ServicesClient scoped to the given workspace/project/env.
func services(workspace, project, env string) *cloud.ServicesClient {
	return sdk().Workspace(workspace).Project(project).Env(env).Services()
}

// ---- list-services ----

type listServicesArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
	Env       string `json:"env"       jsonschema:"Environment slug"`
}

func handleListServices(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args listServicesArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	svcs, err := services(args.Workspace, args.Project, args.Env).List(ctx)
	if err != nil {
		return apiErr("list services", err), nil, nil
	}
	return jsonText(svcs), nil, nil
}

// ---- get-service ----

func handleGetService(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args svcArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	svc, err := services(args.Workspace, args.Project, args.Env).Get(ctx, args.Service)
	if err != nil {
		return apiErr("get service", err), nil, nil
	}
	return jsonText(svc), nil, nil
}

// ---- create-postgres-backup ----

func handleCreatePostgresBackup(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args svcArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	run, err := services(args.Workspace, args.Project, args.Env).CreatePostgresBackup(ctx, args.Service)
	if err != nil {
		return apiErr("create postgres backup", err), nil, nil
	}
	return jsonText(run), nil, nil
}

type postgresParametersArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
	Env       string `json:"env"       jsonschema:"Environment slug"`
	Service   string `json:"service"   jsonschema:"Managed PostgreSQL service slug"`
}

func handleGetPostgresParameters(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args postgresParametersArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	parameters, err := services(args.Workspace, args.Project, args.Env).GetPostgresParameters(ctx, args.Service)
	if err != nil {
		return apiErr("get PostgreSQL parameters", err), nil, nil
	}
	return jsonText(parameters), nil, nil
}

type updatePostgresParametersArgs struct {
	Workspace  string            `json:"workspace"  jsonschema:"Workspace slug"`
	Project    string            `json:"project"    jsonschema:"Project slug"`
	Env        string            `json:"env"        jsonschema:"Environment slug"`
	Service    string            `json:"service"    jsonschema:"Managed PostgreSQL service slug"`
	Parameters map[string]string `json:"parameters" jsonschema:"Complete replacement map of supported PostgreSQL parameter names to string values; use an empty map to restore platform defaults"`
}

func handleUpdatePostgresParameters(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args updatePostgresParametersArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	parameters, err := services(args.Workspace, args.Project, args.Env).UpdatePostgresParameters(
		ctx,
		args.Service,
		cloud.UpdatePostgresParametersInput{Parameters: args.Parameters},
	)
	if err != nil {
		return apiErr("update PostgreSQL parameters", err), nil, nil
	}
	return jsonText(parameters), nil, nil
}

// ---- create-service ----

type createServiceArgs struct {
	Workspace      string            `json:"workspace"    jsonschema:"Workspace slug"`
	Project        string            `json:"project"      jsonschema:"Project slug"`
	Env            string            `json:"env"          jsonschema:"Environment slug"`
	Name           string            `json:"name"         jsonschema:"Service display name"`
	Type           string            `json:"type"         jsonschema:"Service type: docker, postgres, redis, http_gateway, or s3_bucket"`
	Image          string            `json:"image,omitempty"          jsonschema:"Docker image without tag (required for docker type, e.g. nginx)"`
	Tag            string            `json:"tag,omitempty"            jsonschema:"Docker image tag (e.g. latest)"`
	StorageGB      *uint64           `json:"storage_gb,omitempty"    jsonschema:"Storage in GB (required for postgres and redis types, 1-1000)"`
	Mode           string            `json:"mode,omitempty"           jsonschema:"Postgres: replica or standalone. Redis: standalone, replication, or cluster."`
	RedisReplicas  *int              `json:"redis_replicas,omitempty" jsonschema:"Number of redis replicas (1-10, redis type only)"`
	BucketName     string            `json:"bucket_name,omitempty"    jsonschema:"Bucket name (s3_bucket type only)"`
	BucketRegion   string            `json:"bucket_region,omitempty"  jsonschema:"Bucket region (s3_bucket type only)"`
	ReadinessProbe *serviceProbeArgs `json:"readiness_probe,omitempty" jsonschema:"Optional HTTP readiness probe for a Docker service; enables native rolling deployments"`
}

func handleCreateService(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args createServiceArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	in := cloud.CreateServiceInput{
		Name: args.Name,
		Type: cloud.ServiceType(args.Type),
	}
	if args.ReadinessProbe != nil && in.Type != cloud.ServiceTypeDocker {
		return text("readiness_probe is only supported for docker services"), nil, nil
	}
	switch in.Type {
	case cloud.ServiceTypeDocker:
		probe, probeErr := readinessProbeFromArgs(args.ReadinessProbe)
		if probeErr != nil {
			return text(probeErr.Error()), nil, nil
		}
		in.Docker = &cloud.DockerInput{Image: args.Image, Tag: args.Tag, ReadinessProbe: probe}
	case cloud.ServiceTypePostgres:
		pg := &cloud.PostgresInput{Mode: args.Mode}
		if args.StorageGB != nil {
			pg.StorageGB = *args.StorageGB
		}
		in.Postgres = pg
	case cloud.ServiceTypeRedis:
		redis := &cloud.RedisInput{Mode: args.Mode}
		if args.StorageGB != nil {
			redis.StorageGB = *args.StorageGB
		}
		if args.RedisReplicas != nil {
			redis.Replicas = *args.RedisReplicas
		}
		in.Redis = redis
	case cloud.ServiceTypeS3Bucket:
		in.S3Bucket = &cloud.S3BucketInput{Name: args.BucketName, Region: args.BucketRegion}
	}

	svc, err := services(args.Workspace, args.Project, args.Env).Create(ctx, in)
	if err != nil {
		return apiErr("create service", err), nil, nil
	}
	return jsonText(svc), nil, nil
}

// ---- update-service ----

type updateServiceArgs struct {
	Workspace        string            `json:"workspace"                jsonschema:"Workspace slug"`
	Project          string            `json:"project"                  jsonschema:"Project slug"`
	Env              string            `json:"env"                      jsonschema:"Environment slug"`
	Service          string            `json:"service"                  jsonschema:"Service slug"`
	Action           string            `json:"action"                   jsonschema:"What to update: name, vcpus, replicas, memory, image, start_command, readiness_probe, or delete_readiness_probe"`
	Name             string            `json:"name,omitempty"           jsonschema:"New service name (action: name)"`
	VCPUs            uint              `json:"vcpus,omitempty"          jsonschema:"Number of virtual CPUs (action: vcpus)"`
	Replicas         uint              `json:"replicas,omitempty"       jsonschema:"Number of Docker service replicas, 1-10 (action: replicas)"`
	Memory           uint              `json:"memory,omitempty"         jsonschema:"Memory in MiB (action: memory)"`
	Image            string            `json:"image,omitempty"          jsonschema:"Docker image without tag, e.g. nginx (action: image)"`
	Tag              string            `json:"tag,omitempty"            jsonschema:"Docker image tag, e.g. latest (action: image)"`
	StartCommand     string            `json:"start_command,omitempty"       jsonschema:"Container executable (action: start_command)"`
	StartCommandArgs []string          `json:"start_command_args,omitempty"  jsonschema:"Ordered arguments passed directly to the container executable (action: start_command)"`
	Probe            *serviceProbeArgs `json:"probe,omitempty" jsonschema:"HTTP readiness probe configuration (required for action: readiness_probe)"`
}

type serviceProbeArgs struct {
	Path                string `json:"path"                  jsonschema:"Absolute HTTP path beginning with /"`
	Port                uint   `json:"port"                  jsonschema:"Container port, 1-65535"`
	InitialDelaySeconds int32  `json:"initial_delay_seconds,omitempty" jsonschema:"Seconds before the first probe, default 0"`
	PeriodSeconds       int32  `json:"period_seconds,omitempty"        jsonschema:"Seconds between probes, default 10"`
	TimeoutSeconds      int32  `json:"timeout_seconds,omitempty"       jsonschema:"Probe timeout in seconds, default 1"`
	FailureThreshold    int32  `json:"failure_threshold,omitempty"     jsonschema:"Consecutive failures before unready, default 3"`
	SuccessThreshold    int32  `json:"success_threshold,omitempty"     jsonschema:"Consecutive successes before ready, default 1"`
}

func readinessProbeFromArgs(input *serviceProbeArgs) (*cloud.ServiceProbe, error) {
	if input == nil {
		return nil, nil
	}
	if !strings.HasPrefix(input.Path, "/") {
		return nil, fmt.Errorf("probe.path must be an absolute HTTP path beginning with /")
	}
	if input.Port < 1 || input.Port > 65535 {
		return nil, fmt.Errorf("probe.port must be between 1 and 65535")
	}
	if input.InitialDelaySeconds < 0 || input.PeriodSeconds < 0 ||
		input.TimeoutSeconds < 0 || input.FailureThreshold < 0 ||
		input.SuccessThreshold < 0 {
		return nil, fmt.Errorf("probe timing and threshold values cannot be negative")
	}
	probe := &cloud.ServiceProbe{
		Path:                input.Path,
		Port:                input.Port,
		InitialDelaySeconds: input.InitialDelaySeconds,
		PeriodSeconds:       input.PeriodSeconds,
		TimeoutSeconds:      input.TimeoutSeconds,
		FailureThreshold:    input.FailureThreshold,
		SuccessThreshold:    input.SuccessThreshold,
	}
	if probe.PeriodSeconds == 0 {
		probe.PeriodSeconds = 10
	}
	if probe.TimeoutSeconds == 0 {
		probe.TimeoutSeconds = 1
	}
	if probe.FailureThreshold == 0 {
		probe.FailureThreshold = 3
	}
	if probe.SuccessThreshold == 0 {
		probe.SuccessThreshold = 1
	}
	return probe, nil
}

func handleUpdateService(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args updateServiceArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	if args.Action == "readiness_probe" {
		if args.Probe == nil {
			return text("probe is required for action readiness_probe"), nil, nil
		}
	}
	probe, probeErr := readinessProbeFromArgs(args.Probe)
	if probeErr != nil {
		return text(probeErr.Error()), nil, nil
	}
	svc, err := services(args.Workspace, args.Project, args.Env).Update(ctx, args.Service, cloud.UpdateServiceInput{
		Action:           args.Action,
		Name:             args.Name,
		VCPUs:            args.VCPUs,
		Replicas:         args.Replicas,
		Memory:           args.Memory,
		Image:            args.Image,
		Tag:              args.Tag,
		StartCommand:     args.StartCommand,
		StartCommandArgs: args.StartCommandArgs,
		Probe:            probe,
	})
	if err != nil {
		return apiErr("update service", err), nil, nil
	}
	return jsonText(svc), nil, nil
}

// ---- delete-service ----

func handleDeleteService(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args svcArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	if err := services(args.Workspace, args.Project, args.Env).Delete(ctx, args.Service); err != nil {
		return apiErr("delete service", err), nil, nil
	}
	return text("Service deleted successfully"), nil, nil
}

// ---- list-service-variables ----

func handleListServiceVariables(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args svcArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	vars, err := services(args.Workspace, args.Project, args.Env).Variables(args.Service).List(ctx)
	if err != nil {
		return apiErr("list service variables", err), nil, nil
	}
	return jsonText(vars), nil, nil
}

// ---- set-service-variables ----

type setServiceVariablesArgs struct {
	Workspace string            `json:"workspace"  jsonschema:"Workspace slug"`
	Project   string            `json:"project"    jsonschema:"Project slug"`
	Env       string            `json:"env"        jsonschema:"Environment slug"`
	Service   string            `json:"service"    jsonschema:"Service slug"`
	Variables map[string]string `json:"variables"  jsonschema:"Map of variable names to values to set (bulk)"`
}

func handleSetServiceVariables(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args setServiceVariablesArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	if err := services(args.Workspace, args.Project, args.Env).Variables(args.Service).BulkSet(ctx, args.Variables); err != nil {
		return apiErr("set service variables", err), nil, nil
	}
	return text("Service variables updated successfully"), nil, nil
}

// ---- add-service-variable ----

type addSvcVarArgs struct {
	Workspace string `json:"workspace"  jsonschema:"Workspace slug"`
	Project   string `json:"project"    jsonschema:"Project slug"`
	Env       string `json:"env"        jsonschema:"Environment slug"`
	Service   string `json:"service"    jsonschema:"Service slug"`
	Name      string `json:"name"       jsonschema:"Variable name"`
	Value     string `json:"value"      jsonschema:"Variable value"`
}

func handleAddServiceVariable(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args addSvcVarArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	vars := services(args.Workspace, args.Project, args.Env).Variables(args.Service)
	// Upsert: the API's create endpoint rejects duplicate names.
	if slug, ok := findVariableSlug(ctx, vars.List, args.Name); ok {
		v, err := vars.Update(ctx, slug, args.Value)
		if err != nil {
			return apiErr("update service variable", err), nil, nil
		}
		return jsonText(v), nil, nil
	}
	v, err := vars.Set(ctx, args.Name, args.Value)
	if err != nil {
		return apiErr("add service variable", err), nil, nil
	}
	return jsonText(v), nil, nil
}

// ---- delete-service-variable ----

type deleteSvcVarArgs struct {
	Workspace string `json:"workspace"  jsonschema:"Workspace slug"`
	Project   string `json:"project"    jsonschema:"Project slug"`
	Env       string `json:"env"        jsonschema:"Environment slug"`
	Service   string `json:"service"    jsonschema:"Service slug"`
	Variable  string `json:"variable"   jsonschema:"Variable slug or ID to delete"`
}

func handleDeleteServiceVariable(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args deleteSvcVarArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	vars := services(args.Workspace, args.Project, args.Env).Variables(args.Service)
	if err := vars.Delete(ctx, resolveVariableSlug(ctx, vars.List, args.Variable)); err != nil {
		return apiErr("delete service variable", err), nil, nil
	}
	return text("Service variable deleted successfully"), nil, nil
}

// ---- add-shared-variable ----

type addSharedVarArgs struct {
	Workspace string `json:"workspace"  jsonschema:"Workspace slug"`
	Project   string `json:"project"    jsonschema:"Project slug"`
	Env       string `json:"env"        jsonschema:"Environment slug"`
	Service   string `json:"service"    jsonschema:"Service slug"`
	Variable  string `json:"variable"   jsonschema:"Shared environment variable slug to link to this service"`
}

func handleAddSharedVariable(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args addSharedVarArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	if err := services(args.Workspace, args.Project, args.Env).Variables(args.Service).AddShared(ctx, args.Variable); err != nil {
		return apiErr("add shared variable", err), nil, nil
	}
	return text("Shared variable linked successfully"), nil, nil
}

// ---- add-service-ingress ----

type addIngressArgs struct {
	Workspace  string `json:"workspace"             jsonschema:"Workspace slug"`
	Project    string `json:"project"               jsonschema:"Project slug"`
	Env        string `json:"env"                   jsonschema:"Environment slug"`
	Service    string `json:"service"               jsonschema:"Service slug"`
	CustomFQDN string `json:"custom_fqdn,omitempty" jsonschema:"Custom fully qualified domain name (leave empty for auto-generated subdomain)"`
	Port       int    `json:"port,omitempty"        jsonschema:"Container port to route traffic to"`
	Protocol   string `json:"protocol,omitempty"    jsonschema:"Protocol: http (default) or grpc"`
}

func handleAddServiceIngress(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args addIngressArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	protocol := args.Protocol
	if protocol == "" {
		protocol = "http"
	}
	ingress, err := services(args.Workspace, args.Project, args.Env).Ingress(args.Service).Add(ctx, cloud.AddIngressInput{
		Protocol:   protocol,
		Port:       args.Port,
		CustomFQDN: args.CustomFQDN,
	})
	if err != nil {
		return apiErr("add service ingress", err), nil, nil
	}
	return jsonText(ingress), nil, nil
}

// ---- delete-service-ingress ----

type deleteIngressArgs struct {
	Workspace string `json:"workspace"  jsonschema:"Workspace slug"`
	Project   string `json:"project"    jsonschema:"Project slug"`
	Env       string `json:"env"        jsonschema:"Environment slug"`
	Service   string `json:"service"    jsonschema:"Service slug"`
	FQDN      string `json:"fqdn"       jsonschema:"Fully qualified domain name to remove"`
}

func handleDeleteServiceIngress(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args deleteIngressArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	if err := services(args.Workspace, args.Project, args.Env).Ingress(args.Service).DeleteFQDN(ctx, args.FQDN); err != nil {
		return apiErr("delete service ingress", err), nil, nil
	}
	return text(fmt.Sprintf("Ingress for %s removed successfully", args.FQDN)), nil, nil
}

// ---- add-tcp-proxy ----

type tcpProxyArgs struct {
	Workspace string `json:"workspace"  jsonschema:"Workspace slug"`
	Project   string `json:"project"    jsonschema:"Project slug"`
	Env       string `json:"env"        jsonschema:"Environment slug"`
	Service   string `json:"service"    jsonschema:"Service slug"`
	Port      uint   `json:"port"       jsonschema:"Container port to expose via TCP proxy"`
}

type addTCPProxyArgs struct {
	Workspace           string   `json:"workspace"                       jsonschema:"Workspace slug"`
	Project             string   `json:"project"                         jsonschema:"Project slug"`
	Env                 string   `json:"env"                             jsonschema:"Environment slug"`
	Service             string   `json:"service"                         jsonschema:"Service slug"`
	Port                uint     `json:"port"                            jsonschema:"Container port to expose via TCP proxy"`
	AllowedSourceRanges []string `json:"allowed_source_ranges,omitempty" jsonschema:"Client IPs/CIDRs allowed to connect (bare IPs treated as /32); empty means open to all"`
}

func handleAddTCPProxy(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args addTCPProxyArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	result, err := services(args.Workspace, args.Project, args.Env).AddTCPProxy(ctx, args.Service, args.Port, args.AllowedSourceRanges...)
	if err != nil {
		return apiErr("add TCP proxy", err), nil, nil
	}
	return jsonText(result), nil, nil
}

// ---- set-ingress-source-ranges ----

type setIngressSourceRangesArgs struct {
	Workspace           string   `json:"workspace"             jsonschema:"Workspace slug"`
	Project             string   `json:"project"               jsonschema:"Project slug"`
	Env                 string   `json:"env"                   jsonschema:"Environment slug"`
	Service             string   `json:"service"               jsonschema:"Service slug"`
	Ingress             string   `json:"ingress"               jsonschema:"Ingress port slug (from get-service ingress list)"`
	AllowedSourceRanges []string `json:"allowed_source_ranges" jsonschema:"Client IPs/CIDRs allowed to connect (bare IPs treated as /32); empty list opens the port to all IPs"`
}

func handleSetIngressSourceRanges(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args setIngressSourceRangesArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	ranges := args.AllowedSourceRanges
	if ranges == nil {
		ranges = []string{}
	}
	if err := services(args.Workspace, args.Project, args.Env).Ingress(args.Service).SetSourceRanges(ctx, args.Ingress, ranges); err != nil {
		return apiErr("set ingress source ranges", err), nil, nil
	}
	if len(ranges) == 0 {
		return text("IP allowlist cleared — port is open to all IPs"), nil, nil
	}
	return text(fmt.Sprintf("IP allowlist updated: %d range(s); applies to the live LoadBalancer without a redeploy", len(ranges))), nil, nil
}

// ---- delete-tcp-proxy ----

func handleDeleteTCPProxy(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args tcpProxyArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	if err := services(args.Workspace, args.Project, args.Env).DeleteTCPProxy(ctx, args.Service, args.Port); err != nil {
		return apiErr("delete TCP proxy", err), nil, nil
	}
	return text("TCP proxy removed successfully"), nil, nil
}

// ---- service configs ----

type addConfigArgs struct {
	Workspace string `json:"workspace"  jsonschema:"Workspace slug"`
	Project   string `json:"project"    jsonschema:"Project slug"`
	Env       string `json:"env"        jsonschema:"Environment slug"`
	Service   string `json:"service"    jsonschema:"Service slug"`
	Name      string `json:"name"       jsonschema:"Config file display name"`
	Content   string `json:"content"    jsonschema:"File content; supports ${{VAR_NAME}} interpolation of service variables at deploy time"`
	MountPath string `json:"mount_path" jsonschema:"Absolute path where the file is mounted in the container, e.g. /etc/nginx/nginx.conf"`
}

func handleAddServiceConfig(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args addConfigArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	cfg, err := services(args.Workspace, args.Project, args.Env).Configs(args.Service).Create(ctx, cloud.CreateConfigInput{
		Name:      args.Name,
		Content:   args.Content,
		MountPath: args.MountPath,
	})
	if err != nil {
		return apiErr("add service config", err), nil, nil
	}
	return jsonText(cfg), nil, nil
}

type updateConfigArgs struct {
	Workspace string `json:"workspace"  jsonschema:"Workspace slug"`
	Project   string `json:"project"    jsonschema:"Project slug"`
	Env       string `json:"env"        jsonschema:"Environment slug"`
	Service   string `json:"service"    jsonschema:"Service slug"`
	Config    string `json:"config"     jsonschema:"Config slug to update"`
	Name      string `json:"name"       jsonschema:"Config file display name"`
	Content   string `json:"content"    jsonschema:"File content"`
	MountPath string `json:"mount_path" jsonschema:"Absolute mount path in the container"`
}

func handleUpdateServiceConfig(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args updateConfigArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	cfg, err := services(args.Workspace, args.Project, args.Env).Configs(args.Service).Update(ctx, args.Config, cloud.UpdateConfigInput{
		Name:      args.Name,
		Content:   args.Content,
		MountPath: args.MountPath,
	})
	if err != nil {
		return apiErr("update service config", err), nil, nil
	}
	return jsonText(cfg), nil, nil
}

type deleteConfigArgs struct {
	Workspace string `json:"workspace"  jsonschema:"Workspace slug"`
	Project   string `json:"project"    jsonschema:"Project slug"`
	Env       string `json:"env"        jsonschema:"Environment slug"`
	Service   string `json:"service"    jsonschema:"Service slug"`
	Config    string `json:"config"     jsonschema:"Config slug to delete"`
}

func handleDeleteServiceConfig(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args deleteConfigArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	if err := services(args.Workspace, args.Project, args.Env).Configs(args.Service).Delete(ctx, args.Config); err != nil {
		return apiErr("delete service config", err), nil, nil
	}
	return text("Service config deleted successfully"), nil, nil
}

// ---- changesets ----

func handleApproveChangeset(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args svcArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	if err := services(args.Workspace, args.Project, args.Env).ApproveChangeset(ctx, args.Service); err != nil {
		return apiErr("approve changeset", err), nil, nil
	}
	return text("Pending changeset approved"), nil, nil
}

func handleDiscardChangeset(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args svcArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	if err := services(args.Workspace, args.Project, args.Env).DiscardChangeset(ctx, args.Service); err != nil {
		return apiErr("discard changeset", err), nil, nil
	}
	return text("Pending changeset discarded"), nil, nil
}

// ---- cross-project private access ----

type grantPrivateAccessArgs struct {
	Workspace       string `json:"workspace"       jsonschema:"Workspace slug"`
	Project         string `json:"project"         jsonschema:"Destination service project slug"`
	Env             string `json:"env"             jsonschema:"Destination service environment slug"`
	Service         string `json:"service"         jsonschema:"Destination service slug"`
	ConsumerProject string `json:"consumer_project" jsonschema:"Consumer project slug in the same workspace"`
	Protocol        string `json:"protocol"        jsonschema:"Private protocol: TCP or UDP"`
	Port            uint   `json:"port"            jsonschema:"Destination private port, 1-65535"`
}

func handleGrantPrivateServiceAccess(ctx context.Context, _ *mcp.CallToolRequest, args grantPrivateAccessArgs) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	grant, err := services(args.Workspace, args.Project, args.Env).CreatePrivateAccessGrant(ctx, args.Service, cloud.CreatePrivateAccessGrantInput{ConsumerProject: args.ConsumerProject, Protocol: args.Protocol, Port: args.Port})
	if err != nil {
		return apiErr("grant private service access", err), nil, nil
	}
	return jsonText(grant), nil, nil
}

func handleListPrivateServiceAccess(ctx context.Context, _ *mcp.CallToolRequest, args svcArgs) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	grants, err := services(args.Workspace, args.Project, args.Env).ListPrivateAccessGrants(ctx, args.Service)
	if err != nil {
		return apiErr("list private service access", err), nil, nil
	}
	return jsonText(grants), nil, nil
}

type revokePrivateAccessArgs struct {
	svcArgs
	Grant string `json:"grant" jsonschema:"Private access grant slug"`
}

func handleRevokePrivateServiceAccess(ctx context.Context, _ *mcp.CallToolRequest, args revokePrivateAccessArgs) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	if err := services(args.Workspace, args.Project, args.Env).DeletePrivateAccessGrant(ctx, args.Service, args.Grant); err != nil {
		return apiErr("revoke private service access", err), nil, nil
	}
	return text("Private service access revoked; the live network policy is removed without a redeploy"), nil, nil
}

// RegisterServiceTools registers all service-related MCP tools on s.
func RegisterServiceTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list-services",
		Description: "List all services in an environment.",
	}, handleListServices)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-service",
		Description: "Get full details of a specific service including its status, config, variables, and ingress rules.",
	}, handleGetService)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create-postgres-backup",
		Description: "Start an on-demand base backup for a Postgres service. Use before risky migrations or other operations that require a fresh recovery base. The service must already have a valid backup destination configured.",
	}, handleCreatePostgresBackup)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-postgres-parameters",
		Description: "Get customer-controlled PostgreSQL server parameters and the platform allowlist for a managed Postgres service.",
	}, handleGetPostgresParameters)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update-postgres-parameters",
		Description: "Replace the complete customer-controlled PostgreSQL parameter map for a managed Postgres service. Values are validated against a platform allowlist; an empty map restores platform defaults. Deploy the service afterward to apply changes.",
	}, handleUpdatePostgresParameters)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create-service",
		Description: "Create a new service (docker, postgres, redis, http_gateway, or s3_bucket) in an environment. Docker services may include readiness_probe to enable native rolling deployments from the first deploy.",
	}, handleCreateService)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update-service",
		Description: "Update one aspect of a service via its changeset: name, vcpus, replicas, memory, image, start_command, or readiness_probe; use delete_readiness_probe to remove readiness gating. A readiness probe enables native rolling deployments; without one deployments use Recreate. Changes are staged and applied on the next deploy (or via approve-service-changeset).",
	}, handleUpdateService)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete-service",
		Description: "Delete a service and all its resources.",
	}, handleDeleteService)

	mcp.AddTool(s, &mcp.Tool{Name: "list-private-service-access", Description: "List cross-project grants for a destination service, including consumer project, protocol, and port."}, handleListPrivateServiceAccess)
	mcp.AddTool(s, &mcp.Tool{Name: "grant-private-service-access", Description: "Allow services in another project in the same workspace to connect to one TCP/UDP port on this service over its private hostname. Applies live without a redeploy."}, handleGrantPrivateServiceAccess)
	mcp.AddTool(s, &mcp.Tool{Name: "revoke-private-service-access", Description: "Revoke a cross-project private service access grant. Applies live without a redeploy."}, handleRevokePrivateServiceAccess)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list-service-variables",
		Description: "List all environment variables set directly on a service.",
	}, handleListServiceVariables)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add-service-variable",
		Description: "Add a single environment variable to a service.",
	}, handleAddServiceVariable)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "set-service-variables",
		Description: "Bulk-set environment variables on a service (replaces all existing variables with the provided map).",
	}, handleSetServiceVariables)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete-service-variable",
		Description: "Delete a specific environment variable from a service.",
	}, handleDeleteServiceVariable)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add-service-ingress",
		Description: "Create an HTTP/gRPC ingress rule for a service (exposes it via a public URL on Cloudflare DNS).",
	}, handleAddServiceIngress)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete-service-ingress",
		Description: "Remove an ingress rule from a service by FQDN.",
	}, handleDeleteServiceIngress)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add-tcp-proxy",
		Description: "Add a TCP proxy to expose a service port externally. Optionally restrict which client IPs/CIDRs may connect.",
	}, handleAddTCPProxy)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "set-ingress-source-ranges",
		Description: "Set (replace) the client IP allowlist on a TCP/UDP ingress port's public LoadBalancer. Empty list opens the port to all IPs. Applies live, no redeploy needed.",
	}, handleSetIngressSourceRanges)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete-tcp-proxy",
		Description: "Remove a TCP proxy from a service by container port.",
	}, handleDeleteTCPProxy)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add-service-config",
		Description: "Add a static config file mount to a service. Content supports ${{VAR_NAME}} interpolation at deploy time.",
	}, handleAddServiceConfig)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update-service-config",
		Description: "Update an existing config file mount on a service (name, content, and mount path are all required).",
	}, handleUpdateServiceConfig)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete-service-config",
		Description: "Delete a config file mount from a service.",
	}, handleDeleteServiceConfig)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "approve-service-changeset",
		Description: "Approve a service's pending changeset, applying staged changes (resources, image, etc.).",
	}, handleApproveChangeset)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "discard-service-changeset",
		Description: "Discard a service's pending changeset without applying it.",
	}, handleDiscardChangeset)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add-shared-variable",
		Description: "Link an existing environment-level shared variable into a specific service.",
	}, handleAddSharedVariable)
}
