package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// newFakeRegistryCredentialAPI serves one ghcr.io credential for workspace ws
// and records the requests that change anything.
func newFakeRegistryCredentialAPI(t *testing.T) *[]string {
	t.Helper()
	var writes []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		const base = "/v1/workspaces/ws/registry-credentials"
		switch {
		case r.Method == http.MethodGet && r.URL.Path == base:
			_, _ = w.Write([]byte(`[{"slug":"cred-1","registry_host":"ghcr.io","username":"octocat"}]`))
		case r.Method == http.MethodPatch && r.URL.Path == base+"/cred-1",
			r.Method == http.MethodDelete && r.URL.Path == base+"/cred-1":
			writes = append(writes, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"slug":"cred-1","registry_host":"ghcr.io","username":"octocat"}`))
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/v1/workspaces/ws/projects/p/envs/dev/svcs/"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			raw, _ := json.Marshal(body)
			writes = append(writes, "PATCH service "+string(raw))
			_, _ = w.Write([]byte(`{"slug":"api","docker_image_svc":{"image":"ghcr.io/acme/api","registry_host":"ghcr.io","registry_credential_source":"service","registry_password":"never-returned"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	t.Setenv("SIMPLIFYD_API_URL", ts.URL)
	return &writes
}

func TestMatchRegistryCredential(t *testing.T) {
	list := []cloud.WorkspaceRegistryCredential{{Slug: "cred-1", RegistryHost: "ghcr.io"}, {Slug: "cred-2", RegistryHost: "docker.io"}}
	for ref, want := range map[string]string{
		"cred-1": "cred-1", "ghcr.io": "cred-1", "https://GHCR.io/acme": "cred-1",
		"index.docker.io": "cred-2", "quay.io": "",
	} {
		got := ""
		if c := matchRegistryCredential(list, ref); c != nil {
			got = c.Slug
		}
		if got != want {
			t.Errorf("matchRegistryCredential(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestUpdateRegistryCredentialByHost(t *testing.T) {
	writes := newFakeRegistryCredentialAPI(t)
	res, _, _ := handleUpdateRegistryCredential(context.Background(), httpRequest("Bearer tok"),
		updateRegistryCredentialArgs{Workspace: "ws", Registry: "ghcr.io", Password: "ghp_rotated"})
	if res.IsError {
		t.Fatalf("update failed: %s", resultText(t, res))
	}
	if len(*writes) != 1 || (*writes)[0] != "PATCH /v1/workspaces/ws/registry-credentials/cred-1" {
		t.Fatalf("unexpected writes: %v", *writes)
	}
}

func TestUpdateRegistryCredentialNeedsAChange(t *testing.T) {
	writes := newFakeRegistryCredentialAPI(t)
	res, _, _ := handleUpdateRegistryCredential(context.Background(), httpRequest("Bearer tok"),
		updateRegistryCredentialArgs{Workspace: "ws", Registry: "ghcr.io"})
	if !res.IsError || len(*writes) != 0 {
		t.Fatalf("want an error and no request, got %s and %v", resultText(t, res), *writes)
	}
}

func TestRemoveRegistryCredentialUnknownRegistry(t *testing.T) {
	writes := newFakeRegistryCredentialAPI(t)
	res, _, _ := handleRemoveRegistryCredential(context.Background(), httpRequest("Bearer tok"),
		removeRegistryCredentialArgs{Workspace: "ws", Registry: "quay.io"})
	if !res.IsError || !strings.Contains(resultText(t, res), "quay.io") || len(*writes) != 0 {
		t.Fatalf("want an error naming quay.io and no request, got %s and %v", resultText(t, res), *writes)
	}
}

func TestUpdateServiceRegistryCredentials(t *testing.T) {
	writes := newFakeRegistryCredentialAPI(t)
	base := updateServiceArgs{Workspace: "ws", Project: "p", Env: "dev", Service: "api"}

	missing := base
	missing.Action, missing.RegistryUsername = "registry_credentials", "octocat"
	res, _, _ := handleUpdateService(context.Background(), httpRequest("Bearer tok"), missing)
	if !strings.Contains(resultText(t, res), "registry_password") || len(*writes) != 0 {
		t.Fatalf("want a missing-password message and no request, got %s and %v", resultText(t, res), *writes)
	}

	set := base
	set.Action, set.RegistryUsername, set.RegistryPassword = "registry_credentials", "octocat", "ghp_token"
	res, _, _ = handleUpdateService(context.Background(), httpRequest("Bearer tok"), set)
	if out := resultText(t, res); res.IsError || strings.Contains(out, "never-returned") || !strings.Contains(out, `"registry_credential_source": "service"`) {
		t.Fatalf("unexpected set result: %s", out)
	}

	clear := base
	clear.Action = "clear_registry_credentials"
	if res, _, _ = handleUpdateService(context.Background(), httpRequest("Bearer tok"), clear); res.IsError {
		t.Fatalf("clear failed: %s", resultText(t, res))
	}

	want := []string{
		`PATCH service {"action":"registry_credentials","registry_password":"ghp_token","registry_username":"octocat"}`,
		`PATCH service {"action":"registry_credentials"}`,
	}
	if strings.Join(*writes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes = %v, want %v", *writes, want)
	}
}

// Registration infers each tool's input schema, and panics on one it cannot.
func TestRegistryCredentialToolsRegister(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	RegisterRegistryCredentialTools(s)
}
