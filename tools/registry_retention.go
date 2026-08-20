package tools

import (
	"context"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Registry image-retention tools.
//
// The underlying API replaces a workspace's whole rule list, which makes it easy
// to delete a rule by omission — and a dropped rule means images the user meant
// to keep become deletion candidates on the next run. These tools therefore
// expose reading, previewing, and the enable/disable switch only. Writing rules
// stays in the dashboard and the CLI, where a human sees the preview before
// arming anything. This mirrors the PostgreSQL extension tools for the same
// reason.

type retentionToggleArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Enabled   bool   `json:"enabled"   jsonschema:"true to apply the stored retention rules on the daily schedule, false to stop deleting anything"`
}

func handleGetRegistryRetention(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args workspaceSlugArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	policy, err := api.Workspace(args.Workspace).Registry().Retention().Get(ctx)
	if err != nil {
		return apiErr("get registry retention policy", err), nil, nil
	}
	return jsonText(policy), nil, nil
}

func handlePreviewRegistryRetention(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args workspaceSlugArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	run, err := api.Workspace(args.Workspace).Registry().Retention().Preview(ctx, nil)
	if err != nil {
		return apiErr("preview registry retention", err), nil, nil
	}
	return jsonText(map[string]any{
		"tags_to_delete":       run.TagsDeleted,
		"images_to_delete":     run.ArtifactsDeleted,
		"bytes_freed_estimate": run.BytesFreedEstimate,
		"items":                run.Items,
		"note": "Nothing was deleted. Items marked skipped_in_use are images a service is deployed from; " +
			"those are never deleted regardless of the rules.",
	}), nil, nil
}

func handleSetRegistryRetentionEnabled(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args retentionToggleArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	retention := api.Workspace(args.Workspace).Registry().Retention()

	// Read-modify-write, so flipping the switch cannot disturb the rules.
	policy, err := retention.Get(ctx)
	if err != nil {
		return apiErr("get registry retention policy", err), nil, nil
	}
	if len(policy.Rules) == 0 && args.Enabled {
		return text("this workspace has no retention rules yet; add them in the dashboard or with " +
			"\"simplifyd registry retention set\" before enabling"), nil, nil
	}
	policy.Enabled = args.Enabled

	updated, err := retention.Set(ctx, *policy)
	if err != nil {
		return apiErr("update registry retention policy", err), nil, nil
	}
	return jsonText(updated), nil, nil
}

func handleListRegistryRetentionRuns(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args workspaceSlugArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	runs, err := api.Workspace(args.Workspace).Registry().Retention().Runs(ctx)
	if err != nil {
		return apiErr("list registry retention runs", err), nil, nil
	}
	return jsonText(runs), nil, nil
}

// RegisterRegistryRetentionTools registers the registry retention tools on s.
func RegisterRegistryRetentionTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name: "get-registry-retention",
		Description: "Get the container registry image retention policy for a workspace: whether it is enabled and the rules that decide which image tags survive. " +
			"Rules are unioned — a tag is kept if any rule keeps it — and an image a service is deployed from is never deleted.",
	}, handleGetRegistryRetention)

	addTool(s, &mcp.Tool{
		Name: "preview-registry-retention",
		Description: "Show exactly which image tags the workspace's retention policy would delete on its next run. Deletes nothing. " +
			"Use this before enabling retention, and to explain to a user why an image was or was not removed.",
	}, handlePreviewRegistryRetention)

	addTool(s, &mcp.Tool{
		Name: "set-registry-retention-enabled",
		Description: "Turn the workspace's stored retention policy on or off, leaving its rules unchanged. " +
			"Enabling it means images the rules do not keep are deleted daily and cannot be recovered, so preview it and confirm with the user first. " +
			"Rules themselves are edited in the dashboard or with the CLI.",
	}, handleSetRegistryRetentionEnabled)

	addTool(s, &mcp.Tool{
		Name:        "list-registry-retention-runs",
		Description: "List past registry retention runs for a workspace, newest first, with how many tags each deleted. Use it to answer \"where did my image go\".",
	}, handleListRegistryRetentionRuns)
}
