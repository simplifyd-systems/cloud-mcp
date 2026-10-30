package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// These cover what a run of every tool against a real workspace turned up
// before the directory submissions: names a reviewer types that never reached
// the API as slugs, empty and padded responses, and refusals with no reason.

const (
	svcSlug      = "01a18e6c-0b05-7307-b26b-a50a61216c52"
	consumerSlug = "01a18e60-cccb-7f06-b47a-e66dca384437"
)

func TestResolveArgsTranslatesServiceAndConsumerProjectNames(t *testing.T) {
	api, _ := routedAPI(t, map[string]string{
		"/v1/workspaces": `[{"slug":"` + wsSlug + `","name":"MCP Review"}]`,
		"/v1/workspaces/" + wsSlug + "/projects": `[{"slug":"` + projSlug + `","name":"demo"},
			{"slug":"` + consumerSlug + `","name":"default"}]`,
		"/v1/workspaces/" + wsSlug + "/projects/" + projSlug + "/envs": `[{"slug":"` + envSlug + `","name":"production"}]`,
		"/v1/workspaces/" + wsSlug + "/projects/" + projSlug + "/envs/" + envSlug + "/svcs": `[{"slug":"` + svcSlug + `","name":"api"}]`,
	})

	args := struct {
		Workspace       string
		Project         string
		Env             string
		Service         string
		ConsumerProject string
	}{Workspace: "MCP Review", Project: "demo", Env: "production", Service: "API", ConsumerProject: "default"}

	if err := resolveArgs(context.Background(), api, &args); err != nil {
		t.Fatalf("resolveArgs: %v", err)
	}
	if args.Service != svcSlug {
		t.Errorf("service = %q, want %q", args.Service, svcSlug)
	}
	if args.ConsumerProject != consumerSlug {
		t.Errorf("consumer project = %q, want %q", args.ConsumerProject, consumerSlug)
	}
}

func TestResolveArgsListsServicesOnMiss(t *testing.T) {
	api, _ := routedAPI(t, map[string]string{
		"/v1/workspaces/" + wsSlug + "/projects/" + projSlug + "/envs/" + envSlug + "/svcs": `[{"slug":"` + svcSlug + `","name":"api"}]`,
	})
	args := struct{ Workspace, Project, Env, Service string }{wsSlug, projSlug, envSlug, "web"}
	err := resolveArgs(context.Background(), api, &args)
	if err == nil || !strings.Contains(err.Error(), "available: api") {
		t.Fatalf("err = %v, want it to list the services that exist", err)
	}
}

// A service of one type carries a zeroed block for every other type, and
// timestamps the API did not fill come back as year 1.
func TestJSONTextDropsPadding(t *testing.T) {
	out := resultText(t, jsonText(map[string]any{
		"name":              "api",
		"created_at":        "0001-01-01T00:00:00Z",
		"status":            "",
		"replicas":          0,
		"docker_image_svc":  map[string]any{"image": "nginx", "tag": "alpine"},
		"kafka_svc":         map[string]any{"brokers": 0, "mode": ""},
		"http_gateway_svc":  map[string]any{},
		"email_svc":         map[string]any{"suspended": false, "smtp_port": 0},
		"installed":         []any{},
		"has_verified_flag": false,
	}))
	for _, gone := range []string{"created_at", "status", "kafka_svc", "http_gateway_svc", "email_svc"} {
		if strings.Contains(out, `"`+gone+`"`) {
			t.Errorf("%s survived:\n%s", gone, out)
		}
	}
	for _, kept := range []string{`"replicas": 0`, `"nginx"`, `"installed": []`, `"has_verified_flag": false`} {
		if !strings.Contains(out, kept) {
			t.Errorf("%s was dropped:\n%s", kept, out)
		}
	}
}

func TestJSONTextDoesNotEscapeHTML(t *testing.T) {
	out := resultText(t, jsonTextRaw(map[string]string{"content": "<h1>hi</h1>"}))
	if !strings.Contains(out, "<h1>hi</h1>") {
		t.Fatalf("HTML was escaped: %s", out)
	}
}

// Several API writes answer with only a success flag, which the SDK decodes
// into a zero struct; the tool must say it worked rather than show {}.
func TestEmptyWriteResultNamesTheTool(t *testing.T) {
	if jsonText(struct {
		Name    string `json:"name"`
		Memory  int    `json:"memory"`
		Created string `json:"created_at"`
	}{Created: "0001-01-01T00:00:00Z"}) != nothingToReport {
		t.Fatal("an all-zero response was not recognised as having nothing to report")
	}
	if got := resultText(t, jsonText([]string(nil))); got != "No results." {
		t.Fatalf("nil list rendered as %q", got)
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	setToken("t")
	t.Cleanup(func() { setToken("") })
	addTool(server, &mcp.Tool{Name: "update-workspace"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return jsonText(struct {
			Name string `json:"name"`
		}{}), nil, nil
	})
	session := connect(t, server)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "update-workspace", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := resultText(t, res); got != "Update workspace succeeded." {
		t.Fatalf("result = %q", got)
	}
}

// The API's explanation of a 4xx is shown; anything from a 5xx is not.
func TestAPIErrShowsCustomerFacingRefusal(t *testing.T) {
	out := resultText(t, apiErr("add service ingress", &cloud.APIError{
		StatusCode: http.StatusBadRequest,
		Message:    "port 80 is already exposed as TCP; choose a different backend port for HTTP ingress",
	}))
	if !strings.Contains(out, "already exposed as TCP") || !strings.Contains(out, "reference") {
		t.Fatalf("refusal reason missing: %s", out)
	}
	if out := resultText(t, apiErr("x", &cloud.APIError{StatusCode: http.StatusBadRequest, Message: "HTTP 400"})); strings.Contains(out, "HTTP 400") {
		t.Fatalf("SDK placeholder shown as a reason: %s", out)
	}
}

// On the hosted server a path names a file in our own container.
func TestLocalFileToolsAreRefusedOverHTTP(t *testing.T) {
	for _, tc := range []struct {
		local bool
		want  bool
	}{{true, true}, {false, false}} {
		server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
		RegisterVideoTools(server, tc.local)
		if got := hasTool(t, server, "upload-video"); got != tc.want {
			t.Errorf("local=%v: upload-video registered = %v, want %v", tc.local, got, tc.want)
		}
		if !hasTool(t, server, "list-videos") {
			t.Errorf("local=%v: list-videos missing", tc.local)
		}
	}

	res, _, _ := handleDeployStaticSite(context.Background(), httpRequest("Bearer t"),
		deployStaticSiteArgs{Workspace: wsSlug, Project: projSlug, Env: envSlug, Name: "site", ArchivePath: "/etc/passwd.zip"})
	if !res.IsError || !strings.Contains(resultText(t, res), "create-static-site-upload") {
		t.Fatalf("archive_path over HTTP = %+v", res)
	}
	res, _, _ = handleUploadVideo(context.Background(), httpRequest("Bearer t"),
		uploadVideoArgs{Workspace: wsSlug, Project: projSlug, Env: envSlug, Library: "lib", Path: "/var/run/secrets/token"})
	if !res.IsError {
		t.Fatal("upload-video over HTTP was not refused")
	}
}

func connect(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}
