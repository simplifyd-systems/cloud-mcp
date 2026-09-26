package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	supabaseSlug = "01a0d8cc-85a3-76fd-ac69-96221c1d1fa9"
	ownSlug      = "01a0d8cc-0000-7000-8000-000000000001"
	draftSlug    = "01a0d8cc-0000-7000-8000-000000000002"
)

func templateRoutes() map[string]string {
	supabase := `{"slug":"` + supabaseSlug + `","name":"Supabase","status":"published","services":[
		{"key":"supabase_db","type":"docker","vcpus":2,"memory":2048,"docker":{"image":"supabase/postgres","tag":"17.6.1.136"},
		 "init_sql_path":"/docker-entrypoint-initdb.d/migrations/zz-init.sql",
		 "persistent_storages":[{"name":"postgres","mount_path":"/var/lib/postgresql","size_gb":20}]},
		{"key":"supabase_gateway","type":"docker","vcpus":1,"memory":256,"ingress":[{"port":8000,"protocol":"HTTP"}]}]}`
	return map[string]string{
		"/v1/templates": `[` + supabase + `]`,
		// The owning workspace sees the listed template among its own too.
		"/v1/workspaces/" + wsSlug + "/templates": `[` + supabase + `,
			{"slug":"` + ownSlug + `","name":"Internal API","status":"published","services":[{"key":"api","type":"docker"}]},
			{"slug":"` + draftSlug + `","name":"Half done","status":"draft","services":[{"key":"x","type":"docker"}]}]`,
		"/v1/workspaces/" + wsSlug + "/templates/" + draftSlug: `{"slug":"` + draftSlug + `","name":"Half done","status":"draft","services":[]}`,
	}
}

func TestAvailableTemplates(t *testing.T) {
	api, _ := routedAPI(t, templateRoutes())
	got, err := availableTemplates(context.Background(), api, wsSlug)
	if err != nil {
		t.Fatal(err)
	}
	// Supabase once, as Simplifyd's; the draft left out.
	if len(got) != 2 || got[0].source != templateSourceSimplifyd || got[1].template.Name != "Internal API" || got[1].source != templateSourceWorkspace {
		t.Fatalf("got %+v", got)
	}

	// Without a workspace, only the catalog is read.
	api, seen := routedAPI(t, templateRoutes())
	if got, err := availableTemplates(context.Background(), api, ""); err != nil || len(got) != 1 {
		t.Fatalf("got %+v, err %v", got, err)
	}
	if len(*seen) != 1 {
		t.Errorf("requests = %v", *seen)
	}
}

func TestFindTemplate(t *testing.T) {
	api, _ := routedAPI(t, templateRoutes())
	ctx := context.Background()

	for _, ref := range []string{"supabase", "Supabase", supabaseSlug} {
		got, err := findTemplate(ctx, api, wsSlug, ref)
		if err != nil || got.template.Slug != supabaseSlug {
			t.Errorf("findTemplate(%q) = %+v, %v", ref, got, err)
		}
	}
	// A draft is not listed, but its owner can deploy it by slug.
	if got, err := findTemplate(ctx, api, wsSlug, draftSlug); err != nil || got.template.Name != "Half done" {
		t.Errorf("draft by slug = %+v, %v", got, err)
	}
	_, err := findTemplate(ctx, api, wsSlug, "wordpress")
	if err == nil || !strings.Contains(err.Error(), "Supabase") {
		t.Errorf("unknown template error = %v, want the available names", err)
	}
}

func TestFindTemplateAmbiguousName(t *testing.T) {
	routes := templateRoutes()
	routes["/v1/workspaces/"+wsSlug+"/templates"] = `[{"slug":"` + ownSlug + `","name":"Supabase","status":"published","services":[]}]`
	api, _ := routedAPI(t, routes)
	_, err := findTemplate(context.Background(), api, wsSlug, "supabase")
	if err == nil || !strings.Contains(err.Error(), ownSlug) || !strings.Contains(err.Error(), supabaseSlug) {
		t.Fatalf("error = %v, want both slugs", err)
	}
}

// Service names survive the response redaction, which removes any field named
// "key"; a raw template document would lose them.
func TestDescribeTemplateSurvivesRedaction(t *testing.T) {
	api, _ := routedAPI(t, templateRoutes())
	found, err := findTemplate(context.Background(), api, "", "Supabase")
	if err != nil {
		t.Fatal(err)
	}
	result := jsonText(describeTemplate(found.template, found.source))
	out := resultText(t, result)
	for _, want := range []string{`"service": "supabase_db"`, `"image": "supabase/postgres:17.6.1.136"`, `"disk_gb": 20`, `"public": true`, `"vcpus": 3`, `"memory_mb": 2304`, `"init_sql_service": "supabase_db"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %s:\n%s", want, out)
		}
	}
}

// deployAPI stubs the API for deploy-template, recording the deploy request.
func deployAPI(t *testing.T, deployed *map[string]any) {
	t.Helper()
	routes := templateRoutes()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/deploy") {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, deployed)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"services":[{"slug":"s1","name":"supabase_db"},{"slug":"s2","name":"supabase_gateway"}],
				"outputs":[{"service":"supabase_gateway","name":"ANON_KEY","value":"eyJhbGciOi.anon","description":"Public API key"},
				           {"service":"supabase_gateway","name":"SUPABASE_PUBLIC_URL","value":"https://x-production.simplifyd.app"}]}`))
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("SIMPLIFYD_API_URL", ts.URL)
}

