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
	// 1. Build mapping table
	mappings := make(map[string]string)
	for _, m := range cfg.ModelMappings {
		mappings[m.VSCodeID] = m.RelayID
	}

	// 2. Restore extension.js if previously patched (cleanup legacy patches)
	//    NOTE: We NO LONGER patch extension.js because:
	//    - extension.js handles ALL Copilot models (including native claude-opus-4.6, etc.)
	//    - Patching JSON.stringify in extension.js affects local Copilot models too
	//    - This causes API version mismatch errors for native GitHub Copilot models
	//    - cli.js is Claude Agent-specific and is the only file that needs patching
	extPath, _ := FindExtensionJS()
	if extPath != "" && IsPatchApplied(extPath) && HasBackup(extPath) {
		// Restore from backup to remove the legacy patch
		_ = RestoreBackup(extPath)
	}

	// 3. Patch cli.js (handles actual API calls in Claude Agent mode)
	//    CRITICAL: cli.js is the file that ACTUALLY makes HTTP requests for Claude Agent.
	//    This is the ONLY file that should be patched - it's isolated to Claude Agent.
	cliPath, err := FindCLIJS()
	if err != nil {
		return fmt.Errorf("find cli.js: %w", err)
	}
	if err := PatchCLI(cliPath, mappings); err != nil {
		return fmt.Errorf("patch cli.js: %w", err)
	}

	// 4. Write claude settings
	if err := WriteClaudeSettings(cfg); err != nil {
		return fmt.Errorf("write claude settings: %w", err)
	}

	// 5. Write VSCode settings (MCP)
	if err := WriteVSCodeSettings(models.TargetLocal, cfg.MCPServers); err != nil {
		return fmt.Errorf("write vscode settings: %w", err)
	}

	return nil
}

func statusLocal(target models.Target) (*models.DeployStatus, error) {
	status := &models.DeployStatus{Target: target.Name}

	// Check extension.js for legacy patch status (we no longer patch it)
	extPath, err := FindExtensionJS()
	if err != nil {
		status.ExtPath = "not found"
	} else {
		status.ExtPath = extPath
		// Note: Patched=true here means legacy patch exists and should be cleaned up
		status.Patched = IsPatchApplied(extPath)
		status.BackupExists = HasBackup(extPath)
	}
	status.ConfigExists = ClaudeSettingsExist()

	// Check cli.js patch status (this is the only file we actively patch)
	cliPath, cliErr := FindCLIJS()
	if cliErr == nil {
		status.CLIPath = cliPath
		status.CLIPatched = IsCLIPatchApplied(cliPath)
		status.CLIBackupExists = HasCLIBackup(cliPath)
	}

	return status, nil
}

func restoreLocal() error {
	// Restore extension.js if backup exists (legacy cleanup)
	extPath, _ := FindExtensionJS()
	if extPath != "" && HasBackup(extPath) {
		if err := RestoreBackup(extPath); err != nil {
			return fmt.Errorf("restore extension.js: %w", err)
		}
	}

	// Restore cli.js (this is the main patch we need to restore)
	cliPath, err := FindCLIJS()
	if err != nil {
		return fmt.Errorf("find cli.js: %w", err)
	}
	if err := RestoreCLIBackup(cliPath); err != nil {
		return fmt.Errorf("restore cli.js: %w", err)
	}

	return nil
}
