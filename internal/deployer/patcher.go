package deployer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const patchMarker = "/* claude-relay-patch-begin */"
const patchMarkerEnd = "/* claude-relay-patch-end */"

// extensionGlobs returns glob patterns to find extension.js for a given environment.
func extensionGlobs(home string, remote bool) []string {
	if remote {
		return []string{
			filepath.Join(home, ".vscode-server/extensions/github.copilot-chat-*/dist/extension.js"),
			filepath.Join(home, ".vscode-remote/extensions/github.copilot-chat-*/dist/extension.js"),
		}
	}
	return []string{
		filepath.Join(home, ".vscode-server/extensions/github.copilot-chat-*/dist/extension.js"),
		filepath.Join(home, ".vscode-remote/extensions/github.copilot-chat-*/dist/extension.js"),
		filepath.Join(home, ".vscode/extensions/github.copilot-chat-*/dist/extension.js"),
	}
}

// FindExtensionJS finds the latest extension.js on the local machine.
func FindExtensionJS() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	for _, pattern := range extensionGlobs(home, false) {
		matches, _ := filepath.Glob(pattern)
		if len(matches) > 0 {
			sort.Strings(matches)
			return matches[len(matches)-1], nil
		}
	}
	return "", fmt.Errorf("extension.js not found; ensure GitHub Copilot Chat is installed")
}

// buildPatchSnippet generates the JS injection code.
func buildPatchSnippet(mappings map[string]string) string {
	var pairs []string
	for k, v := range mappings {
		pairs = append(pairs, fmt.Sprintf(`"%s":"%s"`, k, v))
	}
	sort.Strings(pairs) // deterministic output
	return fmt.Sprintf(
		"%s\n;(function(){var m={%s};var _s=JSON.stringify;JSON.stringify=function(o){if(o&&o.model&&m[o.model])o.model=m[o.model];return _s.apply(this,arguments)};})();\n%s",
		patchMarker,
		strings.Join(pairs, ","),
		patchMarkerEnd,
	)
}

// PatchExtension injects model mapping into extension.js.
func PatchExtension(path string, mappings map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read extension.js: %w", err)
	}
	content := string(data)

	// Remove existing patch if present
	content = removePatch(content)

	// Create backup (only if none exists)
	backupPath := path + ".claude-relay-backup"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		if err := os.WriteFile(backupPath, data, 0644); err != nil {
			return fmt.Errorf("create backup: %w", err)
		}
	}

	// Append patch
	content = strings.TrimRight(content, "\n") + "\n" + buildPatchSnippet(mappings) + "\n"
	return os.WriteFile(path, []byte(content), 0644)
}

// RestoreBackup restores extension.js from backup.
func RestoreBackup(path string) error {
	backupPath := path + ".claude-relay-backup"
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("no backup found at %s", backupPath)
	}
	return os.WriteFile(path, data, 0644)
}

// IsPatchApplied checks if the patch marker exists in extension.js.
func IsPatchApplied(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), patchMarker)
}

// HasBackup checks if a backup file exists.
func HasBackup(path string) bool {
	_, err := os.Stat(path + ".claude-relay-backup")
	return err == nil
}

func removePatch(content string) string {
	startIdx := strings.Index(content, patchMarker)
	endIdx := strings.Index(content, patchMarkerEnd)
	if startIdx >= 0 && endIdx >= 0 {
		end := endIdx + len(patchMarkerEnd)
		// Also trim trailing newline after marker
		if end < len(content) && content[end] == '\n' {
			end++
		}
		return content[:startIdx] + content[end:]
	}
	return content
}
