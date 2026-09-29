package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newFakeServiceAPI serves a single service carrying two config mounts.
func newFakeServiceAPI(t *testing.T) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"slug":"gateway","configs":[
			{"slug":"cfg-1","name":"kong.yml","content":"url: http://functions:9000","mount_path":"/etc/kong/kong.yml"},
			{"slug":"cfg-2","name":"other","content":"x","mount_path":"/etc/other"}
		]}`))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("SIMPLIFYD_API_URL", ts.URL)
}

func TestListServiceConfigsOmitsContent(t *testing.T) {
	newFakeServiceAPI(t)
	res, _, _ := handleListServiceConfigs(context.Background(), httpRequest("Bearer tok"), svcArgs{Service: "gateway"})
	out := resultText(t, res)
	if strings.Contains(out, "functions:9000") {
		t.Fatalf("list leaked config content: %s", out)
	}
	for _, want := range []string{"cfg-1", "/etc/kong/kong.yml", "has_content"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q: %s", want, out)
		}
	}
}

func TestGetServiceConfigReturnsContent(t *testing.T) {
	newFakeServiceAPI(t)
	for _, key := range []string{"cfg-1", "kong.yml", "/etc/kong/kong.yml"} {
		res, _, _ := handleGetServiceConfig(context.Background(), httpRequest("Bearer tok"), getConfigArgs{Service: "gateway", Config: key})
		out := resultText(t, res)
		if res.IsError || !strings.Contains(out, "url: http://functions:9000") {
			t.Errorf("get %q: want kong.yml content, got %s", key, out)
		}
	}
}

func TestGetServiceConfigUnknown(t *testing.T) {
	newFakeServiceAPI(t)
	res, _, _ := handleGetServiceConfig(context.Background(), httpRequest("Bearer tok"), getConfigArgs{Service: "gateway", Config: "nope"})
	if !res.IsError {
		t.Fatalf("want error for unknown config, got %s", resultText(t, res))
	}
}
