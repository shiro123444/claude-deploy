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
	// 1. Find extension.js on remote
	findCmd := `ls -t ~/.vscode-server/extensions/github.copilot-chat-*/dist/extension.js 2>/dev/null | head -1 || ` +
		`ls -t ~/.vscode-remote/extensions/github.copilot-chat-*/dist/extension.js 2>/dev/null | head -1`
	extPath, err := remoteExec(target, findCmd)
	if err != nil || extPath == "" {
		return fmt.Errorf("extension.js not found on %s: %v", target.Name, err)
	}

	// 2. Create backup (only if none exists)
	backupPath := extPath + ".claude-relay-backup"
	remoteExec(target, fmt.Sprintf("test -f '%s' || cp '%s' '%s'", backupPath, extPath, backupPath))

	// 3. Remove old patch and append new one
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

	// Use sed to remove old patch, then append new one via heredoc
	patchCmd := fmt.Sprintf(
		`sed -i '/%s/,/%s/d' '%s' && cat >> '%s' << 'EOFPATCH'
%s
;(function(){var m={%s};var _s=JSON.stringify;JSON.stringify=function(o){if(o&&o.model&&m[o.model])o.model=m[o.model];return _s.apply(this,arguments)};})();
%s
EOFPATCH`,
		strings.ReplaceAll(patchMarker, "*", "\\*"),
		strings.ReplaceAll(patchMarkerEnd, "*", "\\*"),
		extPath,
		extPath,
		patchMarker,
		jsMap,
		patchMarkerEnd,
	)
	if _, err := remoteExec(target, patchCmd); err != nil {
		return fmt.Errorf("patch failed: %w", err)
	}

	// 5. Find and patch cli.js on remote
	//    CRITICAL: cli.js is the file that ACTUALLY makes API calls in Agent mode.
	//    Without this, Agent mode sends unmapped model IDs to the relay.
	findCLICmd := `ls -t ~/.vscode-server/extensions/github.copilot-chat-*/dist/cli.js 2>/dev/null | head -1 || ` +
		`ls -t ~/.vscode-remote/extensions/github.copilot-chat-*/dist/cli.js 2>/dev/null | head -1`
	cliPath, cliErr := remoteExec(target, findCLICmd)
	if cliErr == nil && cliPath != "" {
		// Create backup
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
			// cli.js patch failure is non-fatal but should be reported
			fmt.Printf("Warning: cli.js patch failed on %s: %v\n", target.Name, err)
		}
	}

	// 6. Write claude settings remotely
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

	// Find extension
	findCmd := `ls -t ~/.vscode-server/extensions/github.copilot-chat-*/dist/extension.js 2>/dev/null | head -1 || ` +
		`ls -t ~/.vscode-remote/extensions/github.copilot-chat-*/dist/extension.js 2>/dev/null | head -1`
	extPath, _ := remoteExec(target, findCmd)
	if extPath == "" {
		status.ExtPath = "not found"
		return status, nil
	}
	status.ExtPath = extPath

	// Check patch
	out, _ := remoteExec(target, fmt.Sprintf("grep -c 'claude-relay-patch-begin' '%s' 2>/dev/null || echo 0", extPath))
	status.Patched = out != "0"

	// Check backup
	out, _ = remoteExec(target, fmt.Sprintf("test -f '%s.claude-relay-backup' && echo yes || echo no", extPath))
	status.BackupExists = out == "yes"

	// Check config
	out, _ = remoteExec(target, "test -f ~/.claude/settings.json && echo yes || echo no")
	status.ConfigExists = out == "yes"

	return status, nil
}

// restoreRemote restores extension.js backup on a remote target.
func restoreRemote(target models.Target) error {
	findCmd := `ls -t ~/.vscode-server/extensions/github.copilot-chat-*/dist/extension.js 2>/dev/null | head -1 || ` +
		`ls -t ~/.vscode-remote/extensions/github.copilot-chat-*/dist/extension.js 2>/dev/null | head -1`
	extPath, err := remoteExec(target, findCmd)
	if err != nil || extPath == "" {
		return fmt.Errorf("extension.js not found on %s", target.Name)
	}

	backupPath := extPath + ".claude-relay-backup"
	if _, err := remoteExec(target, fmt.Sprintf("test -f '%s' && cp '%s' '%s'", backupPath, backupPath, extPath)); err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}
	return nil
}
