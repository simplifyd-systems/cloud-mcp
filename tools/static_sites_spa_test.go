package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

const svcsBase = "/v1/workspaces/ws/projects/p/envs/prod/svcs"

// fakeSites serves a service list holding the given sites and records the
// bodies of creates and document updates.
func fakeSites(t *testing.T, existing ...cloud.Service) (*cloud.ServicesClient, *[]map[string]any, *[]map[string]any) {
	t.Helper()
	var creates, updates []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == svcsBase:
			_ = json.NewEncoder(w).Encode(existing)
		case r.Method == http.MethodPost && r.URL.Path == svcsBase:
			creates = append(creates, body)
			_ = json.NewEncoder(w).Encode(cloud.Service{Slug: "new-site"})
		case r.Method == http.MethodPut && r.URL.Path == svcsBase+"/web/site/documents":
			updates = append(updates, body)
			_ = json.NewEncoder(w).Encode(cloud.StaticSite{})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	svcs := cloud.NewClient(cloud.WithBaseURL(server.URL), cloud.WithToken("test")).
		Workspace("ws").Project("p").Env("prod").Services()
	return svcs, &creates, &updates
}

func TestResolveStaticSiteCreatesWithSPAFallback(t *testing.T) {
	svcs, creates, _ := fakeSites(t)
	on := true
	slug, created, failure := resolveStaticSite(context.Background(), svcs, "web", siteDocuments{SPAFallback: &on}, true)
	if failure != nil || !created || slug != "new-site" {
		t.Fatalf("slug=%q created=%v failure=%v", slug, created, failure)
	}
	site, _ := (*creates)[0]["static_site_svc"].(map[string]any)
	if site["spa_fallback"] != true {
		t.Errorf("create sent %v, want spa_fallback true", (*creates)[0])
	}
}

func TestResolveStaticSiteUpdatesExistingOnlyWhenAsked(t *testing.T) {
	existing := cloud.Service{Slug: "web", Name: "web", Type: cloud.ServiceTypeStaticSite}

	// Nothing given: an existing site is left exactly as it is.
	svcs, _, updates := fakeSites(t, existing)
	if _, _, failure := resolveStaticSite(context.Background(), svcs, "web", siteDocuments{}, true); failure != nil {
		t.Fatal(failure)
	}
	if len(*updates) != 0 {
		t.Fatalf("no settings given, but sent %v", *updates)
	}

	// Turning it off has to reach the API as an explicit false.
	svcs, _, updates = fakeSites(t, existing)
	off := false
	if _, _, failure := resolveStaticSite(context.Background(), svcs, "web", siteDocuments{SPAFallback: &off}, true); failure != nil {
		t.Fatal(failure)
	}
	if len(*updates) != 1 {
		t.Fatalf("want one documents update, got %v", *updates)
	}
	if v, ok := (*updates)[0]["spa_fallback"]; !ok || v != false {
		t.Errorf("turning off sent %v", (*updates)[0])
	}
}
