# ChatGPT plugin package

The files OpenAI's plugin portal (platform.openai.com/plugins) takes for the
public listing at the existing URL `https://mcp.simplifyd.com`. This endpoint has its own
OAuth resource identifier and registers only the tools in `tools/openai.go`.

- `plugin.json`: the listing (name, descriptions, URLs, starter prompts, icon)
  and the review block: 5 positive and 3 negative test cases, the demo video
  URL and the commerce declaration. The test cases assume the MCP Review
  workspace's `demo` project with `api` and PostgreSQL in `production` and a
  `staging` environment. Give the reviewer access only to the demo workspace.
- `mcp.json`: the server and its auth: OAuth with dynamic client registration
  against `api.cloud.simplifyd.com`, so there is no client ID or secret.
- `assets/logo.png`: the 512px square icon, rendered from `assets/logo.svg`
  (the arc of the simplifyd.com wordmark).

## Before uploading

1. Roll out Cloud API with its OpenAI OAuth route restrictions, data projections,
   and updated `OAUTH_RESOURCES`, then roll out the MCP build. The
   root URL serves the listing. General hosted clients connect to `/mcp` and
   authorize that OAuth resource separately.
2. Record ChatGPT running the starter prompts with the custom connector. Add
   a link reviewers can open in the portal’s review details before submitting.
3. Package with `cd openai-plugin && zip -r ../simplifyd-cloud-openai-1.0.3.zip plugin.json mcp.json assets`.
4. Reviewer credentials go in the dashboard's Review details, never in these
   files: the portal rejects `test_credentials` in the package.
5. Domain verification: put the token the portal shows in `OPENAI_APPS_CHALLENGE`
   on the cloud-mcp deployment and restart it (`make restart`); the server
   serves it at `/.well-known/openai-apps-challenge`.
6. Keep `mcp.json` identical to the original submission, with `https://mcp.simplifyd.com`
   as the literal URL. Reconnect OAuth if prompted,
   and select **Scan Tools**. Check the catalog and annotation values against the
   table below before resubmitting.

## Annotation justifications

| Tool | `readOnlyHint` | `destructiveHint` | `openWorldHint` | Justification |
| --- | --- | --- | --- | --- |
| `list-workspaces` | true | false | false | Reads names and slugs of the workspaces in the user's grant. |
| `list-projects` | true | false | false | Reads project names and slugs in one granted workspace. |
| `list-environments` | true | false | false | Reads environment names and slugs in one existing project. |
| `list-services` | true | false | false | Reads service inventory, status, and allocations in an existing environment. |
| `get-service` | true | false | false | Reads type, allocation and status from the existing service and its managed cluster. |
| `list-deployments` | true | false | false | Reads history and active status for one existing service. |
| `get-deployment` | true | false | false | Reads status, deployment time and whether a deployment is active. |
| `get-service-metrics` | true | false | false | Reads and summarizes CPU, memory and disk metrics for an existing service. |
| `list-postgres-extensions` | true | false | false | Reads installed extensions and the supported list for an existing managed database. |
| `list-domains` | true | false | false | Reads registration status, expiry and auto-renewal status from workspace records. |
| `get-domain` | true | false | true | Reads the existing registration and looks up nameservers and transfer-lock status at the registrar. |
| `list-dns-zones` | true | false | false | Reads hosted zone names, nameservers and status from workspace records. |
| `check-dns-zone` | false | true | true | Requires explicit confirmation. Looks up public nameservers and can activate an already delegated pending zone. Activation publishes stored DNS records and can replace records previously served for that zone. An active zone is returned without changes. |

## Known quirks

`mcp.json` follows OpenAI's build guide, which puts the OAuth settings under
`extensions["com.openai"].auth`. The published agent-plugins schema does not
allow `extensions` on a server entry, so a strict validator rejects the file. If
the portal does too, delete the `extensions` block: the server advertises OAuth
and dynamic registration itself through its 401 challenge and
`/.well-known/oauth-protected-resource`.

Changes to listing metadata, prompts or test cases use a new draft version.
