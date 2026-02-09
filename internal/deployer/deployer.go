package deployer

import (
	"fmt"

	"claude-relay/internal/models"
)

// Deploy executes a full deployment to the given target.
func Deploy(target models.Target, cfg *models.Config) error {
	if target.Type == models.TargetLocal {
		return deployLocal(cfg)
	}
	return deployRemote(target, cfg)
}

// Status checks the deployment status of a target.
func Status(target models.Target) (*models.DeployStatus, error) {
	if target.Type == models.TargetLocal {
		return statusLocal(target)
	}
	return statusRemote(target)
}

// Restore restores the extension.js backup on a target.
func Restore(target models.Target) error {
	if target.Type == models.TargetLocal {
		return restoreLocal()
	}
	return restoreRemote(target)
}

// --- Local operations ---

func deployLocal(cfg *models.Config) error {
	// 1. Find extension.js
	extPath, err := FindExtensionJS()
	if err != nil {
		return fmt.Errorf("find extension: %w", err)
	}

	// 2. Build mapping table
	mappings := make(map[string]string)
	for _, m := range cfg.ModelMappings {
		mappings[m.VSCodeID] = m.RelayID
	}

	// 3. Patch extension.js (handles VS Code UI panel)
	if err := PatchExtension(extPath, mappings); err != nil {
		return fmt.Errorf("patch extension: %w", err)
	}

	// 4. Patch cli.js (handles actual API calls in Claude Agent mode)
	//    CRITICAL: cli.js is the file that ACTUALLY makes HTTP requests.
	//    Without this patch, Agent mode sends unmapped model IDs to the relay.
	cliPath, err := FindCLIJS()
	if err != nil {
		return fmt.Errorf("find cli.js: %w", err)
	}
	if err := PatchCLI(cliPath, mappings); err != nil {
		return fmt.Errorf("patch cli.js: %w", err)
	}

	// 5. Write claude settings
	if err := WriteClaudeSettings(cfg); err != nil {
		return fmt.Errorf("write claude settings: %w", err)
	}

	// 6. Write VSCode settings (MCP)
	if err := WriteVSCodeSettings(models.TargetLocal, cfg.MCPServers); err != nil {
		return fmt.Errorf("write vscode settings: %w", err)
	}

	return nil
}

func statusLocal(target models.Target) (*models.DeployStatus, error) {
	status := &models.DeployStatus{Target: target.Name}

	extPath, err := FindExtensionJS()
	if err != nil {
		status.ExtPath = "not found"
		return status, nil
	}

	status.ExtPath = extPath
	status.Patched = IsPatchApplied(extPath)
	status.BackupExists = HasBackup(extPath)
	status.ConfigExists = ClaudeSettingsExist()

	// Also check cli.js patch status
	cliPath, cliErr := FindCLIJS()
	if cliErr == nil {
		status.CLIPath = cliPath
		status.CLIPatched = IsCLIPatchApplied(cliPath)
		status.CLIBackupExists = HasCLIBackup(cliPath)
	}

	return status, nil
}

func restoreLocal() error {
	// Restore extension.js
	extPath, err := FindExtensionJS()
	if err != nil {
		return fmt.Errorf("find extension: %w", err)
	}
	if err := RestoreBackup(extPath); err != nil {
		return fmt.Errorf("restore extension.js: %w", err)
	}

	// Restore cli.js
	cliPath, err := FindCLIJS()
	if err != nil {
		return fmt.Errorf("find cli.js: %w", err)
	}
	if err := RestoreCLIBackup(cliPath); err != nil {
		return fmt.Errorf("restore cli.js: %w", err)
	}

	return nil
}
