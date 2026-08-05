package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- list-projects ----

type listProjectsArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
}

func handleListProjects(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args listProjectsArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	projects, err := api.Workspace(args.Workspace).ListProjects(ctx)
	if err != nil {
		return apiErr("list projects", err), nil, nil
	}
	return jsonText(projects), nil, nil
}

// ---- get-project ----

type projectArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
}

func handleGetProject(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args projectArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	p, err := api.Workspace(args.Workspace).Project(args.Project).Get(ctx)
	if err != nil {
		return apiErr("get project", err), nil, nil
	}
	return jsonText(p), nil, nil
}

// ---- create-project ----

type createProjectArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Name      string `json:"name"      jsonschema:"Display name for the new project"`
}

func handleCreateProject(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args createProjectArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	p, err := api.Workspace(args.Workspace).CreateProject(ctx, args.Name)
	if err != nil {
		return apiErr("create project", err), nil, nil
	}
	return jsonText(p), nil, nil
}

// ---- update-project ----

type updateProjectArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
	Name      string `json:"name"      jsonschema:"New display name for the project"`
}

func handleUpdateProject(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args updateProjectArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	p, err := api.Workspace(args.Workspace).Project(args.Project).Update(ctx, args.Name)
	if err != nil {
		return apiErr("update project", err), nil, nil
	}
	return jsonText(p), nil, nil
}

// RegisterProjectTools registers all project-related MCP tools on s.
func RegisterProjectTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list-projects",
		Description: "List all projects in a workspace.",
	}, handleListProjects)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-project",
		Description: "Get details of a specific project.",
	}, handleGetProject)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create-project",
		Description: "Create a new project inside a workspace.",
	}, handleCreateProject)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update-project",
		Description: "Rename an existing project.",
	}, handleUpdateProject)
}
