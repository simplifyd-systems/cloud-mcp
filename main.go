// Command cloud-mcp is an MCP server for the Simplifyd Cloud platform.
//
// It exposes tools for managing workspaces, projects, environments, services,
// deployments, and API tokens via the Model Context Protocol over stdio.
//
// Authentication:
//
//	Set SIMPLIFYD_API_TOKEN to a JWT token, or run the "login" tool to
//	authenticate interactively. Tokens are persisted to ~/.simplifyd/config.json.
//
// Custom API URL:
//
//	Set SIMPLIFYD_API_URL to override the default https://api.cloud.simplifyd.com.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simplifyd-com/cloud-mcp/tools"
)

const serverName = "simplifyd-cloud-mcp"

var serverVersion = "0.3.0"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(serverVersion)
		os.Exit(0)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, nil)

	// Register all tool groups
	tools.RegisterAuthTools(server)
	tools.RegisterWorkspaceTools(server)
	tools.RegisterProjectTools(server)
	tools.RegisterEnvironmentTools(server)
	tools.RegisterServiceTools(server)
	tools.RegisterDeploymentTools(server)
	tools.RegisterTokenTools(server)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
