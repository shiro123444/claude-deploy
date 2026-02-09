package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"claude-relay/internal/models"
)

var (
	mu         sync.RWMutex
	configPath string
)

func Init() {
	home, _ := os.UserHomeDir()
	configPath = filepath.Join(home, ".claude-relay", "config.json")
}

func Load() (*models.Config, error) {
	mu.RLock()
	defer mu.RUnlock()

	data, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return defaultConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	var cfg models.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultConfig(), nil
	}
	return &cfg, nil
}

func Save(cfg *models.Config) error {
	mu.Lock()
	defer mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0600)
}

func defaultConfig() *models.Config {
	return &models.Config{
		BaseURL:    "https://api.anthropic.com",
		AutoDetect: true,
		ModelMappings: []models.ModelMapping{
			{VSCodeID: "claude-opus-4.6", RelayID: "claude-opus-4-6"},
			{VSCodeID: "claude-sonnet-4.5", RelayID: "claude-sonnet-4-5-20250929"},
			{VSCodeID: "claude-haiku-4.5", RelayID: "claude-haiku-4-5-20251001"},
		},
		DefaultOpus:   "claude-opus-4-6",
		DefaultSonnet: "claude-sonnet-4-5-20250929",
		DefaultHaiku:  "claude-haiku-4-5-20251001",
		MCPServers: []models.MCPServer{
			{Name: "fetch", Enabled: true, Command: "uvx", Args: []string{"mcp-server-fetch"}},
			{Name: "deepwiki", Enabled: true, Command: "npx", Args: []string{"-y", "mcp-deepwiki@latest"}},
		},
		Targets: []models.Target{
			{Name: "local", Type: models.TargetLocal},
		},
	}
}
