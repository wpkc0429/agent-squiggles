package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadInput(t *testing.T) {
	in, err := ReadInput(strings.NewReader(`{"session_id":"s","turn_id":"t","cwd":"/w","hook_event_name":"PostToolUse","tool_name":"apply_patch","tool_use_id":"call_1","tool_input":{"command":"*** Begin Patch"},"tool_response":"ok","model":"gpt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if in.ToolName != "apply_patch" || in.ToolUseID != "call_1" || string(in.ToolInput) != `{"command":"*** Begin Patch"}` {
		t.Fatalf("unexpected input: %+v", in)
	}
	if _, err := ReadInput(strings.NewReader(`{}`)); err == nil {
		t.Fatal("expected error for missing event name")
	}
}

func TestPostToolUseOutput(t *testing.T) {
	if out := PostToolUseOutput("context", "", "", nil); out != nil {
		t.Fatalf("expected no output, got %s", out)
	}
	var got map[string]any
	_ = json.Unmarshal(PostToolUseOutput("context", "report", "1 new error", []string{"installed"}), &got)
	hso := got["hookSpecificOutput"].(map[string]any)
	if hso["additionalContext"] != "report" || hso["hookEventName"] != "PostToolUse" || got["systemMessage"] != "1 new error\ninstalled" || got["decision"] != nil {
		t.Fatalf("context mode output: %v", got)
	}
	got = nil
	_ = json.Unmarshal(PostToolUseOutput("block", "report", "", nil), &got)
	if got["decision"] != "block" || got["reason"] != "report" || got["hookSpecificOutput"] != nil {
		t.Fatalf("block mode output: %v", got)
	}
	got = nil
	_ = json.Unmarshal(PostToolUseOutput("context", "", "", []string{"installing"}), &got)
	if got["systemMessage"] != "installing" || len(got) != 1 {
		t.Fatalf("notice-only output: %v", got)
	}
}

func TestInstallPreservesOtherHooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	existing := `{
  "description": "mine",
  "hooks": {
    "PostToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "echo other"}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "notify"}]}]
  }
}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := "agent-squiggles hook codex"
	for i := 0; i < 2; i++ { // idempotent
		if err := Install(path, cmd); err != nil {
			t.Fatal(err)
		}
	}
	if !Installed(path) {
		t.Fatal("Installed = false after Install")
	}
	data, _ := os.ReadFile(path)
	var doc struct {
		Description string `json:"description"`
		Hooks       map[string][]struct {
			Matcher string           `json:"matcher"`
			Hooks   []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Description != "mine" || len(doc.Hooks["Stop"]) != 1 {
		t.Fatalf("unrelated content lost: %s", data)
	}
	post := doc.Hooks["PostToolUse"]
	if len(post) != 2 || post[0].Hooks[0]["command"] != "echo other" || post[1].Hooks[0]["command"] != cmd || post[1].Matcher != toolMatcher {
		t.Fatalf("PostToolUse groups: %+v", post)
	}
	if len(doc.Hooks["PreToolUse"]) != 1 || len(doc.Hooks["SessionStart"]) != 1 || doc.Hooks["SessionStart"][0].Hooks[0]["async"] != nil {
		t.Fatalf("missing groups: %s", data)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatal("no backup written")
	}

	n, err := Uninstall(path)
	if err != nil || n != 3 {
		t.Fatalf("Uninstall = %d, %v", n, err)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "agent-squiggles") || !strings.Contains(string(data), "echo other") || strings.Contains(string(data), "PreToolUse") {
		t.Fatalf("after uninstall: %s", data)
	}
}

func TestInstallCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "hooks.json")
	if err := Install(path, "/opt/bin/agent-squiggles hook codex"); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for k := range doc {
		if k != "hooks" {
			t.Fatalf("unexpected top-level key %q (Codex rejects unknown keys)", k)
		}
	}
}

func TestShellQuote(t *testing.T) {
	if ShellQuote("/usr/local/bin/agent-squiggles") != "/usr/local/bin/agent-squiggles" {
		t.Fatal("plain path should not be quoted")
	}
	if got := ShellQuote("/Users/me/My Tools/agent-squiggles"); got != "'/Users/me/My Tools/agent-squiggles'" {
		t.Fatalf("got %s", got)
	}
}
