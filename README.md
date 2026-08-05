# Simplifyd Cloud MCP Server

An [MCP (Model Context Protocol)](https://modelcontextprotocol.io) server for the Simplifyd Cloud platform.

It exposes tools for managing Simplifyd Cloud resources from MCP-enabled clients,
over either **stdio** (a local client launches the binary as a subprocess) or
**streamable HTTP** (a hosted, multi-user deployment). One binary serves both;
`MCP_TRANSPORT` selects.

## Tools

| Category | Tools |
|---|---|
| **Auth** | `login` (stdio only), `get-me` |
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

## Transports

| Variable | Default | Meaning |
|---|---|---|
| `MCP_TRANSPORT` | `stdio` | `stdio` or `http` |
| `MCP_ADDR` | `:8080` | Listen address (http only) |
| `MCP_ALLOWED_ORIGINS` | *(none)* | Comma-separated browser origin allowlist (http only) |
| `SIMPLIFYD_API_URL` | `https://api.cloud.simplifyd.com` | API base URL |

Under HTTP the MCP endpoint answers on the bare origin and on `/mcp`, with
a plain `/healthz` for probes.
Sessions are stateless, so replicas need no sticky routing.

## Authentication

Authentication is per-transport, and the two never mix:

- **stdio** — a single local user. The token comes from `SIMPLIFYD_API_TOKEN` or
  `~/.simplifyd/config.json`, and the `login` tool can set it interactively.
- **http** — many callers. Each request must carry its own
  `Authorization: Bearer <token>`, which is used for that call and nothing else.
  Nothing is persisted, no credential is shared between callers, and `login` is
  not registered, since it would write one caller's credentials to the server's
  disk and apply them to everyone else.

### stdio

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

### http

Run the server:

```bash
MCP_TRANSPORT=http MCP_ADDR=:8080 ./cloud-mcp
```

Point a client at it, passing your API token as a header:

```json
{
  "mcpServers": {
    "simplifyd-cloud": {
      "type": "http",
      "url": "https://mcp.simplifyd.com",
      "headers": {
        "Authorization": "Bearer your-jwt-token"
      }
    }
  }
}
```

Create a token with the `create-token` tool, or in the dashboard.

## Security

Requests carrying an `Origin` header are refused unless the origin is listed in
`MCP_ALLOWED_ORIGINS`. This blocks DNS-rebinding attacks, where a page in a
user's browser would otherwise be able to drive a hosted server. Non-browser
clients send no `Origin` and are unaffected.

Normal resource responses omit variable values, passwords, connection URLs, credential blobs, and config-file contents. Tools whose descriptions say they explicitly reveal credentials return sensitive data by design; use them only when needed and avoid copying their output into logs or tickets.

Deployment logs are filtered for common credential patterns, but applications can emit secrets in unexpected formats. Review log output before sharing it.
