package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- list-environments ----

type envListArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
}

func handleListEnvironments(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args envListArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	envs, err := sdk().Workspace(args.Workspace).Project(args.Project).ListEnvs(ctx)
	if err != nil {
		return apiErr("list environments", err), nil, nil
	}
	return jsonText(envs), nil, nil
}

// ---- get-environment ----

type envArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
	Env       string `json:"env"       jsonschema:"Environment slug"`
}

func handleGetEnvironment(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args envArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	env, err := sdk().Workspace(args.Workspace).Project(args.Project).Env(args.Env).Get(ctx)
	if err != nil {
		return apiErr("get environment", err), nil, nil
	}
	return jsonText(env), nil, nil
}

// ---- create-environment ----

type createEnvArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
	Name      string `json:"name"      jsonschema:"Display name for the new environment"`
}

func handleCreateEnvironment(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args createEnvArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	env, err := sdk().Workspace(args.Workspace).Project(args.Project).CreateEnv(ctx, args.Name)
	if err != nil {
		return apiErr("create environment", err), nil, nil
	}
	return jsonText(env), nil, nil
}

// ---- update-environment ----

type updateEnvArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
	Env       string `json:"env"       jsonschema:"Environment slug"`
	Name      string `json:"name"      jsonschema:"New display name for the environment"`
}

func handleUpdateEnvironment(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args updateEnvArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	env, err := sdk().Workspace(args.Workspace).Project(args.Project).Env(args.Env).Update(ctx, args.Name)
	if err != nil {
		return apiErr("update environment", err), nil, nil
	}
	return jsonText(env), nil, nil
}

// ---- list-env-variables ----

func handleListEnvVariables(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args envArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	vars, err := sdk().Workspace(args.Workspace).Project(args.Project).Env(args.Env).Variables().List(ctx)
	if err != nil {
		return apiErr("list environment variables", err), nil, nil
	}
	return jsonText(vars), nil, nil
}

// ---- create-env-variable ----

type createEnvVarArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
	Env       string `json:"env"       jsonschema:"Environment slug"`
	Name      string `json:"name"      jsonschema:"Variable name (e.g. DATABASE_URL)"`
	Value     string `json:"value"     jsonschema:"Variable value"`
}

func handleCreateEnvVariable(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args createEnvVarArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	vars := sdk().Workspace(args.Workspace).Project(args.Project).Env(args.Env).Variables()
	// Upsert: the API's create endpoint rejects duplicate names.
	if slug, ok := findVariableSlug(ctx, vars.List, args.Name); ok {
		v, err := vars.Update(ctx, slug, args.Value)
		if err != nil {
			return apiErr("update environment variable", err), nil, nil
		}
		return jsonText(v), nil, nil
	}
	v, err := vars.Set(ctx, args.Name, args.Value)
	if err != nil {
		return apiErr("create environment variable", err), nil, nil
	}
	return jsonText(v), nil, nil
}

// ---- update-env-variable ----

type updateEnvVarArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
	Env       string `json:"env"       jsonschema:"Environment slug"`
	Variable  string `json:"variable"  jsonschema:"Variable slug or ID to update"`
	Value     string `json:"value"     jsonschema:"New variable value"`
}

func handleUpdateEnvVariable(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args updateEnvVarArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	vars := sdk().Workspace(args.Workspace).Project(args.Project).Env(args.Env).Variables()
	v, err := vars.Update(ctx, resolveVariableSlug(ctx, vars.List, args.Variable), args.Value)
	if err != nil {
		return apiErr("update environment variable", err), nil, nil
	}
	return jsonText(v), nil, nil
}

// ---- delete-env-variable ----

type deleteEnvVarArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
	Env       string `json:"env"       jsonschema:"Environment slug"`
	Variable  string `json:"variable"  jsonschema:"Variable slug or ID to delete"`
}

func handleDeleteEnvVariable(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args deleteEnvVarArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	vars := sdk().Workspace(args.Workspace).Project(args.Project).Env(args.Env).Variables()
	if err := vars.Delete(ctx, resolveVariableSlug(ctx, vars.List, args.Variable)); err != nil {
		return apiErr("delete environment variable", err), nil, nil
	}
	return text("Variable deleted successfully"), nil, nil
}

// RegisterEnvironmentTools registers all environment-related MCP tools on s.
func RegisterEnvironmentTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list-environments",
		Description: "List all environments in a project.",
	}, handleListEnvironments)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-environment",
		Description: "Get details of a specific environment including its services and variables.",
	}, handleGetEnvironment)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create-environment",
		Description: "Create a new environment inside a project.",
	}, handleCreateEnvironment)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update-environment",
		Description: "Rename an existing environment.",
	}, handleUpdateEnvironment)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list-env-variables",
		Description: "List all shared environment variables for an environment.",
	}, handleListEnvVariables)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create-env-variable",
		Description: "Create a new shared environment variable (available to all services in the environment).",
	}, handleCreateEnvVariable)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update-env-variable",
		Description: "Update the value of an existing environment variable.",
	}, handleUpdateEnvVariable)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete-env-variable",
		Description: "Delete an environment variable.",
	}, handleDeleteEnvVariable)
}
