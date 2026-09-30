// Package report renders new diagnostics as a short message for the agent.
package report

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wpkc0429/agent-squiggles/internal/delta"
)

// Options controls rendering.
type Options struct {
	// Root makes paths relative for display.
	Root string
	// Edited is the set of files the tool changed (absolute paths); other
	// files are called out as indirect breakage.
	Edited map[string]bool
	// Max caps the number of diagnostics listed.
	Max int
	// Texts supplies file contents for code excerpts; missing files are
	// read from disk.
	Texts map[string]string
}

const maxExcerpt = 160

// Format renders diags, or "" if there are none.
func Format(diags []delta.Diag, opts Options) string {
	if len(diags) == 0 {
		return ""
	}
	errors, warnings := 0, 0
	for _, d := range diags {
		if d.Severity <= 1 {
			errors++
		} else {
			warnings++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "agent-squiggles: this change introduced %s. Pre-existing problems are not listed.\n", counts(errors, warnings))
	shown := diags
	if opts.Max > 0 && len(shown) > opts.Max {
		shown = shown[:opts.Max]
	}
	lines := map[string][]string{}
	for _, d := range shown {
		rel := d.Path
		if opts.Root != "" {
			if r, err := filepath.Rel(opts.Root, d.Path); err == nil && !strings.HasPrefix(r, "..") {
				rel = filepath.ToSlash(r)
			}
		}
		kind := "error"
		if d.Severity > 1 {
			kind = "warning"
		}
		tag := ""
		if d.Source != "" || d.Code != "" {
			tag = " [" + strings.TrimSpace(d.Source+" "+d.Code) + "]"
		}
		fmt.Fprintf(&b, "\n%s:%d:%d: %s: %s%s", rel, d.Line+1, d.Col+1, kind, oneLine(d.Message), tag)
		if opts.Edited != nil && !opts.Edited[d.Path] {
			b.WriteString(" (in a file you did not edit)")
		}
		b.WriteByte('\n')
		if _, ok := lines[d.Path]; !ok {
			lines[d.Path] = fileLines(d.Path, opts.Texts)
		}
		if src := lines[d.Path]; d.Line < len(src) {
			if ex := strings.TrimSpace(src[d.Line]); ex != "" {
				if len(ex) > maxExcerpt {
					ex = ex[:maxExcerpt] + "…"
				}
				fmt.Fprintf(&b, "    %s\n", ex)
			}
		}
	}
	if rest := len(diags) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "\n…and %d more.\n", rest)
	}
	b.WriteString("\nFix these before moving on, unless they are expected mid-refactor.")
	return b.String()
}

// Summary is a one-line version for the user-facing status message.
func Summary(diags []delta.Diag) string {
	errors, warnings := 0, 0
	for _, d := range diags {
		if d.Severity <= 1 {
			errors++
		} else {
			warnings++
		}
	}
	return "agent-squiggles: " + counts(errors, warnings) + " introduced"
}

func counts(errors, warnings int) string {
	var parts []string
	if errors > 0 {
		parts = append(parts, plural(errors, "new error"))
	}
	if warnings > 0 {
		parts = append(parts, plural(warnings, "new warning"))
	}
	return strings.Join(parts, " and ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// oneLine folds a multi-line message ("x\n\thave (a)\n\twant (b)") into
// "x; have (a); want (b)".
func oneLine(msg string) string {
	parts := strings.Split(strings.ReplaceAll(msg, "\r", ""), "\n")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, "; ")
}

func fileLines(path string, texts map[string]string) []string {
	if t, ok := texts[path]; ok {
		return strings.Split(t, "\n")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Split(string(data), "\n")
}
