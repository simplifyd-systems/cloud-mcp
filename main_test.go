package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubMCP stands in for the streamable handler: routing is what is under test,
// not the MCP protocol itself.
var stubMCP = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("mcp"))
})

// TestMuxRoutingUnauthenticated pins the boundary between the "/" catch-all and
// the endpoints that must stay reachable without credentials. If the catch-all
// ever swallows them, a client can never discover how to authenticate and the
// health probe fails the pod.
func TestMuxRoutingUnauthenticated(t *testing.T) {
	mux := newMux(stubMCP)

	for _, tc := range []struct {
		path       string
		method     string
		wantStatus int
		reason     string
	}{
		{"/healthz", http.MethodGet, http.StatusOK, "kubelet probes this with no credentials"},
		{"/.well-known/oauth-protected-resource", http.MethodGet, http.StatusOK, "clients fetch this after a 401"},
		{"/.well-known/oauth-protected-resource/mcp", http.MethodGet, http.StatusOK, "path-suffixed variant"},
		{"/", http.MethodPost, http.StatusUnauthorized, "bare origin is the MCP endpoint"},
		{"/mcp", http.MethodPost, http.StatusUnauthorized, "explicit path still mounted"},
		{"/mcp/", http.MethodPost, http.StatusUnauthorized, "trailing slash still mounted"},
		{"/anything-else", http.MethodPost, http.StatusUnauthorized, "catch-all challenges rather than 404s"},
	} {
		t.Run(strings.TrimPrefix(tc.path, "/"), func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.wantStatus {
				t.Fatalf("%s %s = %d, want %d (%s)", tc.method, tc.path, rec.Code, tc.wantStatus, tc.reason)
			}
		})
	}
}

// TestChallengeCarriesResourceMetadata is the header that makes a client offer
// to authenticate instead of reporting a bare error.
func TestChallengeCarriesResourceMetadata(t *testing.T) {
	t.Setenv("MCP_RESOURCE_URL", "https://mcp.example.com")

	rec := httptest.NewRecorder()
	newMux(stubMCP).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	challenge := rec.Header().Get("WWW-Authenticate")
	if !strings.HasPrefix(challenge, "Bearer ") {
		t.Fatalf("WWW-Authenticate = %q, want a Bearer challenge", challenge)
	}
	if !strings.Contains(challenge, `resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`) {
		t.Fatalf("WWW-Authenticate = %q, missing the resource_metadata pointer", challenge)
	}
}

func TestAuthenticatedRequestReachesMCPHandler(t *testing.T) {
	for _, path := range []string{"/", "/mcp"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer a-token")
		newMux(stubMCP).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK || rec.Body.String() != "mcp" {
			t.Fatalf("POST %s = %d %q, want 200 \"mcp\"", path, rec.Code, rec.Body.String())
		}
	}
}

func TestProtectedResourceDocument(t *testing.T) {
	t.Setenv("MCP_RESOURCE_URL", "https://mcp.example.com")
	t.Setenv("MCP_AUTHORIZATION_SERVER", "https://api.example.com")

	rec := httptest.NewRecorder()
	newMux(stubMCP).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))

	var doc struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Resource != "https://mcp.example.com" {
		t.Errorf("resource = %q", doc.Resource)
	}
	if len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != "https://api.example.com" {
		t.Errorf("authorization_servers = %v", doc.AuthorizationServers)
	}
}

// TestBrowserNavigationRedirectsToLandingPage: a person who types the URL in
// gets the page, not raw JSON.
func TestBrowserNavigationRedirectsToLandingPage(t *testing.T) {
	t.Setenv("MCP_LANDING_URL", "https://example.com/mcp")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	newMux(stubMCP).ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "https://example.com/mcp" {
		t.Fatalf("Location = %q", got)
	}
}

// TestMCPClientsAreNeverRedirected is the constraint that makes the redirect
// safe. A client that receives a 302 instead of a 401 has no way to discover
// the authorization server, and can never connect — so every shape of request
// a real client makes must still get the challenge.
func TestMCPClientsAreNeverRedirected(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		accept string
	}{
		{"json-rpc post", http.MethodPost, "application/json, text/event-stream"},
		{"post asking for html", http.MethodPost, "text/html"},
		{"sse stream get", http.MethodGet, "text/event-stream"},
		{"get with mixed accept including sse", http.MethodGet, "text/html, text/event-stream"},
		{"get with no accept", http.MethodGet, ""},
		{"get accepting json", http.MethodGet, "application/json"},
		{"wildcard accept", http.MethodGet, "*/*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, "/", nil)
			if tc.accept != "" {
				req.Header.Set("Accept", tc.accept)
			}
			newMux(stubMCP).ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (a redirect would break discovery)", rec.Code)
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("missing WWW-Authenticate; client cannot discover the auth server")
			}
		})
	}
}

// TestCrossOriginRequestRefused covers the DNS-rebinding guard now that the
// endpoint answers on the bare origin, which is the URL a browser would use.
func TestCrossOriginRequestRefused(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Authorization", "Bearer a-token")
	newMux(stubMCP).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a disallowed origin", rec.Code)
	}
}
