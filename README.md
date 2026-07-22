# Simplifyd Cloud MCP Server

An [MCP (Model Context Protocol)](https://modelcontextprotocol.io) server for the Simplifyd Cloud platform.

It exposes tools for managing Simplifyd Cloud resources from MCP-enabled clients.

## Tools

| Category | Tools |
|---|---|
| **Auth** | `login`, `get-me` |
| **Workspaces** | `list-workspaces`, `get-workspace`, `create-workspace`, `update-workspace`, `get-workspace-usage`, `get-workspace-transactions`, `get-my-role`, `list-workspace-members`, `add-workspace-member`, `update-member-role`, `remove-workspace-member`, `fund-workspace`, `get-registry`, `get-registry-credentials`, `list-registry-repos` |
| **Projects** | `list-projects`, `get-project`, `create-project`, `update-project` |
| **Environments** | `list-environments`, `get-environment`, `create-environment`, `update-environment`, `list-env-variables`, `create-env-variable`, `update-env-variable`, `delete-env-variable` |
| **Services** | `list-services`, `get-service`, `create-service`, `update-service`, `delete-service`, `list-private-service-access`, `grant-private-service-access`, `revoke-private-service-access`, `list-service-variables`, `add-service-variable`, `set-service-variables`, `delete-service-variable`, `add-service-ingress`, `delete-service-ingress`, `add-tcp-proxy`, `delete-tcp-proxy`, `set-ingress-source-ranges`, `add-service-config`, `update-service-config`, `delete-service-config`, `approve-service-changeset`, `discard-service-changeset`, `add-shared-variable` |
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

## Security

Normal resource responses omit variable values, passwords, connection URLs, credential blobs, and config-file contents. Tools whose descriptions say they explicitly reveal credentials return sensitive data by design; use them only when needed and avoid copying their output into logs or tickets.

Deployment logs are filtered for common credential patterns, but applications can emit secrets in unexpected formats. Review log output before sharing it.
