package tools

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// ---- list-deployments ----

func handleListDeployments(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args svcArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	deployments, err := services(args.Workspace, args.Project, args.Env).ListDeployments(ctx, args.Service)
	if err != nil {
		return apiErr("list deployments", err), nil, nil
	}
	return jsonText(deployments), nil, nil
}

// ---- get-deployment ----

type deploymentArgs struct {
	Workspace  string `json:"workspace"   jsonschema:"Workspace slug"`
	Project    string `json:"project"     jsonschema:"Project slug"`
	Env        string `json:"env"         jsonschema:"Environment slug"`
	Service    string `json:"service"     jsonschema:"Service slug"`
	Deployment string `json:"deployment"  jsonschema:"Deployment slug (UUID)"`
}

func handleGetDeployment(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args deploymentArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	d, err := services(args.Workspace, args.Project, args.Env).GetDeployment(ctx, args.Service, args.Deployment)
	if err != nil {
		return apiErr("get deployment", err), nil, nil
	}
	return jsonText(d), nil, nil
}

// ---- deploy-service ----

type deployServiceArgs struct {
	Workspace             string `json:"workspace" jsonschema:"Workspace slug"`
	Project               string `json:"project"   jsonschema:"Project slug"`
	Env                   string `json:"env"       jsonschema:"Environment slug"`
	Service               string `json:"service"   jsonschema:"Service slug"`
	AutoApproveChangesets bool   `json:"auto_approve_changesets,omitempty" jsonschema:"Automatically approve any pending changesets before deploying (otherwise the deploy fails if changes are pending)"`
}

func handleDeployService(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args deployServiceArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	d, err := services(args.Workspace, args.Project, args.Env).Deploy(ctx, args.Service,
		cloud.DeployOptions{AutoApproveChangeSets: args.AutoApproveChangesets})
	if err != nil {
		return apiErr("deploy service", err), nil, nil
	}
	return jsonText(d), nil, nil
}

// ---- redeploy-service ----

type redeployServiceArgs struct {
	Workspace             string `json:"workspace" jsonschema:"Workspace slug"`
	Project               string `json:"project"   jsonschema:"Project slug"`
	Env                   string `json:"env"       jsonschema:"Environment slug"`
	Service               string `json:"service"   jsonschema:"Service slug"`
	AutoApproveChangesets bool   `json:"auto_approve_changesets,omitempty" jsonschema:"Automatically approve any pending changesets before redeploying"`
}

func handleRedeployService(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args redeployServiceArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	d, err := services(args.Workspace, args.Project, args.Env).Redeploy(ctx, args.Service,
		cloud.DeployOptions{AutoApproveChangeSets: args.AutoApproveChangesets})
	if err != nil {
		return apiErr("redeploy service", err), nil, nil
	}
	return jsonText(d), nil, nil
}

// ---- undeploy-service ----

func handleUndeployService(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args svcArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	if err := services(args.Workspace, args.Project, args.Env).Undeploy(ctx, args.Service); err != nil {
		return apiErr("undeploy service", err), nil, nil
	}
	return text("Service undeployed successfully"), nil, nil
}

// ---- get-deployment-logs ----

type deploymentLogsArgs struct {
	Workspace  string `json:"workspace"            jsonschema:"Workspace slug"`
	Project    string `json:"project"              jsonschema:"Project slug"`
	Env        string `json:"env"                  jsonschema:"Environment slug"`
	Service    string `json:"service"              jsonschema:"Service slug"`
	Deployment string `json:"deployment"           jsonschema:"Deployment slug (UUID)"`
	MaxLines   int    `json:"max_lines,omitempty"  jsonschema:"Maximum log lines to return (default 500)"`
}

func handleGetDeploymentLogs(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args deploymentLogsArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	maxLines := args.MaxLines
	if maxLines <= 0 {
		maxLines = 500
	}
	// The logs endpoint streams indefinitely; bound the snapshot with a timeout.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	lines, err := services(args.Workspace, args.Project, args.Env).GetLogs(ctx, args.Service, args.Deployment, maxLines)
	if err != nil && len(lines) == 0 {
		return apiErr("get deployment logs", err), nil, nil
	}
	if len(lines) == 0 {
		return text("(no log output)"), nil, nil
	}
	return text(strings.Join(lines, "\n")), nil, nil
}

// RegisterDeploymentTools registers all deployment-related MCP tools on s.
func RegisterDeploymentTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list-deployments",
		Description: "List all deployments for a service, ordered most recent first.",
	}, handleListDeployments)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-deployment",
		Description: "Get details of a specific deployment (status, resource allocation, timestamps).",
	}, handleGetDeployment)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "deploy-service",
		Description: "Deploy a service. To change image or resources first use update-service (which stages a changeset), then deploy with auto_approve_changesets=true.",
	}, handleDeployService)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "redeploy-service",
		Description: "Redeploy the currently active deployment of a service (useful after config or variable changes).",
	}, handleRedeployService)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "undeploy-service",
		Description: "Stop and remove the active deployment of a service without deleting the service itself.",
	}, handleUndeployService)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-deployment-logs",
		Description: "Fetch a snapshot of logs for a deployment (collects streamed lines for up to 10 seconds or max_lines).",
	}, handleGetDeploymentLogs)
}
