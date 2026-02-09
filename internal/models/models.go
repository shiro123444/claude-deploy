package models

type Config struct {
	APIKey        string         `json:"api_key"`
	BaseURL       string         `json:"base_url"`
	ModelMappings []ModelMapping `json:"model_mappings"`
	DefaultOpus   string         `json:"default_opus_model"`
	DefaultSonnet string         `json:"default_sonnet_model"`
	DefaultHaiku  string         `json:"default_haiku_model"`
	MCPServers    []MCPServer    `json:"mcp_servers"`
	Targets       []Target       `json:"targets"`
	AutoDetect    bool           `json:"auto_detect"`
}

type ModelMapping struct {
	VSCodeID string `json:"vscode_id"`
	RelayID  string `json:"relay_id"`
}

type Target struct {
	Name string     `json:"name"`
	Type TargetType `json:"type"`
	Host string     `json:"host,omitempty"`
}

type TargetType string

const (
	TargetLocal     TargetType = "local"
	TargetSSH       TargetType = "ssh"
	TargetCodespace TargetType = "codespace"
)

type MCPServer struct {
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type DeployStatus struct {
	Target          string `json:"target"`
	Patched         bool   `json:"patched"`
	BackupExists    bool   `json:"backup_exists"`
	ConfigExists    bool   `json:"config_exists"`
	ExtPath         string `json:"ext_path"`
	CLIPath         string `json:"cli_path,omitempty"`
	CLIPatched      bool   `json:"cli_patched"`
	CLIBackupExists bool   `json:"cli_backup_exists"`
}

type RelayModel struct {
	ID string `json:"id"`
}
