package deployer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"claude-relay/internal/models"
)

// WriteClaudeSettings writes/updates ~/.claude/settings.json.
func WriteClaudeSettings(cfg *models.Config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".claude", "settings.json")

	// Read existing to preserve other keys
	existing := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &existing)
	}

	// Build env block
	env := map[string]string{
		"ANTHROPIC_BASE_URL":             cfg.BaseURL,
		"ANTHROPIC_API_KEY":              cfg.APIKey,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   cfg.DefaultOpus,
		"ANTHROPIC_DEFAULT_SONNET_MODEL": cfg.DefaultSonnet,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  cfg.DefaultHaiku,
		"ANTHROPIC_SMALL_FAST_MODEL":     cfg.DefaultHaiku,
		"API_TIMEOUT_MS":                 "3000000",
	}
	existing["env"] = env

	// Build mcpServers block
	mcpServers := make(map[string]any)
	for _, s := range cfg.MCPServers {
		if !s.Enabled {
			continue
		}
		mcpServers[s.Name] = map[string]any{
			"command": s.Command,
			"args":    s.Args,
		}
	}
	if len(mcpServers) > 0 {
		existing["mcpServers"] = mcpServers
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// WriteVSCodeSettings writes MCP config to VSCode's settings.json.
func WriteVSCodeSettings(targetType models.TargetType, mcpServers []models.MCPServer) error {
	path := vscodeSettingsPath(targetType)

	existing := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &existing)
	}

	// MCP feature flag
	existing["github.copilot.chat.cli.mcp.enabled"] = true

	// Build MCP servers
	servers := make(map[string]any)
	for _, s := range mcpServers {
		if !s.Enabled {
			continue
		}
		servers[s.Name] = map[string]any{
			"command": s.Command,
			"args":    s.Args,
		}
	}
	if len(servers) > 0 {
		if _, ok := existing["mcp"]; !ok {
			existing["mcp"] = map[string]any{}
		}
		mcpBlock, ok := existing["mcp"].(map[string]any)
		if !ok {
			mcpBlock = map[string]any{}
		}
		mcpBlock["servers"] = servers
		existing["mcp"] = mcpBlock
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(existing, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// ClaudeSettingsExist checks if ~/.claude/settings.json exists.
func ClaudeSettingsExist() bool {
	home, _ := os.UserHomeDir()
	_, err := os.Stat(filepath.Join(home, ".claude", "settings.json"))
	return err == nil
}

func vscodeSettingsPath(t models.TargetType) string {
	home, _ := os.UserHomeDir()
	switch t {
	case models.TargetSSH:
		return filepath.Join(home, ".vscode-server", "data", "Machine", "settings.json")
	case models.TargetCodespace:
		return filepath.Join(home, ".vscode-remote", "data", "Machine", "settings.json")
	default:
		// Try vscode-server first (common on remote), fall back to local
		srvPath := filepath.Join(home, ".vscode-server", "data", "Machine", "settings.json")
		if _, err := os.Stat(filepath.Dir(srvPath)); err == nil {
			return srvPath
		}
		return filepath.Join(home, ".config", "Code", "User", "settings.json")
	}
}

// GenerateClaudeSettingsJSON returns the JSON bytes for remote deployment.
func GenerateClaudeSettingsJSON(cfg *models.Config) ([]byte, error) {
	settings := map[string]any{
		"env": map[string]string{
			"ANTHROPIC_BASE_URL":             cfg.BaseURL,
			"ANTHROPIC_API_KEY":              cfg.APIKey,
			"ANTHROPIC_DEFAULT_OPUS_MODEL":   cfg.DefaultOpus,
			"ANTHROPIC_DEFAULT_SONNET_MODEL": cfg.DefaultSonnet,
			"ANTHROPIC_DEFAULT_HAIKU_MODEL":  cfg.DefaultHaiku,
			"ANTHROPIC_SMALL_FAST_MODEL":     cfg.DefaultHaiku,
			"API_TIMEOUT_MS":                 "3000000",
		},
	}

	mcpServers := make(map[string]any)
	for _, s := range cfg.MCPServers {
		if !s.Enabled {
			continue
		}
		mcpServers[s.Name] = map[string]any{
			"command": s.Command,
			"args":    s.Args,
		}
	}
	if len(mcpServers) > 0 {
		settings["mcpServers"] = mcpServers
	}

	return json.MarshalIndent(settings, "", "  ")
}

// GenerateVSCodeSettingsJSON returns the MCP portion for VSCode settings.
func GenerateVSCodeSettingsJSON(mcpServers []models.MCPServer) (string, error) {
	servers := make(map[string]any)
	for _, s := range mcpServers {
		if !s.Enabled {
			continue
		}
		servers[s.Name] = map[string]any{
			"command": s.Command,
			"args":    s.Args,
		}
	}

	data := map[string]any{
		"github.copilot.chat.cli.mcp.enabled": true,
		"mcp": map[string]any{
			"servers": servers,
		},
	}

	b, err := json.MarshalIndent(data, "", "    ")
	if err != nil {
		return "", fmt.Errorf("generate vscode settings: %w", err)
	}
	return string(b), nil
}
