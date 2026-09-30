// Package patch extracts the file paths touched by a Codex apply_patch
// payload. It is used when the workspace is not a git repository and
// snapshots are unavailable.
package patch

import (
	"encoding/json"
	"strings"
)

// Op is the kind of change apply_patch makes to a file.
type Op string

const (
	Add    Op = "add"
	Update Op = "update"
	Delete Op = "delete"
)

// File is one file touched by a patch.
type File struct {
	Op   Op
	Path string
	// MoveTo is set when an update also renames the file.
	MoveTo string
}

// CommandText extracts the patch text from a hook tool_input value. Codex
// sends {"command": "<patch>"}; older builds used an argv array whose last
// element is the patch.
func CommandText(toolInput json.RawMessage) string {
	var obj struct {
		Command json.RawMessage `json:"command"`
		Input   string          `json:"input"`
		Patch   string          `json:"patch"`
	}
	if json.Unmarshal(toolInput, &obj) != nil {
		var s string
		if json.Unmarshal(toolInput, &s) == nil {
			return s
		}
		return ""
	}
	if len(obj.Command) > 0 {
		var s string
		if json.Unmarshal(obj.Command, &s) == nil {
			return s
		}
		var argv []string
		if json.Unmarshal(obj.Command, &argv) == nil && len(argv) > 0 {
			return argv[len(argv)-1]
		}
	}
	if obj.Input != "" {
		return obj.Input
	}
	return obj.Patch
}

// Parse returns the files touched by an apply_patch document, in order.
func Parse(text string) []File {
	var files []File
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			files = append(files, File{Op: Add, Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))})
		case strings.HasPrefix(line, "*** Delete File: "):
			files = append(files, File{Op: Delete, Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))})
		case strings.HasPrefix(line, "*** Update File: "):
			files = append(files, File{Op: Update, Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))})
		case strings.HasPrefix(line, "*** Move to: ") && len(files) > 0:
			files[len(files)-1].MoveTo = strings.TrimSpace(strings.TrimPrefix(line, "*** Move to: "))
		}
	}
	return files
}

// Paths returns every path a patch reads or writes, including rename
// targets, without duplicates.
func Paths(files []File) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, f := range files {
		add(f.Path)
		add(f.MoveTo)
	}
	return out
}
