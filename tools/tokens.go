package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- common args ----

type tokenListArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Project   string `json:"project"   jsonschema:"Project slug"`
}

// ---- list-tokens ----

func handleListTokens(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args tokenListArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	tokens, err := sdk().Workspace(args.Workspace).Project(args.Project).Tokens().List(ctx)
	if err != nil {
		return apiErr("list tokens", err), nil, nil
	}
	return jsonText(tokens), nil, nil
}

// ---- create-token ----

type createTokenArgs struct {
	Workspace string `json:"workspace"  jsonschema:"Workspace slug"`
	Project   string `json:"project"    jsonschema:"Project slug"`
	Name      string `json:"name"       jsonschema:"Display name for the token (e.g. CI/CD Token)"`
	Env       string `json:"env,omitempty" jsonschema:"Optional environment slug; omit for access to all environments in the project"`
}

func handleCreateToken(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args createTokenArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	token, err := sdk().Workspace(args.Workspace).Project(args.Project).Tokens().Create(ctx, args.Name, args.Env)
	if err != nil {
		return apiErr("create token", err), nil, nil
	}
	return jsonTextRaw(token), nil, nil
}

// ---- delete-token ----

type deleteTokenArgs struct {
	Workspace string `json:"workspace"  jsonschema:"Workspace slug"`
	Project   string `json:"project"    jsonschema:"Project slug"`
	Token     string `json:"token"      jsonschema:"Token slug or ID to revoke"`
}

func handleDeleteToken(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args deleteTokenArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	if err := sdk().Workspace(args.Workspace).Project(args.Project).Tokens().Delete(ctx, args.Token); err != nil {
		return apiErr("delete token", err), nil, nil
	}
	return text("Token revoked successfully"), nil, nil
}

// RegisterTokenTools registers all token-related MCP tools on s.
func RegisterTokenTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list-tokens",
		Description: "List all API tokens for a project.",
	}, handleListTokens)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create-token",
		Description: "Create a project API token. This explicitly reveals the full secret key once. Omit env for all project environments, or provide env to restrict it.",
	}, handleCreateToken)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete-token",
		Description: "Revoke (delete) a project API token.",
	}, handleDeleteToken)
}
