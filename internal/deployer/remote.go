package deployer

import (
	"bytes"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"claude-relay/internal/models"
)

// remoteExec runs a command on the remote target and returns stdout.
func remoteExec(target models.Target, command string) (string, error) {
	var cmd *exec.Cmd

	switch target.Type {
	case models.TargetSSH:
		cmd = exec.Command("ssh", target.Host, command)
	case models.TargetCodespace:
		cmd = exec.Command("gh", "codespace", "ssh", "-c", target.Host, "--", command)
	default:
		return "", fmt.Errorf("unsupported remote type: %s", target.Type)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return "", fmt.Errorf("%s", errMsg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// deployRemote handles deployment to SSH or Codespace targets.
func deployRemote(target models.Target, cfg *models.Config) error {
	// 1. Build mappings
	mappings := make(map[string]string)
	for _, m := range cfg.ModelMappings {
		mappings[m.VSCodeID] = m.RelayID
	}

	// Build JS pairs
	var pairs []string
	for k, v := range mappings {
		pairs = append(pairs, fmt.Sprintf(`"%s":"%s"`, k, v))
	}
	sort.Strings(pairs)
	jsMap := strings.Join(pairs, ",")

	// 2. Find extension.js and restore if patched (cleanup legacy patches)
	//    NOTE: We NO LONGER patch extension.js because it affects ALL Copilot models
	findCmd := `ls -t ~/.vscode-server/extensions/github.copilot-chat-*/dist/extension.js 2>/dev/null | head -1 || ` +
		`ls -t ~/.vscode-remote/extensions/github.copilot-chat-*/dist/extension.js 2>/dev/null | head -1`
	extPath, _ := remoteExec(target, findCmd)
	if extPath != "" {
		backupPath := extPath + ".claude-relay-backup"
		// Check if patched and backup exists, then restore
		checkCmd := fmt.Sprintf("grep -q 'claude-relay-patch-begin' '%s' && test -f '%s' && cp '%s' '%s' && echo restored || echo skip", extPath, backupPath, backupPath, extPath)
		remoteExec(target, checkCmd)
	}

	// 3. Find and patch cli.js on remote
	//    CRITICAL: cli.js is the ONLY file that should be patched.
	//    It handles actual API calls in Agent mode and is isolated to Claude Agent.
	findCLICmd := `ls -t ~/.vscode-server/extensions/github.copilot-chat-*/dist/cli.js 2>/dev/null | head -1 || ` +
		`ls -t ~/.vscode-remote/extensions/github.copilot-chat-*/dist/cli.js 2>/dev/null | head -1`
	cliPath, cliErr := remoteExec(target, findCLICmd)
	if cliErr != nil || cliPath == "" {
		return fmt.Errorf("cli.js not found on %s: %v", target.Name, cliErr)
	}

	// Create backup (only if none exists)
	cliBackup := cliPath + ".claude-relay-backup"
	remoteExec(target, fmt.Sprintf("test -f '%s' || cp '%s' '%s'", cliBackup, cliPath, cliBackup))

	// Build globalThis model map for cli.js
	cliMapJS := fmt.Sprintf(
		`globalThis.__cliModelMap={%s};globalThis.__cliMap=function(m){return(globalThis.__cliModelMap[m]||m)};`,
		jsMap,
	)

	// Inject model map at file header (before first import)
	// Then patch known function patterns
	cliPatchCmd := fmt.Sprintf(`python3 -c "
import sys
with open('%s','r') as f: c=f.read()
marker='/* claude-relay-cli-patch */'
# Remove old patch
if marker in c:
    idx=c.index(marker)
    imp=c.index('import{',idx) if 'import{' in c[idx:] else c.index('import ',idx)
    c=c[:idx]+c[imp:]
# Inject new patch
c=c.replace('import{createRequire',marker+'%s'+'import{createRequire',1)
# Patch Gu function
c=c.replace('function Gu(A){return A.replace(/\\\[(1|2)m\\\]/gi,\"\")}','function Gu(A){return globalThis.__cliMap(A.replace(/\\\[(1|2)m\\\]/gi,\"\"))}',1)
# Patch nH function
c=c.replace('async function nH({apiKey:A,maxRetries:Q,model:B,fetchOverride:G}){let Z=','async function nH({apiKey:A,maxRetries:Q,model:B,fetchOverride:G}){B=globalThis.__cliMap(B||\"\")\;let Z=',1)
with open('%s','w') as f: f.write(c)
print('cli.js patched')
" 2>&1`, cliPath, cliMapJS, cliPath)

	if _, err := remoteExec(target, cliPatchCmd); err != nil {
		return fmt.Errorf("cli.js patch failed: %w", err)
	}

	// 4. Write claude settings remotely
	settingsJSON, err := GenerateClaudeSettingsJSON(cfg)
	if err != nil {
		return fmt.Errorf("generate settings: %w", err)
	}
	writeCmd := fmt.Sprintf("mkdir -p ~/.claude && cat > ~/.claude/settings.json << 'EOFCLAUDE'\n%s\nEOFCLAUDE", string(settingsJSON))
	if _, err := remoteExec(target, writeCmd); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}

	return nil
}

// statusRemote checks deployment status on a remote target.
func statusRemote(target models.Target) (*models.DeployStatus, error) {
	status := &models.DeployStatus{Target: target.Name}

	// Check extension.js for legacy patch status
	findCmd := `ls -t ~/.vscode-server/extensions/github.copilot-chat-*/dist/extension.js 2>/dev/null | head -1 || ` +
		`ls -t ~/.vscode-remote/extensions/github.copilot-chat-*/dist/extension.js 2>/dev/null | head -1`
	extPath, _ := remoteExec(target, findCmd)
	if extPath == "" {
		status.ExtPath = "not found"
	} else {
		status.ExtPath = extPath
		// Note: Patched=true here means legacy patch exists and should be cleaned up
		out, _ := remoteExec(target, fmt.Sprintf("grep -c 'claude-relay-patch-begin' '%s' 2>/dev/null || echo 0", extPath))
		status.Patched = out != "0"
		out, _ = remoteExec(target, fmt.Sprintf("test -f '%s.claude-relay-backup' && echo yes || echo no", extPath))
		status.BackupExists = out == "yes"
	}

	// Check cli.js patch status (this is the main patch)
	findCLICmd := `ls -t ~/.vscode-server/extensions/github.copilot-chat-*/dist/cli.js 2>/dev/null | head -1 || ` +
		`ls -t ~/.vscode-remote/extensions/github.copilot-chat-*/dist/cli.js 2>/dev/null | head -1`
	cliPath, _ := remoteExec(target, findCLICmd)
	if cliPath != "" {
		status.CLIPath = cliPath
		out, _ := remoteExec(target, fmt.Sprintf("grep -c 'claude-relay-cli-patch' '%s' 2>/dev/null || echo 0", cliPath))
		status.CLIPatched = out != "0"
		out, _ = remoteExec(target, fmt.Sprintf("test -f '%s.claude-relay-backup' && echo yes || echo no", cliPath))
		status.CLIBackupExists = out == "yes"
	}

	// Check config
	out, _ := remoteExec(target, "test -f ~/.claude/settings.json && echo yes || echo no")
	status.ConfigExists = out == "yes"

	return status, nil
}

// restoreRemote restores backups on a remote target.
func restoreRemote(target models.Target) error {
	// Restore extension.js if backup exists (legacy cleanup)
	findExtCmd := `ls -t ~/.vscode-server/extensions/github.copilot-chat-*/dist/extension.js 2>/dev/null | head -1 || ` +
		`ls -t ~/.vscode-remote/extensions/github.copilot-chat-*/dist/extension.js 2>/dev/null | head -1`
	extPath, _ := remoteExec(target, findExtCmd)
	if extPath != "" {
		backupPath := extPath + ".claude-relay-backup"
		remoteExec(target, fmt.Sprintf("test -f '%s' && cp '%s' '%s'", backupPath, backupPath, extPath))
	}

	// Restore cli.js (this is the main patch)
	findCLICmd := `ls -t ~/.vscode-server/extensions/github.copilot-chat-*/dist/cli.js 2>/dev/null | head -1 || ` +
		`ls -t ~/.vscode-remote/extensions/github.copilot-chat-*/dist/cli.js 2>/dev/null | head -1`
	cliPath, err := remoteExec(target, findCLICmd)
	if err != nil || cliPath == "" {
		return fmt.Errorf("cli.js not found on %s", target.Name)
	}

	cliBackup := cliPath + ".claude-relay-backup"
	if _, err := remoteExec(target, fmt.Sprintf("test -f '%s' && cp '%s' '%s'", cliBackup, cliBackup, cliPath)); err != nil {
		return fmt.Errorf("restore cli.js failed: %w", err)
	}

	return nil
}
