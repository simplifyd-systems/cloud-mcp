package tools

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("got %d content items, want 1", len(result.Content))
	}
	content, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content has type %T, want *mcp.TextContent", result.Content[0])
	}
	return content.Text
}

func TestJSONTextRedactsSensitiveFieldsRecursively(t *testing.T) {
	input := map[string]any{
		"name": "database",
		"variables": []any{
			map[string]any{"name": "TOKEN", "value": "top-secret"},
		},
		"connection": map[string]any{
			"password":       "database-secret",
			"connection_url": "postgres://user:secret@internal/db",
		},
		"key":     "unexpected-token-key",
		"configs": []any{map[string]any{"content": "private config"}},
	}

	output := resultText(t, jsonText(input))
	for _, secret := range []string{"top-secret", "database-secret", "postgres://", "private config", "unexpected-token-key"} {
		if strings.Contains(output, secret) {
			t.Errorf("output contains secret %q: %s", secret, output)
		}
	}
	for _, indicator := range []string{"has_value", "has_password", "has_connection_url", "has_content"} {
		if !strings.Contains(output, indicator) {
			t.Errorf("output does not contain %q: %s", indicator, output)
		}
	}
}

func TestJSONTextRawPreservesExplicitCredential(t *testing.T) {
	output := resultText(t, jsonTextRaw(map[string]string{"key": "one-time-secret"}))
	if !strings.Contains(output, "one-time-secret") {
		t.Fatalf("explicit credential was removed: %s", output)
	}
}

func TestAPIErrDoesNotExposeBackendMessage(t *testing.T) {
	err := &cloud.APIError{
		StatusCode: http.StatusInternalServerError,
		Message:    "panic at /srv/internal/billing.go Authorization: Bearer secret-token",
	}
	output := resultText(t, apiErr("fund workspace", err))
	if strings.Contains(output, "billing.go") || strings.Contains(output, "secret-token") {
		t.Fatalf("backend details leaked: %s", output)
	}
	if !strings.Contains(output, "reference") {
		t.Fatalf("error lacks a support reference: %s", output)
	}
}

func TestAPIErrDoesNotExposeTransportError(t *testing.T) {
	output := resultText(t, apiErr("list services", errors.New("dial tcp internal-api.service.local: connection refused")))
	if strings.Contains(output, "internal-api") {
		t.Fatalf("transport details leaked: %s", output)
	}
}

// ChatGPT offers to reconnect only when a failed tool result carries the
// challenge in _meta; without it an expired token is a dead end.
func TestAPIErrAsksToReauthenticateOnUnauthorized(t *testing.T) {
	SetAuthChallenge(`Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`)
	t.Cleanup(func() { SetAuthChallenge("") })

	result := apiErr("list services", &cloud.APIError{StatusCode: http.StatusUnauthorized})
	challenges, _ := result.Meta["mcp/www_authenticate"].([]string)
	if !result.IsError || len(challenges) != 1 || !strings.Contains(challenges[0], "resource_metadata=") {
		t.Fatalf("401 result meta = %v, want the auth challenge", result.Meta)
	}

	other := apiErr("list services", &cloud.APIError{StatusCode: http.StatusForbidden})
	if other.Meta != nil {
		t.Fatalf("403 result carries meta %v; only an authentication failure should ask to reconnect", other.Meta)
	}
}
