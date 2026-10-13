package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

const computerBase = "/v1/workspaces/ws/projects/p/envs/prod/svcs/0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

type fakeComputer struct {
	mu    sync.Mutex
	calls []string
	// bodies records each POST body by path.
	bodies map[string]map[string]any
}

func (f *fakeComputer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+strings.TrimPrefix(r.URL.Path, computerBase))
		if data, _ := io.ReadAll(r.Body); len(data) > 0 {
			var body map[string]any
			_ = json.Unmarshal(data, &body)
			if f.bodies == nil {
				f.bodies = map[string]map[string]any{}
			}
			f.bodies[strings.TrimPrefix(r.URL.Path, computerBase)] = body
		}
		f.mu.Unlock()

		switch r.URL.Path {
		case computerBase + "/computer/screenshot":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG"))
		case computerBase + "/computer/bash":
			_, _ = w.Write([]byte(`{"output":"nope\n","exit_code":2}`))
		case computerBase + "/computer/key":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"VALIDATION_ERROR","message":"(Invalid key sequence 'ctrl+frob')"}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}
}

func target() computerTarget {
	return computerTarget{Workspace: "ws", Project: "p", Env: "prod", Service: "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"}
}

func intp(i int) *int { return &i }

func TestComputerActionClicksThenScreenshots(t *testing.T) {
	fake := &fakeComputer{}
	withFakeAPI(t, fake.handler(t))

	result, _, _ := handleComputerAction(context.Background(), nil, computerActionArgs{
		computerTarget: target(), Action: "double_click", X: intp(40), Y: intp(50),
	})
	if result.IsError {
		t.Fatalf("error: %+v", result.Content)
	}
	want := []string{"POST /computer/click", "GET /computer/screenshot"}
	if strings.Join(fake.calls, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v, want %v", fake.calls, want)
	}
	if b := fake.bodies["/computer/click"]; b["double"] != true || b["x"] != float64(40) {
		t.Errorf("click body = %v", b)
	}
	img, ok := result.Content[len(result.Content)-1].(*mcp.ImageContent)
	if !ok || img.MIMEType != "image/png" || string(img.Data) != "\x89PNG" {
		t.Errorf("last content = %#v, want the screenshot", result.Content[len(result.Content)-1])
	}
}

func TestComputerActionCanSkipScreenshot(t *testing.T) {
	fake := &fakeComputer{}
	withFakeAPI(t, fake.handler(t))
	no := false
	_, _, _ = handleComputerAction(context.Background(), nil, computerActionArgs{
		computerTarget: target(), Action: "type", Text: "hello", Screenshot: &no,
	})
	if len(fake.calls) != 1 || fake.calls[0] != "POST /computer/type" {
		t.Errorf("calls = %v", fake.calls)
	}
}

func TestComputerActionValidatesArgumentsLocally(t *testing.T) {
	fake := &fakeComputer{}
	withFakeAPI(t, fake.handler(t))
	for _, args := range []computerActionArgs{
		{computerTarget: target(), Action: "left_click"},
		{computerTarget: target(), Action: "left_click_drag", X: intp(1), Y: intp(1)},
		{computerTarget: target(), Action: "type"},
		{computerTarget: target(), Action: "teleport"},
	} {
		result, _, _ := handleComputerAction(context.Background(), nil, args)
		if !result.IsError {
			t.Errorf("%s with %+v should fail", args.Action, args)
		}
	}
	if len(fake.calls) != 0 {
		t.Errorf("invalid actions reached the API: %v", fake.calls)
	}
}

func TestComputerErrorsKeepTheirReason(t *testing.T) {
	// A key xdotool does not know is something the agent can fix, but only if
	// it is told which key.
	fake := &fakeComputer{}
	withFakeAPI(t, fake.handler(t))
	result, _, _ := handleComputerAction(context.Background(), nil, computerActionArgs{
		computerTarget: target(), Action: "key", Text: "ctrl+frob",
	})
	if out := resultText(t, result); !result.IsError || !strings.Contains(out, "ctrl+frob") {
		t.Errorf("result = %q", out)
	}
}

func TestComputerBashReportsExitCode(t *testing.T) {
	fake := &fakeComputer{}
	withFakeAPI(t, fake.handler(t))
	result, _, _ := handleComputerBash(context.Background(), nil, computerBashArgs{computerTarget: target(), Command: "false", TimeoutSeconds: 5})
	out := resultText(t, result)
	if !result.IsError || !strings.Contains(out, "nope") || !strings.Contains(out, "exit code 2") {
		t.Errorf("result = %q (isError %v)", out, result.IsError)
	}
	if fake.bodies["/computer/bash"]["timeout"] != float64(5) {
		t.Errorf("bash body = %v", fake.bodies["/computer/bash"])
	}
}

func TestComputerResolvesMachineByName(t *testing.T) {
	fake := &fakeComputer{}
	withFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/workspaces/ws/projects/p/envs/prod/svcs" {
			_ = json.NewEncoder(w).Encode([]cloud.Service{
				{Slug: "0199a1b2-0000-7e5f-8a9b-000000000000", Name: "research", VM: &cloud.VMConfig{Image: "ubuntu-24.04"}},
				{Slug: "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", Name: "research", VM: &cloud.VMConfig{Image: cloud.VMImageDesktop}},
			})
			return
		}
		fake.handler(t)(w, r)
	})
	args := target()
	args.Service = "research"
	result, _, _ := handleComputerScreenshot(context.Background(), nil, args)
	if result.IsError || len(fake.calls) != 1 || fake.calls[0] != "GET /computer/screenshot" {
		t.Errorf("calls = %v, result = %+v", fake.calls, result.Content)
	}
}

func TestComputerToolSchemasGenerate(t *testing.T) {
	// addTool panics at registration if an argument struct cannot be turned
	// into a schema; the embedded target struct is the risky part.
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	RegisterComputerTools(server)
}
