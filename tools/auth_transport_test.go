package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// httpRequest builds a CallToolRequest as the streamable HTTP transport would:
// with Extra populated from the inbound request headers.
func httpRequest(authorization string) *mcp.CallToolRequest {
	header := http.Header{}
	if authorization != "" {
		header.Set("Authorization", authorization)
	}
	return &mcp.CallToolRequest{Extra: &mcp.RequestExtra{Header: header}}
}

// stdioRequest builds a CallToolRequest as the stdio transport would: Extra is
// nil, because there is no HTTP layer underneath.
func stdioRequest() *mcp.CallToolRequest {
	return &mcp.CallToolRequest{}
}

// newFakeAPI starts a stub Simplifyd API and points the SDK at it, returning a
// channel of the Authorization header values it observes. Asserting on what
// reaches the wire is stronger than inspecting the client: it proves the
// caller's token is the one actually used for the API call.
func newFakeAPI(t *testing.T) chan string {
	t.Helper()
	seen := make(chan string, 64)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	// Read by client.BaseURL() when sdkFor constructs a client, so this must
	// be set before any sdkFor call in the test.
	t.Setenv("SIMPLIFYD_API_URL", ts.URL)
	return seen
}

// tokenOf makes a real call with api and reports the token it presented.
func tokenOf(t *testing.T, api *cloud.Client, seen chan string) string {
	t.Helper()
	if _, err := api.Me(context.Background()); err != nil {
		t.Fatalf("stub API call failed: %v", err)
	}
	return strings.TrimPrefix(<-seen, "Bearer ")
}

func TestSDKForUsesPerRequestTokenOverHTTP(t *testing.T) {
	seen := newFakeAPI(t)
	// The process-wide identity must never leak into an HTTP call.
	setToken("process-wide-token")
	t.Cleanup(func() { setToken("") })

	api, _, ok := sdkFor(httpRequest("Bearer caller-token"))
	if !ok {
		t.Fatal("sdkFor rejected a request carrying a bearer token")
	}
	if got := tokenOf(t, api, seen); got != "caller-token" {
		t.Fatalf("client uses token %q, want the caller's own token", got)
	}
}

func TestSDKForFallsBackToProcessTokenOverStdio(t *testing.T) {
	seen := newFakeAPI(t)
	setToken("process-wide-token")
	t.Cleanup(func() { setToken("") })

	api, _, ok := sdkFor(stdioRequest())
	if !ok {
		t.Fatal("sdkFor rejected a stdio request with a configured token")
	}
	if got := tokenOf(t, api, seen); got != "process-wide-token" {
		t.Fatalf("stdio client uses token %q, want the process-wide token", got)
	}
}

func TestSDKForRejectsHTTPRequestWithoutAuthorization(t *testing.T) {
	// Even with a process-wide token set, an unauthenticated HTTP caller must
	// not inherit it.
	setToken("process-wide-token")
	t.Cleanup(func() { setToken("") })

	_, result, ok := sdkFor(httpRequest(""))
	if ok {
		t.Fatal("sdkFor accepted an HTTP request with no Authorization header")
	}
	if !result.IsError {
		t.Fatal("result is not marked as an error")
	}
	if msg := resultText(t, result); !strings.Contains(msg, "Authorization") {
		t.Fatalf("error message %q does not tell the caller how to authenticate", msg)
	}
}

