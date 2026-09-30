package lsp

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// Position is a zero-based line/character position (UTF-16 code units).
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a half-open range in a text document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Severity values from the LSP specification.
const (
	SeverityError       = 1
	SeverityWarning     = 2
	SeverityInformation = 3
	SeverityHint        = 4
)

// Diagnostic is an LSP diagnostic as published by a server.
type Diagnostic struct {
	Range    Range           `json:"range"`
	Severity int             `json:"severity,omitempty"`
	Code     json.RawMessage `json:"code,omitempty"`
	Source   string          `json:"source,omitempty"`
	Message  string          `json:"message"`
}

// CodeString renders the diagnostic code, which may be a number or a string.
func (d Diagnostic) CodeString() string {
	if len(d.Code) == 0 || string(d.Code) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(d.Code, &s); err == nil {
		return s
	}
	return string(d.Code)
}

// EffectiveSeverity treats a missing severity as an error, which is how
// most clients interpret it.
func (d Diagnostic) EffectiveSeverity() int {
	if d.Severity == 0 {
		return SeverityError
	}
	return d.Severity
}

// FileChangeType values for workspace/didChangeWatchedFiles.
const (
	FileCreated = 1
	FileChanged = 2
	FileDeleted = 3
)

// FileEvent is one entry of workspace/didChangeWatchedFiles.
type FileEvent struct {
	URI  string `json:"uri"`
	Type int    `json:"type"`
}

// PathToURI converts an absolute file path to a file:// URI.
func PathToURI(path string) string {
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

// URIToPath converts a file:// URI back to a local path. It returns ""
// for non-file URIs.
func URIToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		p = strings.TrimPrefix(p, "/")
	}
	return filepath.Clean(filepath.FromSlash(p))
}
