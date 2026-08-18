package tools

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// Coordinates in this API are UUID slugs, but nobody says "deploy to
// 019b9570-5cb9-7296-9a28-21eed0b5ec42" — they say "deploy to pluralWorkspace".
// A model calling these tools passes on whatever the user said, so a name
// arrives where a slug is expected and the API refuses it: handlers parse the
// path segment as a UUID, and for a project token the middleware compares it
// against the token's own slug and 403s the request before any handler runs.
// Every call fails, which reads as "the MCP is denying everything".
//
// So names are resolved to slugs here, at the edge, before the SDK builds a
// URL from them. A value that already parses as a UUID is passed through
// untouched and costs nothing; only a name triggers the lookup.

// isSlug reports whether ref is already a slug and needs no resolution.
//
// Matched by shape rather than with a UUID library: this only has to separate
// "a slug the API will accept" from "something a person typed", and a
// dependency for that is not worth carrying. A name that happened to be shaped
// like a UUID would be indistinguishable, but the API rejects such names.
func isSlug(ref string) bool {
	if len(ref) != 36 {
		return false
	}
	for i, r := range ref {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			isHex := (r >= '0' && r <= '9') ||
				(r >= 'a' && r <= 'f') ||
				(r >= 'A' && r <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

// resolveWorkspace turns a workspace name into its slug.
func resolveWorkspace(ctx context.Context, api *cloud.Client, ref string) (string, error) {
	if ref == "" || isSlug(ref) {
		return ref, nil
	}
	workspaces, err := api.ListWorkspaces(ctx)
	if err != nil {
		// A project token may be refused the listing, but it is pinned to a
		// single workspace whose name we can compare against directly.
		if scope, scopeErr := api.TokenScope(ctx); scopeErr == nil && scope.IsProjectToken() {
			if strings.EqualFold(scope.WorkspaceName, ref) {
				return scope.Workspace, nil
			}
			return "", fmt.Errorf(
				"this token is scoped to workspace %q, not %q", scope.WorkspaceName, ref)
		}
		return "", fmt.Errorf("could not look up workspace %q: %w", ref, err)
	}
	names := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		if strings.EqualFold(workspace.Name, ref) {
			return workspace.Slug, nil
		}
		names = append(names, workspace.Name)
	}
	return "", notFound("workspace", ref, names)
}

// resolveProject turns a project name into its slug within a workspace.
func resolveProject(ctx context.Context, api *cloud.Client, workspace, ref string) (string, error) {
	if ref == "" || isSlug(ref) {
		return ref, nil
	}
	projects, err := api.Workspace(workspace).ListProjects(ctx)
	if err != nil {
		return "", fmt.Errorf("could not look up project %q: %w", ref, err)
	}
	names := make([]string, 0, len(projects))
	for _, project := range projects {
		if strings.EqualFold(project.Name, ref) {
			return project.Slug, nil
		}
		names = append(names, project.Name)
	}
	return "", notFound("project", ref, names)
}

// resolveEnv turns an environment name into its slug within a project.
func resolveEnv(ctx context.Context, api *cloud.Client, workspace, project, ref string) (string, error) {
	if ref == "" || isSlug(ref) {
		return ref, nil
	}
	envs, err := api.Workspace(workspace).Project(project).ListEnvs(ctx)
	if err != nil {
		return "", fmt.Errorf("could not look up environment %q: %w", ref, err)
	}
	names := make([]string, 0, len(envs))
	for _, env := range envs {
		if strings.EqualFold(env.Name, ref) {
			return env.Slug, nil
		}
		names = append(names, env.Name)
	}
	return "", notFound("environment", ref, names)
}

// notFound builds the miss message. The available names are listed because the
// caller is usually a model that can retry immediately with a corrected value,
// and otherwise has no way to discover what it got wrong.
func notFound(kind, ref string, available []string) error {
	if len(available) == 0 {
		return fmt.Errorf("no %s named %q, and none are visible to this token", kind, ref)
	}
	return fmt.Errorf("no %s named %q — available: %s", kind, ref, strings.Join(available, ", "))
}

// resolveArgs rewrites the Workspace, Project and Env fields of an arguments
// struct in place, replacing any name with its slug.
//
// Reflection rather than a per-tool edit: all ~60 tools spell these
// coordinates the same way, and a resolution step that has to be remembered at
// each call site is one that will be forgotten by the next tool added.
func resolveArgs(ctx context.Context, api *cloud.Client, args any) error {
	value := reflect.ValueOf(args)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return nil
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct {
		return nil
	}

	// Ordered deliberately: a project is looked up within its workspace, and an
	// environment within its project, so each must already hold a slug.
	workspace := stringField(value, "Workspace")
	project := stringField(value, "Project")
	env := stringField(value, "Env")

	if workspace.IsValid() {
		resolved, err := resolveWorkspace(ctx, api, workspace.String())
		if err != nil {
			return err
		}
		workspace.SetString(resolved)
	}
	if project.IsValid() && workspace.IsValid() {
		resolved, err := resolveProject(ctx, api, workspace.String(), project.String())
		if err != nil {
			return err
		}
		project.SetString(resolved)
	}
	if env.IsValid() && workspace.IsValid() && project.IsValid() {
		resolved, err := resolveEnv(ctx, api, workspace.String(), project.String(), env.String())
		if err != nil {
			return err
		}
		env.SetString(resolved)
	}
	return nil
}

// stringField returns the named settable string field, or the zero Value when
// the struct has no such field.
func stringField(value reflect.Value, name string) reflect.Value {
	field := value.FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.String || !field.CanSet() {
		return reflect.Value{}
	}
	return field
}

// addTool registers a tool whose coordinate arguments are resolved from names
// to slugs before the handler runs. It is a drop-in for mcp.AddTool, and every
// tool in this package goes through it.
func addTool[In, Out any](s *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	mcp.AddTool(s, t, func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		args In,
	) (*mcp.CallToolResult, Out, error) {
		var zero Out
		api, r, ok := sdkFor(req)
		if !ok {
			return r, zero, nil
		}
		if err := resolveArgs(ctx, api, &args); err != nil {
			return toolError(err.Error()), zero, nil
		}
		return h(ctx, req, args)
	})
}
