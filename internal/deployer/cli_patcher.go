package deployer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const cliPatchMarker = "/* claude-relay-cli-patch */"

// cliGlobs returns glob patterns to find cli.js.
func cliGlobs(home string, remote bool) []string {
	if remote {
		return []string{
			filepath.Join(home, ".vscode-server/extensions/github.copilot-chat-*/dist/cli.js"),
			filepath.Join(home, ".vscode-remote/extensions/github.copilot-chat-*/dist/cli.js"),
		}
	}
	return []string{
		filepath.Join(home, ".vscode-server/extensions/github.copilot-chat-*/dist/cli.js"),
		filepath.Join(home, ".vscode-remote/extensions/github.copilot-chat-*/dist/cli.js"),
		filepath.Join(home, ".vscode/extensions/github.copilot-chat-*/dist/cli.js"),
	}
}

// FindCLIJS finds the latest cli.js on the local machine.
func FindCLIJS() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	for _, pattern := range cliGlobs(home, false) {
		matches, _ := filepath.Glob(pattern)
		if len(matches) > 0 {
			sort.Strings(matches)
			return matches[len(matches)-1], nil
		}
	}
	return "", fmt.Errorf("cli.js not found; ensure GitHub Copilot Chat is installed")
}

// buildCLIModelMap generates the globalThis model map JS snippet.
func buildCLIModelMap(mappings map[string]string) string {
	var pairs []string
	for k, v := range mappings {
		pairs = append(pairs, fmt.Sprintf(`"%s":"%s"`, k, v))
	}
	sort.Strings(pairs) // deterministic output
	return fmt.Sprintf(
		`globalThis.__cliModelMap={%s};globalThis.__cliMap=function(m){return(globalThis.__cliModelMap[m]||m)};`,
		strings.Join(pairs, ","),
	)
}

// cliPatchPoints defines the find→replace rules for cli.js.
// Each entry: [0]=old string, [1]=new string.
// IMPORTANT: These patterns are based on the minified cli.js from copilot-chat-0.37.x.
// The function names (g81, Gu, nH) may change between versions.
// If patches fail to match, the function signatures need to be re-discovered.
// See ARCHITECTURE.md for the discovery methodology.
type cliPatchPoint struct {
	Name    string
	Old     string
	New     string
	Comment string
}

// discoverCLIPatchPoints returns patch points by searching the actual cli.js content.
// This makes the patcher more resilient to minifier name changes between versions.
func discoverCLIPatchPoints(content string) []cliPatchPoint {
	var points []cliPatchPoint

	// Patch 1: Main conversation streaming generator
	// Pattern: "async function*XXX(A,Q,B){let G=YYY(B)" where XXX is the function name
	// This is the core function that sends messages to the Anthropic API.
	// We inject B.model=globalThis.__cliMap(B.model) to remap the model before the request.
	if idx := strings.Index(content, "async function*"); idx >= 0 {
		// Search for the streaming generator pattern with ba8 call
		searchFrom := 0
		for {
			idx = strings.Index(content[searchFrom:], "async function*")
			if idx < 0 {
				break
			}
			idx += searchFrom
			// Extract enough context to identify the pattern
			end := idx + 200
			if end > len(content) {
				end = len(content)
			}
			snippet := content[idx:end]
			// Look for the pattern: async function*XXX(A,Q,B){let G=YYY(B)
			// where it has model:B.model nearby (this is the conversation function)
			if strings.Contains(snippet, "(A,Q,B){") &&
				strings.Contains(snippet, "model:B.model") {
				// Find the exact old string
				braceIdx := strings.Index(snippet, "{")
				if braceIdx > 0 {
					// Find "let G=" after the brace
					letIdx := strings.Index(snippet[braceIdx:], "let G=")
					if letIdx > 0 {
						old := snippet[:braceIdx+letIdx+len("let G=")]
						// Extract until we find the opening paren of the call
						callEnd := strings.Index(snippet[braceIdx+letIdx:], "(B)")
						if callEnd > 0 {
							old = snippet[:braceIdx+letIdx+callEnd+3]
							new := snippet[:braceIdx+1] + "B.model=globalThis.__cliMap(B.model);" + snippet[braceIdx+1:braceIdx+letIdx+callEnd+3]
							points = append(points, cliPatchPoint{
								Name:    "streaming-generator",
								Old:     old,
								New:     new,
								Comment: "Map model ID at entry of main conversation streaming function",
							})
						}
					}
				}
				break
			}
			searchFrom = idx + 15
		}
	}

	// Patch 2: ANSI strip function used as model name
	// Pattern: function Gu(A){return A.replace(/\[(1|2)m\]/gi,"")}
	// This function strips ANSI codes and is used in model:Gu(X) calls.
	// We wrap the return value with globalThis.__cliMap().
	ansiPattern := `function Gu(A){return A.replace(/\[(1|2)m\]/gi,"")}`
	if strings.Contains(content, ansiPattern) {
		points = append(points, cliPatchPoint{
			Name:    "ansi-strip-model",
			Old:     ansiPattern,
			New:     `function Gu(A){return globalThis.__cliMap(A.replace(/\[(1|2)m\]/gi,""))}`,
			Comment: "Wrap ANSI strip function return with model mapping (used as model:Gu(X))",
		})
	}

	// Patch 3: Anthropic client factory
	// Pattern: async function nH({apiKey:A,maxRetries:Q,model:B,fetchOverride:G}){let Z=
	// This creates the Anthropic SDK client. We inject B=globalThis.__cliMap(B||"") at entry.
	clientPattern := `async function nH({apiKey:A,maxRetries:Q,model:B,fetchOverride:G}){let Z=`
	if strings.Contains(content, clientPattern) {
		points = append(points, cliPatchPoint{
			Name:    "client-factory",
			Old:     clientPattern,
			New:     `async function nH({apiKey:A,maxRetries:Q,model:B,fetchOverride:G}){B=globalThis.__cliMap(B||"");let Z=`,
			Comment: "Map model ID at entry of Anthropic SDK client factory",
		})
	}

	return points
}

