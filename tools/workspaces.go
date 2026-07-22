package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- list-workspaces ----

func handleListWorkspaces(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ struct{},
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	workspaces, err := sdk().ListWorkspaces(ctx)
	if err != nil {
		return apiErr("list workspaces", err), nil, nil
	}
	return jsonText(workspaces), nil, nil
}

// ---- get-workspace ----

type workspaceSlugArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
}

func handleGetWorkspace(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args workspaceSlugArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	ws, err := sdk().Workspace(args.Workspace).Get(ctx)
	if err != nil {
		return apiErr("get workspace", err), nil, nil
	}
	return jsonText(ws), nil, nil
}

// ---- create-workspace ----

type createWorkspaceArgs struct {
	Name string `json:"name" jsonschema:"Display name for the new workspace"`
}

func handleCreateWorkspace(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args createWorkspaceArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	ws, err := sdk().CreateWorkspace(ctx, args.Name)
	if err != nil {
		return apiErr("create workspace", err), nil, nil
	}
	return jsonText(ws), nil, nil
}

// ---- update-workspace ----

type updateWorkspaceArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Name      string `json:"name"      jsonschema:"New display name for the workspace"`
}

func handleUpdateWorkspace(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args updateWorkspaceArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	ws, err := sdk().Workspace(args.Workspace).Update(ctx, args.Name)
	if err != nil {
		return apiErr("update workspace", err), nil, nil
	}
	return jsonText(ws), nil, nil
}

// ---- get-workspace-usage ----

func handleGetWorkspaceUsage(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args workspaceSlugArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	usage, err := sdk().Workspace(args.Workspace).Usage(ctx)
	if err != nil {
		return apiErr("get workspace usage", err), nil, nil
	}
	return jsonText(usage), nil, nil
}

// ---- get-workspace-transactions ----

func handleGetWorkspaceTransactions(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args workspaceSlugArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	txns, err := sdk().Workspace(args.Workspace).Transactions(ctx)
	if err != nil {
		return apiErr("get workspace transactions", err), nil, nil
	}
	return jsonText(txns), nil, nil
}

// ---- get-my-role ----

func handleGetMyRole(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args workspaceSlugArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	role, err := sdk().Workspace(args.Workspace).MyRole(ctx)
	if err != nil {
		return apiErr("get my role", err), nil, nil
	}
	return text(fmt.Sprintf("Your role in workspace %s: %s", args.Workspace, role)), nil, nil
}

// ---- list-workspace-members ----

func handleListWorkspaceMembers(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args workspaceSlugArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	members, err := sdk().Workspace(args.Workspace).Members().List(ctx)
	if err != nil {
		return apiErr("list workspace members", err), nil, nil
	}
	return jsonText(members), nil, nil
}

// ---- add-workspace-member ----

type addWorkspaceMemberArgs struct {
	Workspace string   `json:"workspace"      jsonschema:"Workspace slug"`
	Emails    []string `json:"emails"         jsonschema:"Email addresses of the people to invite"`
	Role      string   `json:"role,omitempty" jsonschema:"Role for the invited members: owner, developer (default), or billing"`
}

func handleAddWorkspaceMember(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args addWorkspaceMemberArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	members := sdk().Workspace(args.Workspace).Members()
	var err error
	if args.Role != "" {
		err = members.AddWithRole(ctx, args.Emails, args.Role)
	} else {
		err = members.Add(ctx, args.Emails)
	}
	if err != nil {
		return apiErr("add workspace member", err), nil, nil
	}
	return text("Member invitation(s) sent successfully"), nil, nil
}

// ---- update-member-role ----

type updateMemberRoleArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	MemberID  string `json:"member_id" jsonschema:"Member slug or ID whose role to change"`
	Role      string `json:"role"      jsonschema:"New role: owner, developer, or billing"`
}

func handleUpdateMemberRole(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args updateMemberRoleArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	if err := sdk().Workspace(args.Workspace).Members().UpdateRole(ctx, args.MemberID, args.Role); err != nil {
		return apiErr("update member role", err), nil, nil
	}
	return text("Member role updated successfully"), nil, nil
}

// ---- remove-workspace-member ----

