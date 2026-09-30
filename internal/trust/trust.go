// Package trust follows Codex's project trust decisions.
//
// Codex hooks run outside the Codex sandbox with the user's full
// permissions. Running a repository's own binaries (node_modules/.bin/tsc,
// .venv/bin/pyright-langserver) or honoring its .agent-squiggles.json
// (which can set server commands and settings such as gopls buildFlags)
// would let an untrusted repository run code through the hook. So
// agent-squiggles only does those things for projects Codex itself trusts
// ([projects."<root>"] trust_level = "trusted" in ~/.codex/config.toml).
package trust

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// ConfigPath is Codex's user config file.
func ConfigPath() string {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".codex")
	}
	return filepath.Join(home, "config.toml")
}

// Trusted reports whether Codex trusts the project rooted at root.
// AGENT_SQUIGGLES_TRUST_PROJECT=1 forces trust (for CI and scripts).
func Trusted(root string) bool {
	if v := os.Getenv("AGENT_SQUIGGLES_TRUST_PROJECT"); v == "1" || strings.EqualFold(v, "true") {
		return true
	}
	keys := map[string]bool{filepath.Clean(root): true}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		keys[filepath.Clean(real)] = true
	}
	for p, level := range projectTrust(ConfigPath()) {
		if keys[filepath.Clean(p)] && level == "trusted" {
			return true
		}
	}
	return false
}

// projectTrust extracts trust_level for each [projects."<path>"] table.
// It is a narrow line-based reader for the one shape Codex writes.
func projectTrust(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	current := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			current = projectKey(line)
			continue
		}
		if current == "" {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != "trust_level" {
			continue
		}
		value = strings.TrimSpace(value)
		if i := strings.Index(value, "#"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		out[current] = strings.Trim(value, `"'`)
	}
	return out
}

// projectKey returns the path in a [projects."<path>"] header, or "".
func projectKey(header string) string {
	h := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(header, "["), "]"))
	if !strings.HasPrefix(h, "projects.") {
		return ""
	}
	rest := strings.TrimSpace(strings.TrimPrefix(h, "projects."))
	switch {
	case len(rest) >= 2 && rest[0] == '"' && rest[len(rest)-1] == '"':
		s := rest[1 : len(rest)-1]
		s = strings.ReplaceAll(s, `\"`, `"`)
		return strings.ReplaceAll(s, `\\`, `\`)
	case len(rest) >= 2 && rest[0] == '\'' && rest[len(rest)-1] == '\'':
		return rest[1 : len(rest)-1]
	}
	return ""
}
