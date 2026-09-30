// Package bashcmd recognizes shell commands that cannot modify source
// files, so agent-squiggles can skip snapshotting around them. Anything it
// does not recognize is treated as a potential write.
package bashcmd

import (
	"encoding/json"
	"strings"
)

var readOnly = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "grep": true, "egrep": true,
	"fgrep": true, "rg": true, "ag": true, "wc": true, "pwd": true, "echo": true,
	"printf": true, "which": true, "type": true, "tree": true, "stat": true, "file": true,
	"du": true, "df": true, "env": true, "printenv": true, "cd": true, "true": true,
	"false": true, "test": true, "[": true, "jq": true, "sort": true, "uniq": true,
	"cut": true, "tr": true, "diff": true, "cmp": true, "nl": true, "basename": true,
	"dirname": true, "realpath": true, "readlink": true, "date": true, "whoami": true,
	"uname": true, "ps": true, "less": true, "more": true, "column": true, "fd": true,
	"sleep": true, "id": true, "hostname": true, "nproc": true, "command": true,
}

// read-only git subcommands.
var gitReadOnly = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "branch": true,
	"rev-parse": true, "ls-files": true, "blame": true, "grep": true, "remote": true,
	"describe": true, "shortlog": true, "ls-tree": true, "cat-file": true, "reflog": true,
}

// CommandText extracts the shell command from a Bash hook tool_input.
func CommandText(toolInput json.RawMessage) string {
	var obj struct {
		Command json.RawMessage `json:"command"`
		Cmd     string          `json:"cmd"`
	}
	if json.Unmarshal(toolInput, &obj) != nil {
		return ""
	}
	if len(obj.Command) > 0 {
		var s string
		if json.Unmarshal(obj.Command, &s) == nil {
			return s
		}
		var argv []string
		if json.Unmarshal(obj.Command, &argv) == nil {
			return strings.Join(argv, " ")
		}
	}
	return obj.Cmd
}

// IsReadOnly reports whether cmd certainly leaves the file system alone.
// It is deliberately conservative.
func IsReadOnly(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return true
	}
	// Command substitution and subshells can hide anything.
	if strings.Contains(cmd, "$(") || strings.Contains(cmd, "`") || strings.ContainsAny(cmd, "(){}") {
		return false
	}
	if hasWriteRedirect(cmd) {
		return false
	}
	for _, seg := range splitSegments(cmd) {
		if !segmentReadOnly(seg) {
			return false
		}
	}
	return true
}

// hasWriteRedirect reports whether cmd redirects output to a file other
// than /dev/null or another file descriptor.
func hasWriteRedirect(cmd string) bool {
	inSingle, inDouble := false, false
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '>' && !inSingle && !inDouble:
			rest := strings.TrimLeft(cmd[i+1:], ">")
			if strings.HasPrefix(rest, "&") {
				continue // 2>&1
			}
			rest = strings.TrimSpace(rest)
			if strings.HasPrefix(rest, "/dev/null") {
				continue
			}
			return true
		}
	}
	return false
}

func splitSegments(cmd string) []string {
	var segs []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '&' && ((i > 0 && cmd[i-1] == '>') || (i+1 < len(cmd) && cmd[i+1] == '>')):
			// Part of a redirection such as 2>&1 or &>file, not a separator.
		case !inSingle && !inDouble && (c == ';' || c == '|' || c == '&' || c == '\n'):
			segs = append(segs, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	return append(segs, cur.String())
}

func segmentReadOnly(seg string) bool {
	fields := strings.Fields(seg)
	// Skip leading VAR=value assignments.
	for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "-") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return true
	}
	name := fields[0]
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	args := fields[1:]
	switch name {
	case "git":
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			return gitReadOnly[a]
		}
		return true
	case "sed":
		for _, a := range args {
			if a == "-i" || strings.HasPrefix(a, "-i") || a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
				return false
			}
		}
		return true
	case "find":
		for _, a := range args {
			switch a {
			case "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprintf", "-fls":
				return false
			}
		}
		return true
	case "sort":
		for _, a := range args {
			if a == "-o" || strings.HasPrefix(a, "--output") {
				return false
			}
		}
		return true
	case "tee", "xargs":
		return false
	}
	return readOnly[name]
}
