package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// routedAPI starts a stub API serving the given canned responses by path, and
// records every path requested.
func routedAPI(t *testing.T, routes map[string]string) (*cloud.Client, *[]string) {
	t.Helper()
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"denied"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return cloud.NewClient(cloud.WithBaseURL(ts.URL), cloud.WithToken("t")), &seen
}

const (
	wsSlug   = "019b9570-5cb9-7296-9a28-21eed0b5ec42"
	projSlug = "019bfeda-9709-78f4-8c60-324cbbafefd3"
	envSlug  = "019f52f9-c91c-7e5b-8384-19f1b3c79f55"
)

func TestIsSlug(t *testing.T) {
	for _, ref := range []string{wsSlug, strings.ToUpper(wsSlug)} {
		if !isSlug(ref) {
			t.Errorf("isSlug(%q) = false, want true", ref)
		}
	}
	for _, ref := range []string{"", "pluralWorkspace", "pre-dev", "neoEhr",
		"019b9570-5cb9-7296-9a28-21eed0b5ec4", "gggggggg-5cb9-7296-9a28-21eed0b5ec42"} {
		if isSlug(ref) {
			t.Errorf("isSlug(%q) = true, want false", ref)
		}
	}
}

// The bug this whole file exists for: a user says "pluralWorkspace / neoEhr /
// pre-dev", and every one of those has to reach the API as a slug.
func TestResolveArgsTranslatesNamesToSlugs(t *testing.T) {
	api, _ := routedAPI(t, map[string]string{
		"/v1/workspaces": `[{"slug":"` + wsSlug + `","name":"pluralWorkspace"},
			{"slug":"019b9570-e29f-77f5-b94a-e34b0ef3af6d","name":"Plural"}]`,
		"/v1/workspaces/" + wsSlug + "/projects": `[{"slug":"` + projSlug + `","name":"neoEhr"}]`,
		"/v1/workspaces/" + wsSlug + "/projects/" + projSlug + "/envs": `[{"slug":"` + envSlug + `","name":"pre-dev"}]`,
	})

	args := struct {
		Workspace string
		Project   string
		Env       string
		Slug      string
	}{Workspace: "pluralWorkspace", Project: "neoEhr", Env: "pre-dev", Slug: "api"}

	if err := resolveArgs(context.Background(), api, &args); err != nil {
		t.Fatalf("resolveArgs: %v", err)
	}
	if args.Workspace != wsSlug || args.Project != projSlug || args.Env != envSlug {
		t.Errorf("got %s/%s/%s, want %s/%s/%s",
			args.Workspace, args.Project, args.Env, wsSlug, projSlug, envSlug)
	}
	if args.Slug != "api" {
		t.Errorf("unrelated field was rewritten to %q", args.Slug)
	}
}

// Names are matched case-insensitively: the model rarely reproduces the exact
// casing a workspace was created with.
func TestResolveArgsMatchesNameCaseInsensitively(t *testing.T) {
	api, _ := routedAPI(t, map[string]string{
		"/v1/workspaces": `[{"slug":"` + wsSlug + `","name":"pluralWorkspace"}]`,
	})

	args := struct{ Workspace string }{Workspace: "PLURALWORKSPACE"}
	if err := resolveArgs(context.Background(), api, &args); err != nil {
		t.Fatalf("resolveArgs: %v", err)
	}
	if args.Workspace != wsSlug {
		t.Errorf("Workspace = %q, want %q", args.Workspace, wsSlug)
	}
}

// A slug must cost no lookups: it is already what the API wants, and the extra
// round trips would be paid on every tool call.
func TestResolveArgsLeavesSlugsUntouchedWithoutCallingAPI(t *testing.T) {
	api, seen := routedAPI(t, map[string]string{})

	args := struct {
		Workspace string
		Project   string
		Env       string
	}{Workspace: wsSlug, Project: projSlug, Env: envSlug}

	if err := resolveArgs(context.Background(), api, &args); err != nil {
		t.Fatalf("resolveArgs: %v", err)
	}
	if args.Workspace != wsSlug || args.Project != projSlug || args.Env != envSlug {
		t.Error("slugs were rewritten")
	}
	if len(*seen) != 0 {
		t.Errorf("made %d API calls for slug arguments: %v", len(*seen), *seen)
	}
}

