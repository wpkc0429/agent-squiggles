package trust

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrusted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("AGENT_SQUIGGLES_TRUST_PROJECT", "")
	trusted := t.TempDir()
	untrusted := t.TempDir()
	child := filepath.Join(trusted, "child")
	config := `model = "gpt"

[projects."` + trusted + `"]
trust_level = "trusted" # set by codex

[projects.'` + untrusted + `']
trust_level = "untrusted"

[other]
trust_level = "trusted"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if !Trusted(trusted) {
		t.Error("trusted project reported untrusted")
	}
	if Trusted(untrusted) {
		t.Error("untrusted project reported trusted")
	}
	// Like Codex, trust is per project root, not inherited by subdirectories.
	if Trusted(child) {
		t.Error("child of a trusted project should not inherit trust")
	}
	t.Setenv("AGENT_SQUIGGLES_TRUST_PROJECT", "1")
	if !Trusted(untrusted) {
		t.Error("AGENT_SQUIGGLES_TRUST_PROJECT should force trust")
	}
}

func TestTrustedWithoutConfig(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("AGENT_SQUIGGLES_TRUST_PROJECT", "")
	if Trusted(t.TempDir()) {
		t.Error("no config should mean untrusted")
	}
}
