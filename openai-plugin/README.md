# ChatGPT plugin package

The files OpenAI's plugin portal (platform.openai.com/plugins) takes for the
hosted server at `https://mcp.simplifyd.com`.

- `plugin.json`: the listing (name, descriptions, URLs, starter prompts, icon)
  and the review block: 5 positive and 3 negative test cases, the demo video
  URL and the commerce declaration. The test cases assume the MCP Review
  workspace's `demo` project: `api` and Postgres in `production`, `staging`
  empty.
- `mcp.json`: the server and its auth: OAuth with dynamic client registration
  against `api.cloud.simplifyd.com`, so there is no client ID or secret.
- `assets/logo.png`: the 512px square icon, rendered from `assets/logo.svg`
  (the arc of the simplifyd.com wordmark).

## Before uploading

1. Replace `REPLACE_WITH_DEMO_VIDEO_URL` in `plugin.json` with a link reviewers
   can open: a recording of ChatGPT (developer mode, custom connector) running
   the starter prompts.
2. Package it: `cd openai-plugin && zip -r ../simplifyd-cloud-plugin.zip plugin.json mcp.json assets`
3. Reviewer credentials go in the dashboard's Review details, never in these
   files: the portal rejects `test_credentials` in the package.
4. Domain verification: put the token the portal shows in `OPENAI_APPS_CHALLENGE`
   on the cloud-mcp deployment and restart it (`make restart`); the server
   serves it at `/.well-known/openai-apps-challenge`.

## Known quirks

`mcp.json` follows OpenAI's build guide, which puts the OAuth settings under
`extensions["com.openai"].auth`. The published agent-plugins schema does not
allow `extensions` on a server entry, so a strict validator rejects the file. If
the portal does too, delete the `extensions` block: the server advertises OAuth
and dynamic registration itself through its 401 challenge and
`/.well-known/oauth-protected-resource`.

Updates to tools need no new upload: OpenAI rescans the server daily. A change
to the listing, prompts or test cases does: bump `version` and upload again.
