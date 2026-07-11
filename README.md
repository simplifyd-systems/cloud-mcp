# Simplifyd Cloud MCP Server

An [MCP (Model Context Protocol)](https://modelcontextprotocol.io) server for the Simplifyd Cloud platform, built with Go and the [official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk).

It exposes 57 tools spanning the full Simplifyd Cloud API over a stdio transport, making it compatible with Claude Code, Claude Desktop, Cursor, and any other MCP-enabled client. All API calls go through the shared [cloud-go-sdk](https://github.com/simplifyd-systems/cloud-go-sdk) (the same client the `edge` CLI uses), so API payloads stay in sync automatically.

## Tools

| Category | Tools |
|---|---|
| **Auth** | `login`, `get-me` |
| **Workspaces** | `list-workspaces`, `get-workspace`, `create-workspace`, `update-workspace`, `get-workspace-usage`, `get-workspace-transactions`, `get-my-role`, `list-workspace-members`, `add-workspace-member`, `update-member-role`, `remove-workspace-member`, `fund-workspace`, `get-registry`, `get-registry-credentials`, `list-registry-repos` |
| **Projects** | `list-projects`, `get-project`, `create-project`, `update-project` |
| **Environments** | `list-environments`, `get-environment`, `create-environment`, `update-environment`, `list-env-variables`, `create-env-variable`, `update-env-variable`, `delete-env-variable` |
| **Services** | `list-services`, `get-service`, `create-service`, `update-service`, `delete-service`, `list-service-variables`, `add-service-variable`, `set-service-variables`, `delete-service-variable`, `add-service-ingress`, `delete-service-ingress`, `add-tcp-proxy`, `delete-tcp-proxy`, `add-service-config`, `update-service-config`, `delete-service-config`, `approve-service-changeset`, `discard-service-changeset`, `add-shared-variable` |
| **Deployments** | `list-deployments`, `get-deployment`, `deploy-service`, `redeploy-service`, `undeploy-service`, `get-deployment-logs` |
| **Tokens** | `list-tokens`, `create-token`, `delete-token` |

## Build

```bash
go build -o cloud-mcp .
```

## Authentication

**Option 1 — Environment variable (recommended for CI):**
```bash
export SIMPLIFYD_API_TOKEN="your-jwt-token"
```

**Option 2 — Interactive login:**
Use the `login` tool to authenticate with your email and password. The token is saved to `~/.simplifyd/config.json` and reused automatically on subsequent runs.

## Claude Code Configuration

Add to your MCP settings (`.claude/settings.json` or `~/.claude/settings.json`):

```json
{
  "mcpServers": {
    "simplifyd-cloud": {
      "command": "/path/to/cloud/mcp/cloud-mcp",
      "env": {
        "SIMPLIFYD_API_TOKEN": "your-jwt-token"
      }
    }
  }
}
```

Or if you prefer to authenticate interactively (omit `SIMPLIFYD_API_TOKEN` and run `login` first):

```json
{
  "mcpServers": {
    "simplifyd-cloud": {
      "command": "/path/to/cloud/mcp/cloud-mcp"
    }
  }
}
```

## Architecture

```
mcp/
├── main.go              # Entry point — creates server, registers tools, runs stdio transport
├── client/
│   └── client.go        # Auth/config: token resolution + persistence (~/.simplifyd/config.json)
└── tools/               # Thin MCP wrappers over the cloud-go-sdk
    ├── helpers.go        # Shared utilities (text/JSON responses, auth check, error helpers)
    ├── auth.go           # login, get-me
    ├── workspaces.go     # Workspace management + billing
    ├── projects.go       # Project CRUD
    ├── environments.go   # Environment + variable management
    ├── services.go       # Service CRUD, variables, ingress, TCP proxy, configs, changesets
    ├── deployments.go    # Deploy, redeploy, undeploy, logs
    └── tokens.go         # API token management
```
