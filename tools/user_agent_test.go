package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The SDK's default User-Agent names the CLI, so an MCP server that does not
// override it is recorded as a command line in the user's session list. This
// asserts on what reaches the wire rather than on the client, because the
// override lives in newSDKClient and every transport has to go through it.
func TestSDKClientIdentifiesItselfAsTheMCPServer(t *testing.T) {
	seen := make(chan string, 8)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("SIMPLIFYD_API_URL", ts.URL)

	SetVersion("9.9.9")
	t.Cleanup(func() { SetVersion("dev") })

	api, _, ok := sdkFor(httpRequest("Bearer caller-token"))
	if !ok {
		t.Fatal("sdkFor rejected a request carrying a bearer token")
	}
	if _, err := api.Me(context.Background()); err != nil {
		t.Fatalf("stub API call failed: %v", err)
	}

	if got := <-seen; got != "cloud-mcp/9.9.9" {
		t.Fatalf("User-Agent = %q, want %q", got, "cloud-mcp/9.9.9")
	}
}
