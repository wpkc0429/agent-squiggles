// Package paths centralizes where agent-squiggles keeps its files.
package paths

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// DataDir holds installed language servers, logs, and snapshot indexes.
// It honors AGENT_SQUIGGLES_HOME, then XDG_DATA_HOME.
func DataDir() string {
	if d := os.Getenv("AGENT_SQUIGGLES_HOME"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "agent-squiggles")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "agent-squiggles")
}

// ServersDir holds language servers installed by agent-squiggles.
func ServersDir() string { return filepath.Join(DataDir(), "servers") }

// LogDir holds daemon logs.
func LogDir() string { return filepath.Join(DataDir(), "logs") }

// StateDir holds per-workspace private git indexes.
func StateDir() string { return filepath.Join(DataDir(), "state") }

// RuntimeDir holds daemon sockets. Socket paths must stay short, so this
// prefers XDG_RUNTIME_DIR and falls back to a per-user temp directory.
func RuntimeDir() string {
	if d := os.Getenv("AGENT_SQUIGGLES_RUNTIME_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "agent-squiggles")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("agent-squiggles-%d", os.Getuid()))
}

// WorkspaceID is a short stable identifier for a workspace root.
func WorkspaceID(root string) string {
	sum := sha1.Sum([]byte(root))
	return hex.EncodeToString(sum[:6])
}

// SocketPath is the daemon socket for a workspace root.
func SocketPath(root string) string {
	return filepath.Join(RuntimeDir(), WorkspaceID(root)+".sock")
}

// LockPath is the daemon's single-instance lock for a workspace root.
func LockPath(root string) string {
	return filepath.Join(RuntimeDir(), WorkspaceID(root)+".lock")
}

// LogPath is the daemon log file for a workspace root.
func LogPath(root string) string {
	return filepath.Join(LogDir(), WorkspaceID(root)+".log")
}
