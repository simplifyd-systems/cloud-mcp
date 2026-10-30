package tools

import (
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool annotations tell a client what calling a tool does before it calls it.
// Clients act on them: Claude runs read-only tools without asking and always
// confirms destructive ones, and ChatGPT shows the same distinction. Both
// directories reject a server whose tools carry no annotations, or whose
// annotations misdescribe the tool.
//
// Every tool must appear in exactly one of the three lists below; a test
// enforces it, so a new tool cannot ship unclassified. The lists are by name
// rather than on each mcp.Tool literal so the whole classification can be
// reviewed in one place.

// readOnlyTools only read. Calling one changes nothing a person would notice.
var readOnlyTools = toolSet(
	"check-dns-zone",
	"get-deployment",
	"get-deployment-logs",
	"get-domain",
	"get-environment",
	"get-environment-metrics",
	"get-me",
	"get-my-role",
	"get-postgres-parameters",
	"get-project",
	"get-publishable-variables",
	"get-registry",
	"get-registry-retention",
	"get-service",
	"get-service-config",
	"get-service-metrics",
	"get-static-site",
	"get-static-site-files",
	"get-template",
	"get-video",
	"get-video-stats",
	"get-workspace",
	"get-workspace-transactions",
	"get-workspace-usage",
	"list-alerts",
	"list-deployments",
	"list-dns-records",
	"list-dns-zones",
	"list-domains",
	"list-email-domains",
	"list-env-variables",
	"list-environments",
	"list-postgres-extensions",
	"list-private-service-access",
	"list-projects",
	"list-registry-artifacts",
	"list-registry-credentials",
	"list-registry-repos",
	"list-registry-retention-runs",
	"list-service-configs",
	"list-service-variables",
	"list-services",
	"list-static-site-files",
	"list-templates",
	"list-tokens",
	"list-videos",
	"list-workspace-members",
	"list-workspaces",
	"preview-registry-retention",
	"quote-domain",
	"search-domains",
	"verify-email-domain",
)

// destructiveTools delete, overwrite, revoke access, or spend money. A tool
// belongs here if it can lose something the person had, even when the loss can
// be undone afterwards: a restored value is still one that was overwritten.
var destructiveTools = toolSet(
	"add-service-variable", // replaces an existing variable's value
	"approve-service-changeset",
	"configure-mysql-backup", // an empty frequency turns backups off
	"delete-dns-record",
	"delete-dns-zone",
	"delete-env-variable",
	"delete-registry-tag",
	"delete-service",
	"delete-service-config",
	"delete-service-ingress",
	"delete-service-variable",
	"delete-service-volume",
	"delete-tcp-proxy",
	"delete-token",
	"deploy-service", // replaces the running deployment
	"deploy-static-site",
	"disable-postgres-extension",
	"discard-service-changeset",
	"register-domain", // paid from the wallet
	"remove-email-domain",
	"remove-registry-credential",
	"remove-static-site-domain",
	"remove-workspace-member",
	"renew-domain", // paid from the wallet
	"revoke-private-service-access",
	"set-ingress-source-ranges",
	"set-registry-retention-enabled", // enabling deletes tags on the next run
	"set-service-variables",
	"set-static-site-domain", // can detach every custom domain
	"set-video-playback-domain",
	"undeploy-service",
	"update-dns-record",
	"update-domain", // turns off auto-renewal or the transfer lock
	"update-env-variable",
	"update-member-role",
	"update-postgres-parameters",
	"update-registry-credential",
	"update-service-config",
)

// additiveTools write, but only add or change something without losing what
// was there: creating a resource, staging a change, or renaming.
var additiveTools = toolSet(
	"add-dns-record",
	"add-dns-zone",
	"add-email-domain",
	"add-registry-credential",
	"add-service-config",
	"add-service-ingress",
	"add-service-volume",
	"add-shared-variable",
	"add-static-site-domain",
	"add-tcp-proxy",
	"add-video-captions",
	"add-workspace-member",
	"create-env-variable",
	"create-environment",
	"create-postgres-backup",
	"create-project",
	"create-service",
	"create-static-site-upload",
	"create-token",
	"create-workspace",
	"deploy-template",
	"enable-postgres-extension",
	"fund-workspace", // only returns a payment link; nothing is charged
	"grant-private-service-access",
	"login",
	"redeploy-service",
	"update-environment",
	"update-project",
	"update-service", // stages a changeset; nothing changes until it is approved
	"update-service-volume",
	"update-workspace",
	"upload-video",
)

// openWorldTools reach beyond the caller's own workspace. Every other tool acts
// only on resources the caller's workspace owns.
var openWorldTools = toolSet(
	"quote-domain",
	"search-domains",
)

func toolSet(names ...string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

// annotate sets a tool's title and behaviour hints from its name.
//
// All three hints are set explicitly, false included: ChatGPT's review rejects
// a tool with any of them missing. A tool missing from every list is treated
// as destructive, so a client confirms it rather than running it unasked.
func annotate(t *mcp.Tool) {
	if t.Title == "" {
		t.Title = toolTitle(t.Name)
	}
	readOnly := readOnlyTools[t.Name]
	destructive := !readOnly && !additiveTools[t.Name]
	openWorld := openWorldTools[t.Name]
	t.Annotations = &mcp.ToolAnnotations{
		Title:           t.Title,
		ReadOnlyHint:    readOnly,
		DestructiveHint: &destructive,
		OpenWorldHint:   &openWorld,
	}
}

// titleWords spells out the words a plain capitalisation would get wrong.
var titleWords = map[string]string{
	"dns":      "DNS",
	"env":      "environment",
	"me":       "current user",
	"mysql":    "MySQL",
	"postgres": "Postgres",
	"tcp":      "TCP",
}

// toolTitle turns a tool name into the title clients show a person:
// "add-dns-zone" becomes "Add DNS zone".
func toolTitle(name string) string {
	words := strings.Split(name, "-")
	for i, word := range words {
		if spelled, ok := titleWords[word]; ok {
			words[i] = spelled
		}
	}
	title := strings.Join(words, " ")
	return strings.ToUpper(title[:1]) + title[1:]
}

// RegisterAll registers every tool group on s. local selects the stdio
// transport, where login, the domain purchase tools and upload-video are also
// available; see RegisterAuthTools, RegisterDomainTools and RegisterVideoTools.
func RegisterAll(s *mcp.Server, local bool) {
	RegisterAuthTools(s, local)
	RegisterWorkspaceTools(s)
	RegisterProjectTools(s)
	RegisterEnvironmentTools(s)
	RegisterServiceTools(s)
	RegisterPostgresExtensionTools(s)
	RegisterRegistryRetentionTools(s)
	RegisterRegistryCredentialTools(s)
	RegisterStaticSiteTools(s, local)
	RegisterVideoTools(s, local)
	RegisterEmailTools(s)
	RegisterDomainTools(s, local)
	RegisterDNSTools(s)
	RegisterDeploymentTools(s)
	RegisterTokenTools(s)
	RegisterAlertTools(s)
	RegisterMetricsTools(s)
	RegisterTemplateTools(s)
}