// Publishable outputs come back with their values, so an assistant can
// configure a client; the init SQL reaches the API.
func TestDeployTemplateOutputsAndInitSQL(t *testing.T) {
	var deployed map[string]any
	deployAPI(t, &deployed)

	result, _, err := handleDeployTemplate(context.Background(), httpRequest("Bearer x"), deployTemplateArgs{
		Workspace: wsSlug, Project: projSlug, Env: envSlug, Template: "Supabase",
		InitSQL: "create table orders (id int);",
	})
	if err != nil || result.IsError {
		t.Fatalf("deploy-template: %v %s", err, resultText(t, result))
	}
	if deployed["init_sql"] != "create table orders (id int);" || deployed["project"] != projSlug {
		t.Errorf("deploy body = %v", deployed)
	}
	out := resultText(t, result)
	for _, want := range []string{`"value": "eyJhbGciOi.anon"`, `"value": "https://x-production.simplifyd.app"`, `"name": "supabase_gateway"`, `init_sql applied`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %s:\n%s", want, out)
		}
	}
}

func TestDeployTemplateRefusesInitSQLItCannotTake(t *testing.T) {
	var deployed map[string]any
	deployAPI(t, &deployed)

	result, _, _ := handleDeployTemplate(context.Background(), httpRequest("Bearer x"), deployTemplateArgs{
		Workspace: wsSlug, Project: projSlug, Env: envSlug, Template: "Internal API",
		InitSQL: "create table t ();",
	})
	if !result.IsError || !strings.Contains(resultText(t, result), "does not take init_sql") {
		t.Fatalf("result = %s", resultText(t, result))
	}
	if deployed != nil {
		t.Errorf("deployed anyway: %v", deployed)
	}
}

func TestGetPublishableVariables(t *testing.T) {
	path := "/v1/workspaces/" + wsSlug + "/projects/" + projSlug + "/envs/" + envSlug + "/publishable-variables"
	for _, c := range []struct{ body, want string }{
		{`[{"service_slug":"s","service":"supabase_gateway","name":"ANON_KEY","value":"eyJhbGciOi.anon"}]`, `"value": "eyJhbGciOi.anon"`},
		{`[]`, "no publishable variables"},
	} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(c.body))
		}))
		t.Setenv("SIMPLIFYD_API_URL", ts.URL)
		result, _, err := handleGetPublishableVariables(context.Background(), httpRequest("Bearer x"), envArgs{Workspace: wsSlug, Project: projSlug, Env: envSlug})
		ts.Close()
		if err != nil || result.IsError {
			t.Fatalf("get-publishable-variables: %v %s", err, resultText(t, result))
		}
		if out := resultText(t, result); !strings.Contains(out, c.want) {
			t.Errorf("output missing %s:\n%s", c.want, out)
		}
	}
}

// A new variable is sent without sealed, so the API seals it; sealed false is
// passed through, for a create and for an update.
func TestAddServiceVariableSealed(t *testing.T) {
	base := "/v1/workspaces/" + wsSlug + "/projects/" + projSlug + "/envs/" + envSlug + "/svcs/svc-1/variables"
	for _, c := range []struct {
		existing string
		sealed   *bool
		want     string
	}{
		{`[]`, nil, `absent`},
		{`[]`, new(bool), `false`},
		{`[{"slug":"v1","name":"PW","sealed":true}]`, new(bool), `false`},
	} {
		var body map[string]any
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == base:
				_, _ = w.Write([]byte(c.existing))
			case (r.Method == http.MethodPost && r.URL.Path == base) || (r.Method == http.MethodPut && r.URL.Path == base+"/v1"):
				_ = json.NewDecoder(r.Body).Decode(&body)
				_, _ = w.Write([]byte(`{"slug":"v1","name":"PW"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		t.Setenv("SIMPLIFYD_API_URL", ts.URL)
		result, _, err := handleAddServiceVariable(context.Background(), httpRequest("Bearer x"), addSvcVarArgs{
			Workspace: wsSlug, Project: projSlug, Env: envSlug, Service: "svc-1", Name: "PW", Value: "x", Sealed: c.sealed,
		})
		ts.Close()
		if err != nil || result.IsError {
			t.Fatalf("add-service-variable: %v %s", err, resultText(t, result))
		}
		got := "absent"
		if v, ok := body["sealed"]; ok {
			got = fmt.Sprint(v)
		}
		if got != c.want {
			t.Errorf("existing %s, sealed %v: sent sealed %s, want %s", c.existing, c.sealed, got, c.want)
		}
	}
}
