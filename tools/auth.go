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

// handleLogin is registered only for the stdio transport. It mutates
// process-wide state and writes ~/.simplifyd/config.json, both of which are
// per-machine concepts: on a hosted server they would apply one caller's
// credentials to every other connected caller.
func handleLogin(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args loginArgs,
) (*mcp.CallToolResult, any, error) {
	if req != nil && req.Extra != nil {
		return toolError(
			"login is not available over HTTP — authenticate by sending a " +
				"Simplifyd API token as an Authorization: Bearer <token> header",
		), nil, nil
	}
	api, _ := localClient()
	resp, err := api.Login(ctx, args.Email, args.Password)
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
	saveErr := client.SaveConfig(cfg)

	message := fmt.Sprintf(
		"Logged in as %s\nActive workspace: %s | project: %s | env: %s\nToken saved to ~/.simplifyd/config.json",
		args.Email,
		resp.ActiveWorkspace, resp.ActiveProject, resp.ActiveEnv,
	)
	if saveErr != nil {
		message = fmt.Sprintf(
			"Logged in as %s\nActive workspace: %s | project: %s | env: %s\nWarning: login succeeded for this session, but the token could not be saved.",
			args.Email, resp.ActiveWorkspace, resp.ActiveProject, resp.ActiveEnv,
		)
	}
	return text(message), nil, nil
}

// ---- get-me ----

func handleGetMe(
	ctx context.Context,
	req *mcp.CallToolRequest,
	_ struct{},
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	u, err := api.Me(ctx)
	if err != nil {
		return apiErr("get current user", err), nil, nil
	}
	return jsonText(u), nil, nil
}

// RegisterAuthTools registers the authentication tools on s. The login tool is
// registered only when local is true (stdio), since it persists credentials to
// the machine running the server — see handleLogin.
func RegisterAuthTools(s *mcp.Server, local bool) {
	if local {
		mcp.AddTool(s, &mcp.Tool{
			Name:        "login",
			Description: "Log in to Simplifyd Cloud with your email and password. Saves the session token for subsequent calls.",
		}, handleLogin)
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-me",
		Description: "Return the profile of the currently authenticated user.",
	}, handleGetMe)
}
