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
