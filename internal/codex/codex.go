// Package codex adapts agent-squiggles to Codex CLI hooks: it parses hook
// payloads, renders hook output, and installs the hook configuration.
package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// HookInput is the JSON object Codex writes to a hook's stdin.
type HookInput struct {
	SessionID     string          `json:"session_id"`
	TurnID        string          `json:"turn_id"`
	Cwd           string          `json:"cwd"`
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolUseID     string          `json:"tool_use_id"`
	ToolInput     json.RawMessage `json:"tool_input"`
	Source        string          `json:"source"`
}

// ReadInput decodes a hook payload.
func ReadInput(r io.Reader) (*HookInput, error) {
	var in HookInput
	if err := json.NewDecoder(io.LimitReader(r, 32<<20)).Decode(&in); err != nil {
		return nil, fmt.Errorf("decode hook input: %w", err)
	}
	if in.HookEventName == "" {
		return nil, errors.New("hook input has no hook_event_name")
	}
	return &in, nil
}

// PostToolUseOutput renders the hook's stdout for a PostToolUse event, or
// nil when there is nothing to say.
//
// In "context" mode the report is added for the model as additional
// context and the tool result is left alone. In "block" mode Codex
// replaces the tool result with the report, which is a stronger nudge but
// makes the agent believe the tool call failed.
func PostToolUseOutput(mode, context, summary string, notices []string) []byte {
	status := strings.Join(append(nonEmpty(summary), notices...), "\n")
	out := map[string]any{}
	switch {
	case context != "" && mode == "block":
		out["decision"] = "block"
		out["reason"] = context
	case context != "":
		out["hookSpecificOutput"] = map[string]any{
			"hookEventName":     "PostToolUse",
			"additionalContext": context,
		}
	}
	if status != "" {
		out["systemMessage"] = status
	}
	if len(out) == 0 {
		return nil
	}
	data, _ := json.Marshal(out)
	return data
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// Marker identifies hook commands installed by agent-squiggles.
const Marker = "hook codex"

// DefaultHooksPath is the user-level hooks file.
func DefaultHooksPath() string {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".codex")
	}
	return filepath.Join(home, "hooks.json")
}

// ProjectHooksPath is the repository-level hooks file.
func ProjectHooksPath(root string) string {
	return filepath.Join(root, ".codex", "hooks.json")
}

const toolMatcher = "^(apply_patch|Bash)$"

// hookGroups returns the matcher groups agent-squiggles installs.
func hookGroups(command string) map[string]map[string]any {
	handler := func(timeout int) map[string]any {
		return map[string]any{"type": "command", "command": command, "timeout": timeout}
	}
	return map[string]map[string]any{
		// Start warming up language servers when a session starts. The
		// daemon does the work in the background and the hook returns at
		// once, so this is not an "async" hook (older Codex builds skip
		// those).
		"SessionStart": {"matcher": "startup|resume|clear", "hooks": []any{handler(30)}},
		// Snapshot the tree right before an edit.
		"PreToolUse": {"matcher": toolMatcher, "hooks": []any{handler(30)}},
		// Compare diagnostics right after it.
		"PostToolUse": {"matcher": toolMatcher, "hooks": []any{handler(120)}},
	}
}

func isOurs(handler any) bool {
	h, ok := handler.(map[string]any)
	if !ok {
		return false
	}
	cmd, _ := h["command"].(string)
	return strings.Contains(cmd, "agent-squiggles") && strings.Contains(cmd, Marker)
}

func load(path string) (map[string]any, []byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	doc := map[string]any{}
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	}
	return doc, data, nil
}

// strip removes agent-squiggles handlers from doc and reports how many.
func strip(doc map[string]any) int {
	hooks, _ := doc["hooks"].(map[string]any)
	removed := 0
	for event, v := range hooks {
		groups, _ := v.([]any)
		var kept []any
		for _, g := range groups {
			group, ok := g.(map[string]any)
			if !ok {
				kept = append(kept, g)
				continue
			}
			handlers, _ := group["hooks"].([]any)
			var keptHandlers []any
			for _, h := range handlers {
				if isOurs(h) {
					removed++
				} else {
					keptHandlers = append(keptHandlers, h)
				}
			}
			if len(keptHandlers) == 0 && len(handlers) > 0 {
				continue
			}
			group["hooks"] = keptHandlers
			kept = append(kept, group)
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	return removed
}

func save(path string, doc map[string]any, previous []byte) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if string(data) == string(previous) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if previous != nil {
		if err := os.WriteFile(path+".bak", previous, 0o644); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Install adds (or refreshes) the agent-squiggles hooks in a hooks.json
// file, leaving other hooks untouched. A backup of the previous file is
// written next to it as hooks.json.bak.
func Install(path, command string) error {
	doc, previous, err := load(path)
	if err != nil {
		return err
	}
	strip(doc)
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for event, group := range hookGroups(command) {
		existing, _ := hooks[event].([]any)
		hooks[event] = append(existing, group)
	}
	doc["hooks"] = hooks
	return save(path, doc, previous)
}

// Uninstall removes agent-squiggles hooks and reports how many handlers
// were removed.
func Uninstall(path string) (int, error) {
	doc, previous, err := load(path)
	if err != nil || previous == nil {
		return 0, err
	}
	n := strip(doc)
	if n == 0 {
		return 0, nil
	}
	if hooks, _ := doc["hooks"].(map[string]any); len(hooks) == 0 {
		delete(doc, "hooks")
	}
	return n, save(path, doc, previous)
}

// Installed reports whether path contains agent-squiggles hooks.
func Installed(path string) bool {
	doc, previous, err := load(path)
	if err != nil || previous == nil {
		return false
	}
	return strip(doc) > 0
}

// ShellQuote quotes s for use in a hook command line if needed.
func ShellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
