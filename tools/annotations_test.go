package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// listAllTools returns every tool a client sees on the stdio transport, which
// registers a superset of the HTTP tools (login is stdio-only).
func listAllTools(t *testing.T) []*mcp.Tool {
	t.Helper()
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	RegisterAll(server, true)

	ct, st := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	var all []*mcp.Tool
	for tool, err := range clientSession.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("list tools: %v", err)
		}
		all = append(all, tool)
	}
	return all
}

// Every tool must be classified explicitly. An unlisted tool still gets the
// cautious destructive default at runtime, but that default is a safety net,
// not a classification: this test is what makes adding a tool a decision.
func TestEveryToolIsClassifiedOnce(t *testing.T) {
	registered := map[string]bool{}
	for _, tool := range listAllTools(t) {
		registered[tool.Name] = true
		lists := 0
		for _, set := range []map[string]bool{readOnlyTools, destructiveTools, additiveTools} {
			if set[tool.Name] {
				lists++
			}
		}
		if lists != 1 {
			t.Errorf("tool %q is in %d of readOnlyTools, destructiveTools and additiveTools; want exactly 1",
				tool.Name, lists)
		}
	}

	// A name left behind after a tool is renamed or removed would silently
	// classify nothing.
	for _, set := range []map[string]bool{readOnlyTools, destructiveTools, additiveTools, openWorldTools} {
		for name := range set {
			if !registered[name] {
				t.Errorf("%q is classified but no such tool is registered", name)
			}
		}
	}
}

// The directories read the JSON a client receives, so check that rather than
// the Go values: ChatGPT rejects a tool whose readOnlyHint, destructiveHint or
// openWorldHint is absent, including when its value is false.
func TestToolsListCarriesTitleAndAllHints(t *testing.T) {
	for _, tool := range listAllTools(t) {
		data, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("marshal %q: %v", tool.Name, err)
		}
		var wire struct {
			Title       string         `json:"title"`
			Annotations map[string]any `json:"annotations"`
		}
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatalf("unmarshal %q: %v", tool.Name, err)
		}
		if wire.Title == "" {
			t.Errorf("tool %q has no title", tool.Name)
		}
		if wire.Annotations["title"] != wire.Title {
			t.Errorf("tool %q annotation title %v, want %q", tool.Name, wire.Annotations["title"], wire.Title)
		}
		for _, hint := range []string{"readOnlyHint", "destructiveHint", "openWorldHint"} {
			if _, ok := wire.Annotations[hint].(bool); !ok {
				t.Errorf("tool %q: %s is %v, want an explicit boolean", tool.Name, hint, wire.Annotations[hint])
			}
		}
		if wire.Annotations["readOnlyHint"] == true && wire.Annotations["destructiveHint"] == true {
			t.Errorf("tool %q is marked both read-only and destructive", tool.Name)
		}
	}
}

func TestUnclassifiedToolDefaultsToDestructive(t *testing.T) {
	tool := &mcp.Tool{Name: "not-a-real-tool"}
	annotate(tool)
	if tool.Annotations.ReadOnlyHint || !*tool.Annotations.DestructiveHint {
		t.Fatalf("unclassified tool annotated %+v, want destructive", tool.Annotations)
	}
}

func TestToolTitle(t *testing.T) {
	for name, want := range map[string]string{
		"add-dns-zone":           "Add DNS zone",
		"get-me":                 "Get current user",
		"list-env-variables":     "List environment variables",
		"configure-mysql-backup": "Configure MySQL backup",
		"delete-tcp-proxy":       "Delete TCP proxy",
		"list-services":          "List services",
	} {
		if got := toolTitle(name); got != want {
			t.Errorf("toolTitle(%q) = %q, want %q", name, got, want)
		}
	}
}
