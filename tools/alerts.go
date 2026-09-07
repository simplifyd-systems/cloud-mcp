package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// Operational alerts for a workspace.
//
// MCP has no dependable way to push: an agent learns about a filling volume by
// asking. That is why this is a tool rather than a notification — a tool is
// something an agent can call on its own schedule, or as part of a health check
// before it deploys, and it works on every client. A subscribable resource
// would reach only clients that both support subscriptions and surface them to
// the model, which is a small and unpredictable subset.

// ---- list-alerts ----

type listAlertsArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	// The band an agent cares about is task-dependent: a capacity review wants
	// everything, a pre-deploy check wants only what is nearly full.
	MinBand int `json:"min_band,omitempty" jsonschema:"Only return volumes at or above this threshold band: 50, 75 or 90. Defaults to 50 (every alerting volume)."`
}

func handleListAlerts(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args listAlertsArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}

	alerts, err := api.Workspace(args.Workspace).Alerts(ctx)
	if err != nil {
		return apiErr("list alerts", err), nil, nil
	}

	minBand := args.MinBand
	if minBand <= 0 {
		minBand = 50
	}

	volumes := make([]cloud.VolumeAlert, 0, len(alerts.Volumes))
	for _, v := range alerts.Volumes {
		if int(v.Band) >= minBand {
			volumes = append(volumes, v)
		}
	}

	// An empty list is the answer, not an absence of one: an agent that asked
	// "is anything filling up" needs to be able to tell "no" from "the call did
	// not work", and a bare [] reads as neither.
	if len(volumes) == 0 {
		return text(fmt.Sprintf(
			"No volumes in workspace %q are at or above %d%% utilisation.",
			args.Workspace, minBand,
		)), nil, nil
	}

	return jsonText(volumes), nil, nil
}

func RegisterAlertTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name: "list-alerts",
		Description: "List operational alerts for a workspace. Currently persistent volumes that have reached a utilisation threshold (50%, 75% or 90% full), worst first. " +
			"Each entry gives the service, its project and environment, disk used and provisioned, the percentage, and the threshold band. " +
			"Use this to check whether any database or service is running out of disk before it starts failing writes.",
	}, handleListAlerts)
}
