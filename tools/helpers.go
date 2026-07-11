// Package tools implements MCP tool handlers for the Simplifyd Cloud API.
package tools

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"

	"github.com/simplifyd-com/cloud-mcp/client"
)

var (
	sdkMu     sync.RWMutex
	sdkClient *cloud.Client
	sdkToken  string
)

func init() {
	sdkToken = client.ResolveToken()
	sdkClient = newSDKClient(sdkToken)
}

func newSDKClient(token string) *cloud.Client {
	return cloud.NewClient(
		cloud.WithToken(token),
		cloud.WithBaseURL(client.BaseURL()),
	)
}

// sdk returns the shared SDK client instance used by all tools.
func sdk() *cloud.Client {
	sdkMu.RLock()
	defer sdkMu.RUnlock()
	return sdkClient
}

// setToken swaps the SDK client for one authenticated with the given token
// (SDK clients are immutable once constructed).
func setToken(token string) {
	sdkMu.Lock()
	defer sdkMu.Unlock()
	sdkToken = token
	sdkClient = newSDKClient(token)
}

// isAuthenticated reports whether a token is configured.
func isAuthenticated() bool {
	sdkMu.RLock()
	defer sdkMu.RUnlock()
	return sdkToken != ""
}

// text returns a successful CallToolResult containing a plain-text message.
func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: s}},
	}
}

// toolError returns an error CallToolResult with the given message.
func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

// errResult formats an error and returns it as a tool error result.
func errResult(format string, args ...any) *mcp.CallToolResult {
	return toolError(fmt.Sprintf(format, args...))
}

// jsonText marshals v to indented JSON and returns it as a text result.
func jsonText(v any) *mcp.CallToolResult {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult("failed to encode response: %v", err)
	}
	return text(string(data))
}

// requireAuth checks that a token is configured and returns an error result
// if not. Returns true when authenticated.
func requireAuth() (*mcp.CallToolResult, bool) {
	if !isAuthenticated() {
		return toolError(
			"not authenticated — set SIMPLIFYD_API_TOKEN or run the login tool first",
		), false
	}
	return nil, true
}

// apiErr formats an API error for display.
func apiErr(op string, err error) *mcp.CallToolResult {
	return toolError(fmt.Sprintf("%s: %v", op, err))
}
