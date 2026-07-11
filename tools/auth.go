package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/simplifyd-com/cloud-mcp/client"
)

// ---- login ----

type loginArgs struct {
	Email    string `json:"email"    jsonschema:"Your Simplifyd Cloud account email address"`
	Password string `json:"password" jsonschema:"Your account password"`
}

func handleLogin(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args loginArgs,
) (*mcp.CallToolResult, any, error) {
	resp, err := sdk().Login(ctx, args.Email, args.Password)
	if err != nil {
		return apiErr("login failed", err), nil, nil
	}

	// Persist token for this session and to disk
	setToken(resp.Token)
	cfg := &client.Config{
		Token: resp.Token,
		ActiveEnv: client.ActiveEnv{
			Workspace: resp.ActiveWorkspace,
			Project:   resp.ActiveProject,
			Env:       resp.ActiveEnv,
		},
	}
	_ = client.SaveConfig(cfg) // best-effort

	return text(fmt.Sprintf(
		"Logged in as %s\nActive workspace: %s | project: %s | env: %s\nToken saved to ~/.simplifyd/config.json",
		args.Email,
		resp.ActiveWorkspace, resp.ActiveProject, resp.ActiveEnv,
	)), nil, nil
}

// ---- get-me ----

func handleGetMe(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ struct{},
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	u, err := sdk().Me(ctx)
	if err != nil {
		return apiErr("get current user", err), nil, nil
	}
	return jsonText(u), nil, nil
}

// RegisterAuthTools registers all authentication-related MCP tools on s.
func RegisterAuthTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "login",
		Description: "Log in to Simplifyd Cloud with your email and password. Saves the session token for subsequent calls.",
	}, handleLogin)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-me",
		Description: "Return the profile of the currently authenticated user.",
	}, handleGetMe)
}