// PatchCLI injects model mapping into cli.js.
//
// CRITICAL ARCHITECTURAL NOTE:
// cli.js is the file that ACTUALLY makes API calls in Claude Agent mode.
// extension.js only handles the VS Code UI panel.
// If you only patch extension.js, the Agent will still send unmapped model IDs,
// causing requests to fail or time out on third-party relays.
//
// KEY DIFFERENCES from extension.js patching:
//  1. cli.js is an ES Module (uses import), not a CommonJS bundle
//  2. "use strict" appears INSIDE a Function() constructor string, NOT at file top
//     → injecting at "use strict" puts code in wrong scope → "XXX is not defined" errors
//  3. Must use globalThis.* to ensure variables are accessible across all scopes
//  4. Injection point is BEFORE the first import statement at file top level
func PatchCLI(path string, mappings map[string]string) error {
	backupPath := path + ".claude-relay-backup"

	// If a backup exists, always restore from the clean backup first.
	// removeCLIPatch() only strips the header injection but NOT the function-level
	// patches (e.g., globalThis.__cliMap wrappers inside Gu, g81, nH).
	// Re-deploying on an already-patched file causes discoverCLIPatchPoints() to
	// fail because the original function signatures no longer match.
	var data []byte
	var err error
	if _, statErr := os.Stat(backupPath); statErr == nil {
		data, err = os.ReadFile(backupPath)
		if err != nil {
			return fmt.Errorf("read cli.js backup: %w", err)
		}
	} else {
		data, err = os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read cli.js: %w", err)
		}
		// Create backup from the original clean file
		if err := os.WriteFile(backupPath, data, 0644); err != nil {
			return fmt.Errorf("create cli.js backup: %w", err)
		}
	}
	content := string(data)

	// Build the model map JS
	modelMapJS := cliPatchMarker + buildCLIModelMap(mappings)

	// Inject at file header, before the first import statement
	// cli.js starts with: #!/usr/bin/env node\n// comments...\nimport{createRequire...
	importMarker := "import{createRequire"
	if !strings.Contains(content, importMarker) {
		// Fallback: try other import patterns
		importMarker = "import "
	}
	if idx := strings.Index(content, importMarker); idx >= 0 {
		content = content[:idx] + modelMapJS + content[idx:]
	} else {
		return fmt.Errorf("cannot find import statement in cli.js; file format may have changed")
	}

	// Discover and apply function-level patches
	patches := discoverCLIPatchPoints(content)
	applied := 0
	for _, p := range patches {
		if strings.Contains(content, p.Old) {
			content = strings.Replace(content, p.Old, p.New, 1)
			applied++
		}
	}

	if applied == 0 {
		return fmt.Errorf("no function-level patches matched in cli.js; minified names may have changed (see ARCHITECTURE.md)")
	}

	return os.WriteFile(path, []byte(content), 0644)
}

// RestoreCLIBackup restores cli.js from backup.
func RestoreCLIBackup(path string) error {
	backupPath := path + ".claude-relay-backup"
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("no cli.js backup found at %s", backupPath)
	}
	return os.WriteFile(path, data, 0644)
}

// IsCLIPatchApplied checks if the CLI patch marker exists in cli.js.
func IsCLIPatchApplied(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), cliPatchMarker)
}

// HasCLIBackup checks if a cli.js backup file exists.
func HasCLIBackup(path string) bool {
	_, err := os.Stat(path + ".claude-relay-backup")
	return err == nil
}

func removeCLIPatch(content string) string {
	// The CLI patch is a single line injected before the import,
	// marked by cliPatchMarker at the beginning.
	// Also need to remove the globalThis.__cliMap calls injected into functions.
	// For simplicity, restore from backup is the recommended approach for full removal.
	// This function handles the header injection removal.
	if idx := strings.Index(content, cliPatchMarker); idx >= 0 {
		// Find the end of the injected line (up to the next import or newline)
		end := strings.Index(content[idx:], "import{")
		if end < 0 {
			end = strings.Index(content[idx:], "import ")
		}
		if end > 0 {
			content = content[:idx] + content[idx+end:]
		}
	}
	return content
}