type removeWorkspaceMemberArgs struct {
	Workspace string `json:"workspace"  jsonschema:"Workspace slug"`
	MemberID  string `json:"member_id"  jsonschema:"Member ID to remove"`
}

func handleRemoveWorkspaceMember(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args removeWorkspaceMemberArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	if err := sdk().Workspace(args.Workspace).Members().Remove(ctx, args.MemberID); err != nil {
		return apiErr("remove workspace member", err), nil, nil
	}
	return text("Member removed successfully"), nil, nil
}

// ---- fund-workspace ----

type fundWorkspaceArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug"`
	Method    string `json:"method"    jsonschema:"Payment method: paystack (NGN) | stripe (USD) | bank_transfer (NGN)"`
	Amount    int64  `json:"amount"    jsonschema:"Amount in the smallest currency unit (kobo for NGN or cents for USD)"`
}

func handleFundWorkspace(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args fundWorkspaceArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	result, err := sdk().Workspace(args.Workspace).Fund(ctx, args.Method, args.Amount)
	if err != nil {
		return apiErr("fund workspace", err), nil, nil
	}
	return jsonText(result), nil, nil
}

// ---- registry ----

func handleGetRegistry(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args workspaceSlugArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	reg, err := sdk().Workspace(args.Workspace).Registry().Get(ctx)
	if err != nil {
		return apiErr("get registry", err), nil, nil
	}
	return jsonText(reg), nil, nil
}

func handleGetRegistryCredentials(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args workspaceSlugArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	creds, err := sdk().Workspace(args.Workspace).Registry().Credentials(ctx)
	if err != nil {
		return apiErr("get registry credentials", err), nil, nil
	}
	return jsonTextRaw(creds), nil, nil
}

func handleListRegistryRepos(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	args workspaceSlugArgs,
) (*mcp.CallToolResult, any, error) {
	if r, ok := requireAuth(); !ok {
		return r, nil, nil
	}
	repos, err := sdk().Workspace(args.Workspace).Registry().ListRepos(ctx)
	if err != nil {
		return apiErr("list registry repos", err), nil, nil
	}
	return jsonText(repos), nil, nil
}

// RegisterWorkspaceTools registers all workspace-related MCP tools on s.
func RegisterWorkspaceTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list-workspaces",
		Description: "List all workspaces the authenticated user belongs to.",
	}, handleListWorkspaces)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-workspace",
		Description: "Get details of a specific workspace including wallet balance.",
	}, handleGetWorkspace)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create-workspace",
		Description: "Create a new workspace.",
	}, handleCreateWorkspace)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update-workspace",
		Description: "Rename an existing workspace.",
	}, handleUpdateWorkspace)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-workspace-usage",
		Description: "Get the current-month billing summary for a workspace: usage costs, estimated burn, runway, and wallet balance (owner or billing role required).",
	}, handleGetWorkspaceUsage)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-workspace-transactions",
		Description: "List wallet transactions (fundings and charges) for a workspace (owner or billing role required).",
	}, handleGetWorkspaceTransactions)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-my-role",
		Description: "Get the calling user's role (owner, developer, or billing) in a workspace.",
	}, handleGetMyRole)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list-workspace-members",
		Description: "List all members of a workspace with their roles.",
	}, handleListWorkspaceMembers)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add-workspace-member",
		Description: "Invite one or more users to a workspace by email, optionally with a role (owner, developer, or billing).",
	}, handleAddWorkspaceMember)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update-member-role",
		Description: "Change a workspace member's role (owner only).",
	}, handleUpdateMemberRole)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "remove-workspace-member",
		Description: "Remove a member from a workspace.",
	}, handleRemoveWorkspaceMember)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "fund-workspace",
		Description: "Initiate a wallet top-up for a workspace. Returns a payment URL for Paystack/Stripe or bank account details for bank transfer.",
	}, handleFundWorkspace)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-registry",
		Description: "Get the workspace container registry details (registry URL, project).",
	}, handleGetRegistry)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get-registry-credentials",
		Description: "Explicitly reveal push/pull username, password, and credential data for docker login. Treat the response as secret.",
	}, handleGetRegistryCredentials)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list-registry-repos",
		Description: "List repositories in the workspace container registry.",
	}, handleListRegistryRepos)
}
