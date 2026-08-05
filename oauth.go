package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

// This file makes the hosted server an OAuth 2.1 protected resource, which is
// what turns "paste an API token into your config" into a Connect button.
//
// The flow the client runs, and what each piece here does:
//
//  1. Client calls /mcp with no credentials  →  requireAuth answers 401 with a
//     WWW-Authenticate header naming our metadata document. Without this header
//     the client has no way to discover that OAuth is even an option, and shows
//     a bare authentication error instead.
//  2. Client fetches /.well-known/oauth-protected-resource  →  protectedResource
//     tells it which authorization server to use.
//  3. Client registers, sends the user to consent, and exchanges a code with
//     cloudapi. None of that touches this server.
//  4. Client retries /mcp with an access token, which sdkFor picks up per
//     request exactly like a pasted token.

// resourceURL is this server's canonical identifier, the "resource" a client
// requests a token for (RFC 8707). It must match what the authorization server
// is configured to accept, and what the client sends, or the token is refused.
func resourceURL() string {
	if url := os.Getenv("MCP_RESOURCE_URL"); url != "" {
		return strings.TrimSuffix(url, "/")
	}
	return "https://mcp.simplifyd.com"
}

// authorizationServer is the issuer that mints tokens for this resource.
func authorizationServer() string {
	if url := os.Getenv("MCP_AUTHORIZATION_SERVER"); url != "" {
		return strings.TrimSuffix(url, "/")
	}
	return "https://api.cloud.simplifyd.com"
}

// registerOAuthMetadata mounts the protected-resource document (RFC 9728).
//
// The path is fixed by the spec: a client derives it from the origin of the
// URL it was configured with, so it cannot be served anywhere else.
func registerOAuthMetadata(mux *http.ServeMux) {
	mux.HandleFunc("/.well-known/oauth-protected-resource", protectedResource)
	// Clients that were given the full endpoint URL rather than the origin look
	// for the document under that path instead.
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", protectedResource)
}

func protectedResource(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource":                 resourceURL(),
		"authorization_servers":    []string{authorizationServer()},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         []string{"identity", "workspace", "offline_access"},
		"resource_documentation":   "https://docs.cloud.simplifyd.com",
	})
}

// requireAuth rejects unauthenticated requests at the HTTP layer, with the
// challenge that starts the OAuth flow.
//
// This has to happen here rather than inside a tool handler: a tool result
// saying "not authenticated" is a successful HTTP response, and the client has
// no reason to look for a way to authenticate. Only a 401 carrying
// WWW-Authenticate does that.
func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bearerFromHeader(r.Header.Get("Authorization")) == "" {
			// A person who typed the URL into a browser gets the page that
			// explains what this is; a client gets the challenge it needs.
			if isBrowserNavigation(r) {
				http.Redirect(w, r, landingURL(), http.StatusFound)
				return
			}
			challenge(w, "", "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// landingURL is the human-facing page describing this server.
func landingURL() string {
	if url := os.Getenv("MCP_LANDING_URL"); url != "" {
		return url
	}
	return "https://simplifyd.com/mcp"
}

// isBrowserNavigation reports whether a request is a person navigating to this
// URL rather than an MCP client talking to it.
//
// The distinction matters because the two need opposite responses. An MCP
// client that gets a redirect learns nothing and cannot connect: discovery
// depends entirely on the 401 and its WWW-Authenticate header. A person who
// gets that 401 sees a line of raw JSON.
//
// Only a GET/HEAD that explicitly asks for HTML is treated as a person. Every
// MCP request is a POST, or sends an Accept of application/json and/or
// text/event-stream, so no client is ever redirected.
func isBrowserNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	accept := r.Header.Get("Accept")
	if !strings.Contains(accept, "text/html") {
		return false
	}
	// A client asking for SSE is opening a stream, not browsing, even on a GET
	// with a permissive Accept.
	return !strings.Contains(accept, "text/event-stream")
}

// challenge writes a 401 pointing the client at our metadata document.
func challenge(w http.ResponseWriter, errCode, description string) {
	params := fmt.Sprintf(
		`Bearer resource_metadata="%s/.well-known/oauth-protected-resource"`,
		resourceURL(),
	)
	if errCode != "" {
		params += fmt.Sprintf(`, error="%s"`, errCode)
	}
	if description != "" {
		params += fmt.Sprintf(`, error_description="%s"`, description)
	}
	w.Header().Set("WWW-Authenticate", params)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             "unauthorized",
		"error_description": description,
	})
	log.Printf("unauthenticated request rejected: %s", description)
}

// bearerFromHeader mirrors tools.bearerToken; duplicated rather than exported
// because the tools package must stay usable without the HTTP transport.
func bearerFromHeader(header string) string {
	const prefix = "bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}
