// Command cloud-mcp is an MCP server for the Simplifyd Cloud platform.
//
// It exposes tools for managing workspaces, projects, environments, services,
// deployments, and API tokens via the Model Context Protocol, over either
// stdio (the default, for a local client launching this binary as a
// subprocess) or streamable HTTP (for a hosted, multi-user deployment).
//
// Transport:
//
//	MCP_TRANSPORT=stdio  (default) Serve on stdin/stdout.
//	MCP_TRANSPORT=http             Serve streamable HTTP on MCP_ADDR (:8080).
//
// Authentication:
//
//	stdio: set SIMPLIFYD_API_TOKEN to a JWT token, or run the "login" tool to
//	authenticate interactively. Tokens are persisted to ~/.simplifyd/config.json.
//
//	http: every request must carry the caller's token in an
//	Authorization: Bearer <token> header. Nothing is persisted, and no
//	credential is shared between callers. The "login" tool is not registered.
//
// Custom API URL:
//
//	Set SIMPLIFYD_API_URL to override the default https://api.cloud.simplifyd.com.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simplifyd-com/cloud-mcp/tools"
)

const serverName = "simplifyd-cloud-mcp"

// Overridden at build time via -ldflags -X main.serverVersion; keep in step
// with VERSION in the Makefile so a plain `go build` reports the truth.
var serverVersion = "0.0.3"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(serverVersion)
		os.Exit(0)
	}

	local := !strings.EqualFold(os.Getenv("MCP_TRANSPORT"), "http")

	server := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, nil)

	// Register all tool groups. Only the auth group differs between
	// transports: the login tool is local-only.
	tools.RegisterAuthTools(server, local)
	tools.RegisterWorkspaceTools(server)
	tools.RegisterProjectTools(server)
	tools.RegisterEnvironmentTools(server)
	tools.RegisterServiceTools(server)
	tools.RegisterStaticSiteTools(server)
	tools.RegisterDeploymentTools(server)
	tools.RegisterTokenTools(server)

	if local {
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatalf("server error: %v", err)
		}
		return
	}

	if err := serveHTTP(server); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// serveHTTP runs the server over streamable HTTP.
//
// Sessions are stateless: every request is self-describing and carries its own
// credentials, so any replica can serve any request and the deployment scales
// horizontally without sticky routing.
func serveHTTP(server *mcp.Server) error {
	addr := os.Getenv("MCP_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := newMux(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: streamable HTTP holds long-lived SSE responses open,
		// and a write deadline would truncate them mid-stream.
	}

	log.Printf("listening on %s (streamable http)", addr)
	return srv.ListenAndServe()
}

// newMux wires the HTTP routes. Split out from serveHTTP so the routing can be
// tested: the MCP endpoint is mounted at "/", which is a ServeMux catch-all,
// and it must not swallow the health probe or the OAuth metadata documents.
func newMux(mcpHandler http.Handler) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	registerOAuthMetadata(mux)

	// Served at the root as well as /mcp, so a client can be configured with
	// the bare origin (https://mcp.simplifyd.com). /mcp stays mounted for
	// clients already configured with it.
	//
	// The patterns registered above are more specific than "/" and still win,
	// so the unauthenticated metadata fetch a client makes after being
	// challenged here keeps working.
	endpoint := checkOrigin(requireAuth(mcpHandler))
	mux.Handle("/", endpoint)
	mux.Handle("/mcp", endpoint)
	mux.Handle("/mcp/", endpoint)

	return mux
}

// checkOrigin rejects cross-origin browser requests, which would otherwise let
// a malicious page in a user's browser drive this server via DNS rebinding.
// Non-browser clients send no Origin header and are unaffected.
//
// MCP_ALLOWED_ORIGINS is a comma-separated allowlist; when unset, any request
// carrying an Origin header is refused.
func checkOrigin(next http.Handler) http.Handler {
	var allowed []string
	if raw := os.Getenv("MCP_ALLOWED_ORIGINS"); raw != "" {
		for _, o := range strings.Split(raw, ",") {
			if o = strings.TrimSpace(o); o != "" {
				allowed = append(allowed, o)
			}
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			ok := false
			for _, a := range allowed {
				if strings.EqualFold(a, origin) {
					ok = true
					break
				}
			}
			if !ok {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers",
				"Authorization, Content-Type, Mcp-Session-Id, Mcp-Protocol-Version")
			w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
