package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simplifyd-systems/cloud-mcp/tools"
)

func TestOpenAIEndpoint(t *testing.T) {
	t.Setenv("MCP_RESOURCE_URL", "https://mcp.example.com")
	t.Setenv("MCP_AUTHORIZATION_SERVER", "https://api.example.com")
	resource := openAIResource()
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	tools.RegisterOpenAITools(mcpServer, resource.challengeValue("invalid_token", "Reconnect."))
	mux := newMux(stubMCP, streamable(mcpServer))

	metadata := httptest.NewRecorder()
	mux.ServeHTTP(metadata, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))
	var document map[string]any
	if err := json.Unmarshal(metadata.Body.Bytes(), &document); err != nil || document["resource"] != "https://mcp.example.com/" {
		t.Fatalf("OpenAI resource metadata: %d %s", metadata.Code, metadata.Body.String())
	}

	for _, tc := range []struct{ path, audience, challenge string }{
		{"/", generalResource().url, resource.metadataURL},
		{"/openai", generalResource().url, legacyListingResource().metadataURL},
		{"/mcp", resource.url, generalResource().metadataURL},
		{"/mcp", strings.TrimSuffix(resource.url, "/"), generalResource().metadataURL},
		{"/mcp", legacyListingResource().url, generalResource().metadataURL},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+testOAuthToken(t, tc.audience))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong token at %s: status %d", tc.path, rec.Code)
		}
		if !strings.Contains(rec.Header().Get("WWW-Authenticate"), `resource_metadata="`+tc.challenge+`"`) {
			t.Fatalf("OpenAI challenge: %s", rec.Header().Get("WWW-Authenticate"))
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+testOAuthToken(t, generalResource().url))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "mcp" {
		t.Fatalf("general OAuth token did not reach the general handler: %d %s", rec.Code, rec.Body.String())
	}

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "review", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: resource.url,
		// The rejected app already has an OAuth grant for the slashless audience.
		HTTPClient: &http.Client{Transport: handlerTransport{mux, testOAuthToken(t, strings.TrimSuffix(resource.url, "/"))}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	want := strings.Fields("check-dns-zone get-deployment get-domain get-service get-service-metrics list-deployments list-dns-zones list-domains list-environments list-postgres-extensions list-projects list-services list-workspaces")
	var got []string
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, tool.Name)
		data, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Annotations map[string]any `json:"annotations"`
			InputSchema struct {
				Properties           map[string]any `json:"properties"`
				AdditionalProperties any            `json:"additionalProperties"`
			} `json:"inputSchema"`
		}
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatal(err)
		}
		if wire.InputSchema.AdditionalProperties != false {
			t.Errorf("%s permits undeclared arguments", tool.Name)
		}
		for key, expected := range map[string]bool{
			"readOnlyHint":    tool.Name != "check-dns-zone",
			"destructiveHint": tool.Name == "check-dns-zone",
			"openWorldHint":   tool.Name == "check-dns-zone" || tool.Name == "get-domain",
		} {
			if value := wire.Annotations[key]; value != expected {
				t.Errorf("%s %s = %v, want %v", tool.Name, key, value, expected)
			}
		}
		for key := range wire.InputSchema.Properties {
			switch key {
			case "workspace", "project", "env", "service", "deployment", "range", "include_series", "database", "domain", "zone", "confirm":
			default:
				t.Errorf("unexpected listing argument %q on %s", key, tool.Name)
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listing tools = %v, want %v", got, want)
	}
	for _, excluded := range []string{"fund-workspace", "quote-domain", "update-domain", "create-service", "add-registry-credential", "create-token", "get-deployment-logs"} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: excluded})
		if err == nil && (result == nil || !result.IsError) {
			t.Errorf("excluded tool %q was callable", excluded)
		}
	}

	const workspace = "00000000-0000-0000-0000-000000000001"
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "check-dns-zone", Arguments: map[string]any{
		"workspace": workspace, "zone": "example.com", "confirm": false,
	}})
	if err != nil || result == nil || !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "Confirm") {
		t.Fatalf("unconfirmed delegation check: %v, %v", result, err)
	}
}

func TestOpenAIManifestEndpoint(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("openai-plugin", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Servers map[string]struct {
			URL string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	// Preserve the literal value from the first submission. The portal can
	// render the origin with a slash even though the manifest used none.
	if got, want := manifest.Servers["simplifyd"].URL, "https://mcp.simplifyd.com"; got != want {
		t.Errorf("listing endpoint = %q, want %q", got, want)
	}
}

func testOAuthToken(t *testing.T, audience string) string {
	t.Helper()
	claims, err := json.Marshal(oauthClaims{Use: "oauth_access", Audience: audience, Issuer: authorizationServer()})
	if err != nil {
		t.Fatal(err)
	}
	return "test." + base64.RawURLEncoding.EncodeToString(claims) + ".test"
}

type handlerTransport struct {
	handler http.Handler
	token   string
}

func (h handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+h.token)
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}