// An empty coordinate is left alone: optional fields are common, and resolving
// "" would turn a valid omission into an error.
func TestResolveArgsIgnoresEmptyFields(t *testing.T) {
	api, seen := routedAPI(t, map[string]string{})

	args := struct {
		Workspace string
		Project   string
		Env       string
	}{}
	if err := resolveArgs(context.Background(), api, &args); err != nil {
		t.Fatalf("resolveArgs: %v", err)
	}
	if len(*seen) != 0 {
		t.Errorf("made %d API calls for empty arguments: %v", len(*seen), *seen)
	}
}

// A miss names the alternatives, so the caller can retry without a second
// round of questions to the user.
func TestResolveArgsListsAvailableNamesOnMiss(t *testing.T) {
	api, _ := routedAPI(t, map[string]string{
		"/v1/workspaces": `[{"slug":"` + wsSlug + `","name":"pluralWorkspace"}]`,
	})

	args := struct{ Workspace string }{Workspace: "plurel"}
	err := resolveArgs(context.Background(), api, &args)
	if err == nil {
		t.Fatal("resolveArgs accepted an unknown workspace name")
	}
	if !strings.Contains(err.Error(), "pluralWorkspace") {
		t.Errorf("error does not name the available workspaces: %v", err)
	}
}

// A project token may be refused the workspace listing, but /v1/auth/scope
// tells it the one workspace it is pinned to.
func TestResolveWorkspaceFallsBackToTokenScope(t *testing.T) {
	api, _ := routedAPI(t, map[string]string{
		"/v1/auth/scope": `{"kind":"project_token","workspace":"` + wsSlug +
			`","workspace_name":"pluralWorkspace","project":"` + projSlug + `"}`,
	})

	got, err := resolveWorkspace(context.Background(), api, "pluralWorkspace")
	if err != nil {
		t.Fatalf("resolveWorkspace: %v", err)
	}
	if got != wsSlug {
		t.Errorf("resolveWorkspace = %q, want %q", got, wsSlug)
	}
}

// Asking a project token for a workspace it is not scoped to must say so,
// rather than reporting a lookup failure the caller cannot act on.
func TestResolveWorkspaceReportsScopeMismatch(t *testing.T) {
	api, _ := routedAPI(t, map[string]string{
		"/v1/auth/scope": `{"kind":"project_token","workspace":"` + wsSlug +
			`","workspace_name":"pluralWorkspace","project":"` + projSlug + `"}`,
	})

	_, err := resolveWorkspace(context.Background(), api, "someoneElse")
	if err == nil {
		t.Fatal("resolveWorkspace accepted an out-of-scope workspace")
	}
	if !strings.Contains(err.Error(), "pluralWorkspace") {
		t.Errorf("error does not name the token's workspace: %v", err)
	}
}

// get-me is the first call most clients make. A project token cannot reach
// /v1/auth/me at all, and answering with its scope keeps that from looking
// like a broken server.
func TestGetMeFallsBackToTokenScopeForProjectTokens(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/scope" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"kind":"project_token","workspace":"` + wsSlug +
				`","workspace_name":"pluralWorkspace","project":"` + projSlug + `"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"route is outside project token capabilities"}`))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("SIMPLIFYD_API_URL", ts.URL)

	result, _, err := handleGetMe(context.Background(), httpRequest("Bearer sk_proj_x"), struct{}{})
	if err != nil {
		t.Fatalf("handleGetMe: %v", err)
	}
	if result.IsError {
		t.Fatalf("get-me failed for a project token: %s", resultText(t, result))
	}
	if body := resultText(t, result); !strings.Contains(body, "project_token") {
		t.Errorf("result does not describe the token scope: %s", body)
	}
}