func TestConcurrentHTTPCallersDoNotShareCredentials(t *testing.T) {
	seen := newFakeAPI(t)
	setToken("")
	t.Cleanup(func() { setToken("") })

	const callers = 32
	var wg sync.WaitGroup
	errs := make(chan string, callers)

	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := fmt.Sprintf("token-%d", i)
			api, _, ok := sdkFor(httpRequest("Bearer " + want))
			if !ok {
				errs <- "caller was rejected"
				return
			}
			if _, err := api.Me(context.Background()); err != nil {
				errs <- "stub API call failed: " + err.Error()
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	close(seen)

	for msg := range errs {
		t.Error(msg)
	}

	// Every caller's own token must have reached the API exactly once; a
	// shared client would show one token repeated.
	got := map[string]int{}
	for auth := range seen {
		got[strings.TrimPrefix(auth, "Bearer ")]++
	}
	if len(got) != callers {
		t.Fatalf("stub API saw %d distinct tokens, want %d — callers shared a client", len(got), callers)
	}
	for i := range callers {
		if n := got[fmt.Sprintf("token-%d", i)]; n != 1 {
			t.Errorf("token-%d presented %d times, want 1", i, n)
		}
	}
}

func TestBearerToken(t *testing.T) {
	cases := map[string]string{
		"Bearer abc":      "abc",
		"bearer abc":      "abc",
		"BEARER abc":      "abc",
		"Bearer   abc  ":  "abc",
		"abc":             "",
		"":                "",
		"Basic abc":       "",
		"Bearer":          "",
		"Bearer ":         "",
		"Bearer a b":      "a b",
		"Token abc":       "",
		"Bearer\tabc":     "",
		"Bearer  \t abc ": "abc",
	}
	for header, want := range cases {
		if got := bearerToken(header); got != want {
			t.Errorf("bearerToken(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestLoginToolIsNotRegisteredOverHTTP(t *testing.T) {
	for _, tc := range []struct {
		name      string
		local     bool
		wantLogin bool
	}{
		{"stdio", true, true},
		{"http", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
			RegisterAuthTools(server, tc.local)

			if got := hasTool(t, server, "login"); got != tc.wantLogin {
				t.Fatalf("login tool registered = %v, want %v", got, tc.wantLogin)
			}
			if !hasTool(t, server, "get-me") {
				t.Fatal("get-me must be registered on both transports")
			}
		})
	}
}

func TestLoginToolRefusesHTTPCall(t *testing.T) {
	// Defence in depth: even if login were somehow reachable over HTTP, it
	// must not mutate process-wide state on a caller's behalf.
	result, _, err := handleLogin(context.Background(), httpRequest("Bearer x"),
		loginArgs{Email: "a@b.c", Password: "hunter2"})
	if err != nil {
		t.Fatalf("handleLogin returned error: %v", err)
	}
	if !result.IsError {
		t.Fatal("login over HTTP was not refused")
	}
}

func hasTool(t *testing.T, server *mcp.Server, name string) bool {
	t.Helper()
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	ct, st := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer serverSession.Close()
	clientSession, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer clientSession.Close()

	for tool, err := range clientSession.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("list tools: %v", err)
		}
		if tool.Name == name {
			return true
		}
	}
	return false
}

// TestHTTPServerServesToolsPerCaller exercises the real streamable HTTP
// handler end to end, confirming the transport populates Extra.Header so that
// sdkFor sees each caller's own Authorization value.
func TestHTTPServerServesToolsPerCaller(t *testing.T) {
	seen := make(chan string, 4)
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "whoami", Description: "echo the caller token"},
		func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			if req.Extra == nil {
				seen <- "<no extra>"
				return text("no extra"), nil, nil
			}
			seen <- bearerToken(req.Extra.Header.Get("Authorization"))
			return text("ok"), nil, nil
		})

	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	for _, token := range []string{"alice-token", "bob-token"} {
		callWhoami(t, ts.URL, token)
		if got := <-seen; got != token {
			t.Fatalf("handler saw token %q, want %q", got, token)
		}
	}
}

func callWhoami(t *testing.T, url, token string) {
	t.Helper()
	ctx := context.Background()
	transport := &mcp.StreamableClientTransport{
		Endpoint: url,
		HTTPClient: &http.Client{Transport: authRoundTripper{
			token: token,
			base:  http.DefaultTransport,
		}},
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"}); err != nil {
		t.Fatalf("call tool: %v", err)
	}
}

type authRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (a authRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+a.token)
	return a.base.RoundTrip(r)
}
