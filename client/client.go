// Package client manages authentication state for the Simplifyd Cloud MCP
// server: token resolution (env var or config file) and persistence of the
// login session to ~/.simplifyd/config.json. API calls go through the
// cloud-go-sdk.
package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultAPIURL = "https://api.cloud.simplifyd.com"
	configDir     = ".simplifyd"
	configFile    = "config.json"
)

// Config holds persisted authentication state.
type Config struct {
	Token     string    `json:"token"`
	ActiveEnv ActiveEnv `json:"active_env,omitempty"`
}

// ActiveEnv holds the currently active workspace/project/env context.
type ActiveEnv struct {
	Workspace string `json:"workspace,omitempty"`
	Project   string `json:"project,omitempty"`
	Env       string `json:"env,omitempty"`
}

// BaseURL returns the API base URL, honouring the SIMPLIFYD_API_URL override.
func BaseURL() string {
	if url := os.Getenv("SIMPLIFYD_API_URL"); url != "" {
		return url
	}
	return DefaultAPIURL
}

// ResolveToken returns the API token from the SIMPLIFYD_API_TOKEN environment
// variable, falling back to the config file at ~/.simplifyd/config.json.
// Returns "" when no token is available.
func ResolveToken() string {
	if token := os.Getenv("SIMPLIFYD_API_TOKEN"); token != "" {
		return token
	}
	if cfg, err := LoadConfig(); err == nil {
		return cfg.Token
	}
	return ""
}

// LoadConfig reads the config file from ~/.simplifyd/config.json.
func LoadConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("could not find home directory: %w", err)
	}
	path := filepath.Join(home, configDir, configFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config file: %w", err)
	}
	return &cfg, nil
}

// SaveConfig writes the config to ~/.simplifyd/config.json.
func SaveConfig(cfg *Config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not find home directory: %w", err)
	}
	dir := filepath.Join(home, configDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("could not create config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, configFile), data, 0600)
}
