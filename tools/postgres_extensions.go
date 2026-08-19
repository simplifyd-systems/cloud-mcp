package tools

import (
	"context"
	"fmt"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// PostgreSQL extension tools.
//
// The underlying API replaces the whole extension set for a database, which makes
// it easy to drop an extension by omission. These tools deliberately expose only
// enable and disable: an agent acting on "our search needs trigram matching" has
// no reason to restate the extensions it is not touching, and a partial list sent
// to the replacing endpoint would issue DROP EXTENSION against a live database.

type postgresExtensionsArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Service   string `json:"service"   jsonschema:"Managed PostgreSQL service slug"`
	Database  string `json:"database,omitempty" jsonschema:"Database name; defaults to the built-in \"app\" database"`
}

type postgresExtensionArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Service   string `json:"service"   jsonschema:"Managed PostgreSQL service slug"`
	Database  string `json:"database,omitempty" jsonschema:"Database name; defaults to the built-in \"app\" database"`
	Extension string `json:"extension" jsonschema:"Extension name, e.g. pg_trgm. Must be on the platform allowlist returned by list-postgres-extensions"`
}

// defaultPostgresDatabase is the database every managed service is created with.
// It is the one an application connects to unless it declared others, so it is the
// right default for a tool call that omits the database.
const defaultPostgresDatabase = "app"

func databaseOrDefault(name string) string {
	if name == "" {
		return defaultPostgresDatabase
	}
	return name
}

func handleListPostgresExtensions(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args postgresExtensionsArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	extensions, err := services(api, args.Workspace, args.Project, args.Env).ListPostgresExtensions(
		ctx, args.Service, databaseOrDefault(args.Database),
	)
	if err != nil {
		return apiErr("list PostgreSQL extensions", err), nil, nil
	}
	// Installed() drops the "absent" tombstones the platform keeps while it
	// finishes a removal; reporting those as installed would be misleading.
	return jsonText(map[string]any{
		"database":  databaseOrDefault(args.Database),
		"installed": extensions.Installed(),
		"supported": extensions.Supported,
	}), nil, nil
}

func handleEnablePostgresExtension(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args postgresExtensionArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	if args.Extension == "" {
		return text("extension is required"), nil, nil
	}
	database := databaseOrDefault(args.Database)
	extensions, err := services(api, args.Workspace, args.Project, args.Env).EnablePostgresExtension(
		ctx, args.Service, database, args.Extension,
	)
	if err != nil {
		return apiErr("enable PostgreSQL extension", err), nil, nil
	}
	return jsonText(map[string]any{
		"database":  database,
		"enabled":   args.Extension,
		"installed": extensions.Installed(),
	}), nil, nil
}

func handleDisablePostgresExtension(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args postgresExtensionArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	if args.Extension == "" {
		return text("extension is required"), nil, nil
	}
	database := databaseOrDefault(args.Database)
	extensions, err := services(api, args.Workspace, args.Project, args.Env).DisablePostgresExtension(
		ctx, args.Service, database, args.Extension,
	)
	if err != nil {
		return apiErr("disable PostgreSQL extension", err), nil, nil
	}
	return jsonText(map[string]any{
		"database":  database,
		"disabled":  args.Extension,
		"installed": extensions.Installed(),
		"note": fmt.Sprintf(
			"%s was dropped from %s. Any index or column that depended on it is gone with it.",
			args.Extension, database,
		),
	}), nil, nil
}

// RegisterPostgresExtensionTools registers the PostgreSQL extension tools on s.
func RegisterPostgresExtensionTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "list-postgres-extensions",
		Description: "List the PostgreSQL extensions installed in a database on a managed Postgres service, along with the platform allowlist of extensions that may be installed. Defaults to the built-in \"app\" database.",
	}, handleListPostgresExtensions)

	addTool(s, &mcp.Tool{
		Name:        "enable-postgres-extension",
		Description: "Install one PostgreSQL extension in a database (e.g. pg_trgm for typo-tolerant search, pg_stat_statements for query timings). Other installed extensions are left untouched, and enabling one that is already installed does nothing. The extension must be on the platform allowlist; call list-postgres-extensions to see it.",
	}, handleEnablePostgresExtension)

	addTool(s, &mcp.Tool{
		Name:        "disable-postgres-extension",
		Description: "Drop one PostgreSQL extension from a database, leaving the others in place. This is destructive: DROP EXTENSION also removes any index, column or other object that depends on the extension, and fails if the platform cannot drop it cleanly. Confirm with the user before calling.",
	}, handleDisablePostgresExtension)
}
