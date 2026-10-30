package tools

import (
	"context"
	"fmt"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// Workspace registry credentials: pull credentials for private container
// registries such as ghcr.io or Docker Hub, which every Docker image service in
// the workspace inherits by the registry its image is on. Passwords go in and
// never come back out: the API does not return them, and jsonText would redact
// one if it did.

// ---- list-registry-credentials ----

func handleListRegistryCredentials(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args workspaceSlugArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	credentials, err := api.Workspace(args.Workspace).RegistryCredentials().List(ctx)
	if err != nil {
		return apiErr("list registry credentials", err), nil, nil
	}
	return jsonText(credentials), nil, nil
}

// ---- add-registry-credential ----

type addRegistryCredentialArgs struct {
	Workspace    string `json:"workspace"     jsonschema:"Workspace slug or name"`
	RegistryHost string `json:"registry_host" jsonschema:"Registry hostname, e.g. ghcr.io, or docker.io for Docker Hub. Not sdcr.io, which always pulls with platform credentials"`
	Username     string `json:"username"      jsonschema:"Registry username"`
	Password     string `json:"password"      jsonschema:"Registry password or access token; a read-only token scoped to pulling images is enough"`
}

func handleAddRegistryCredential(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args addRegistryCredentialArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	if strings.TrimSpace(args.RegistryHost) == "" || args.Username == "" || args.Password == "" {
		return toolError("registry_host, username and password are required"), nil, nil
	}
	credential, err := api.Workspace(args.Workspace).RegistryCredentials().Create(ctx, cloud.CreateRegistryCredentialInput{
		RegistryHost: args.RegistryHost,
		Username:     args.Username,
		Password:     args.Password,
	})
	if err != nil {
		return apiErr("add registry credential", err), nil, nil
	}
	return jsonText(credential), nil, nil
}

// ---- update-registry-credential ----

type updateRegistryCredentialArgs struct {
	Workspace string `json:"workspace"          jsonschema:"Workspace slug or name"`
	Registry  string `json:"registry"           jsonschema:"Registry host (e.g. ghcr.io) or credential ID, as returned by list-registry-credentials"`
	Username  string `json:"username,omitempty" jsonschema:"New username. Omit to keep the current one"`
	Password  string `json:"password,omitempty" jsonschema:"New password or access token. Omit to keep the current one"`
}

func handleUpdateRegistryCredential(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args updateRegistryCredentialArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	if args.Username == "" && args.Password == "" {
		return toolError("a new username or password is required"), nil, nil
	}
	client := api.Workspace(args.Workspace).RegistryCredentials()
	credential, result := findRegistryCredential(ctx, client, args.Registry, "update registry credential")
	if result != nil {
		return result, nil, nil
	}
	updated, err := client.Update(ctx, credential.Slug, cloud.UpdateRegistryCredentialInput{
		Username: args.Username,
		Password: args.Password,
	})
	if err != nil {
		return apiErr("update registry credential", err), nil, nil
	}
	return jsonText(updated), nil, nil
}

// ---- remove-registry-credential ----

type removeRegistryCredentialArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Registry  string `json:"registry"  jsonschema:"Registry host (e.g. ghcr.io) or credential ID, as returned by list-registry-credentials"`
}

func handleRemoveRegistryCredential(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args removeRegistryCredentialArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	client := api.Workspace(args.Workspace).RegistryCredentials()
	credential, result := findRegistryCredential(ctx, client, args.Registry, "remove registry credential")
	if result != nil {
		return result, nil, nil
	}
	if err := client.Delete(ctx, credential.Slug); err != nil {
		return apiErr("remove registry credential", err), nil, nil
	}
	return text(fmt.Sprintf("Removed the credentials for %s", credential.RegistryHost)), nil, nil
}

// findRegistryCredential looks a credential up by its ID or its registry host.
// The non-nil result is the tool's error when there is no such credential.
func findRegistryCredential(ctx context.Context, client *cloud.RegistryCredentialsClient, ref, op string) (*cloud.WorkspaceRegistryCredential, *mcp.CallToolResult) {
	list, err := client.List(ctx)
	if err != nil {
		return nil, apiErr(op, err)
	}
	if c := matchRegistryCredential(list, ref); c != nil {
		return c, nil
	}
	return nil, toolError(fmt.Sprintf("%s: the workspace has no registry credential for %q; list-registry-credentials shows the ones it has", op, ref))
}

func matchRegistryCredential(list []cloud.WorkspaceRegistryCredential, ref string) *cloud.WorkspaceRegistryCredential {
	host := normalizeRegistryHost(ref)
	for i, c := range list {
		if c.Slug == ref || c.RegistryHost == host {
			return &list[i]
		}
	}
	return nil
}

// normalizeRegistryHost reduces a registry reference to the host the API
// stores: no scheme, no path, lower case, and Docker Hub by one name.
func normalizeRegistryHost(ref string) string {
	host := strings.ToLower(strings.TrimSpace(ref))
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host, _, _ = strings.Cut(host, "/")
	switch host {
	case "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com", "hub.docker.com":
		return "docker.io"
	}
	return host
}

// RegisterRegistryCredentialTools registers the workspace registry credential
// tools on s.
func RegisterRegistryCredentialTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name: "list-registry-credentials",
		Description: "List a workspace's credentials for private container registries (ghcr.io, Docker Hub, ...): registry host and username, never the password. " +
			"Every Docker image service in the workspace whose image is on one of these registries pulls with its credentials, unless the service has credentials of its own; " +
			"get-service reports which a service uses as docker_image_svc.registry_credential_source (service, workspace or none).",
	}, handleListRegistryCredentials)

	addTool(s, &mcp.Tool{
		Name: "add-registry-credential",
		Description: "Add a workspace's credentials for a private container registry, e.g. ghcr.io (owner only). " +
			"Every Docker image service in the workspace whose image is on that registry pulls with them from its next image pull, without a redeploy, unless it has credentials of its own; " +
			"a service last deployed before 30 October 2026 picks them up on its next deploy. " +
			"A registry that already has credentials is rejected: use update-registry-credential. " +
			"The password passes through this conversation, so prefer a read-only token scoped to pulling images.",
	}, handleAddRegistryCredential)

	addTool(s, &mcp.Tool{
		Name:        "update-registry-credential",
		Description: "Change the username, password or both of a workspace's registry credential (owner only). Services pulling with it use the new values from their next image pull, without a redeploy.",
	}, handleUpdateRegistryCredential)

	addTool(s, &mcp.Tool{
		Name: "remove-registry-credential",
		Description: "Remove a workspace's registry credential (owner only). " +
			"Services that pulled with it pull without credentials from then on, so a private image fails to pull on their next restart or deploy, unless they have credentials of their own.",
	}, handleRemoveRegistryCredential)
}
