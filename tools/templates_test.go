package tools

import (
	"context"
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
	for _, want := range []string{`"service": "supabase_db"`, `"image": "supabase/postgres:17.6.1.136"`, `"disk_gb": 20`, `"public": true`, `"vcpus": 3`, `"memory_mb": 2304`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %s:\n%s", want, out)
		}
	}
}
