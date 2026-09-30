package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMergesUserAndProject(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("AGENT_SQUIGGLES_DISABLE", "")
	if err := os.MkdirAll(filepath.Join(cfgHome, "agent-squiggles"), 0o755); err != nil {
		t.Fatal(err)
	}
	user := `{"maxDiagnostics": 5, "autoInstall": false, "languages": {"python": {"settings": {"python": {"analysis": {"typeCheckingMode": "strict", "x": 1}}}}}}`
	if err := os.WriteFile(filepath.Join(cfgHome, "agent-squiggles", "config.json"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	project := `{"severity": "warning", "languages": {"python": {"settings": {"python": {"analysis": {"typeCheckingMode": "basic"}}}}, "go": {"disabled": true}}}`
	if err := os.WriteFile(filepath.Join(root, ProjectFile), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxDiagnostics != 5 || cfg.AutoInstallEnabled() || cfg.MaxSeverity() != 2 || !cfg.BashEnabled() {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if !cfg.Languages["go"].Disabled {
		t.Fatal("go should be disabled")
	}
	analysis := cfg.Languages["python"].Settings["python"].(map[string]any)["analysis"].(map[string]any)
	if analysis["typeCheckingMode"] != "basic" || analysis["x"] != float64(1) {
		t.Fatalf("settings not deep-merged: %v", analysis)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ProjectFile), []byte(`{"mode": "loud"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, true); err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestDisableEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AGENT_SQUIGGLES_DISABLE", "1")
	cfg, err := Load(t.TempDir(), true)
	if err != nil || !cfg.Disabled {
		t.Fatalf("cfg.Disabled = %v, err = %v", cfg.Disabled, err)
	}
}

func TestUntrustedProjectFileIgnored(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AGENT_SQUIGGLES_DISABLE", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ProjectFile), []byte(`{"languages": {"go": {"command": ["/tmp/evil"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Languages["go"].Command) != 0 {
		t.Fatal("untrusted project config must not set commands")
	}
}
