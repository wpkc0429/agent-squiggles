// Package config loads agent-squiggles settings from the user config file
// and the project's .agent-squiggles.json, in that order of precedence
// (project wins).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProjectFile is the per-repository config file name.
const ProjectFile = ".agent-squiggles.json"

// Language holds per-language overrides.
type Language struct {
	// Disabled turns checking off for this language.
	Disabled bool `json:"disabled,omitempty"`
	// Command overrides the language server command line.
	Command []string `json:"command,omitempty"`
	// Settings are merged into the settings sent to the server.
	Settings map[string]any `json:"settings,omitempty"`
}

// Config is the effective configuration.
type Config struct {
	// Disabled turns agent-squiggles off entirely.
	Disabled bool `json:"disabled,omitempty"`
	// Severity is the minimum severity reported: "error" (default) or
	// "warning".
	Severity string `json:"severity,omitempty"`
	// MaxDiagnostics caps how many new diagnostics are shown to the agent.
	MaxDiagnostics int `json:"maxDiagnostics,omitempty"`
	// MaxDependents caps how many importing files are opened to find
	// cross-file breakage (TypeScript and Python).
	MaxDependents int `json:"maxDependents,omitempty"`
	// Bash enables checking files changed by shell commands.
	Bash *bool `json:"bash,omitempty"`
	// AutoInstall lets agent-squiggles install missing language servers.
	AutoInstall *bool `json:"autoInstall,omitempty"`
	// Mode is "context" (default: add a note for the agent) or "block"
	// (replace the tool result with the errors).
	Mode string `json:"mode,omitempty"`
	// TimeoutSeconds bounds a single check, including server startup.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
	// Languages holds per-language overrides keyed by "go", "typescript",
	// or "python".
	Languages map[string]Language `json:"languages,omitempty"`
}

// Defaults returns the built-in configuration.
func Defaults() Config {
	t := true
	return Config{
		Severity:       "error",
		MaxDiagnostics: 20,
		MaxDependents:  25,
		Bash:           &t,
		AutoInstall:    &t,
		Mode:           "context",
		TimeoutSeconds: 45,
		Languages:      map[string]Language{},
	}
}

// UserFile returns the path of the user-level config file.
func UserFile() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "agent-squiggles", "config.json")
}

// Load returns the effective configuration for a workspace root. The
// project file is only read when the project is trusted, because it can
// set language server commands and settings.
func Load(root string, trusted bool) (Config, error) {
	cfg := Defaults()
	files := []string{UserFile()}
	if trusted {
		files = append(files, filepath.Join(root, ProjectFile))
	}
	for _, path := range files {
		if err := mergeFile(&cfg, path); err != nil {
			return cfg, err
		}
	}
	if v := os.Getenv("AGENT_SQUIGGLES_DISABLE"); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		cfg.Disabled = true
	}
	return cfg, cfg.validate()
}

func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var over Config
	if err := json.Unmarshal(data, &over); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if over.Disabled {
		cfg.Disabled = true
	}
	if over.Severity != "" {
		cfg.Severity = over.Severity
	}
	if over.MaxDiagnostics != 0 {
		cfg.MaxDiagnostics = over.MaxDiagnostics
	}
	if over.MaxDependents != 0 {
		cfg.MaxDependents = over.MaxDependents
	}
	if over.Bash != nil {
		cfg.Bash = over.Bash
	}
	if over.AutoInstall != nil {
		cfg.AutoInstall = over.AutoInstall
	}
	if over.Mode != "" {
		cfg.Mode = over.Mode
	}
	if over.TimeoutSeconds != 0 {
		cfg.TimeoutSeconds = over.TimeoutSeconds
	}
	for name, l := range over.Languages {
		cur := cfg.Languages[name]
		if l.Disabled {
			cur.Disabled = true
		}
		if len(l.Command) > 0 {
			cur.Command = l.Command
		}
		if len(l.Settings) > 0 {
			if cur.Settings == nil {
				cur.Settings = map[string]any{}
			}
			MergeMaps(cur.Settings, l.Settings)
		}
		cfg.Languages[name] = cur
	}
	return nil
}

func (c Config) validate() error {
	switch c.Severity {
	case "error", "warning":
	default:
		return fmt.Errorf("config: severity must be \"error\" or \"warning\", got %q", c.Severity)
	}
	switch c.Mode {
	case "context", "block":
	default:
		return fmt.Errorf("config: mode must be \"context\" or \"block\", got %q", c.Mode)
	}
	return nil
}

// MaxSeverity returns the numeric LSP severity threshold (1=error, 2=warning).
func (c Config) MaxSeverity() int {
	if c.Severity == "warning" {
		return 2
	}
	return 1
}

// BashEnabled reports whether shell-command edits are checked.
func (c Config) BashEnabled() bool { return c.Bash == nil || *c.Bash }

// AutoInstallEnabled reports whether missing servers may be installed.
func (c Config) AutoInstallEnabled() bool { return c.AutoInstall == nil || *c.AutoInstall }

// MergeMaps deep-merges src into dst.
func MergeMaps(dst, src map[string]any) {
	for k, v := range src {
		if sv, ok := v.(map[string]any); ok {
			if dv, ok := dst[k].(map[string]any); ok {
				MergeMaps(dv, sv)
				continue
			}
		}
		dst[k] = v
	}
}
