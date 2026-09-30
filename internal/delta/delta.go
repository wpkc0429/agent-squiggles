// Package delta decides which diagnostics a change introduced.
//
// Diagnostics have no stable identity: an edit above an existing error
// moves it to a different line, and servers rephrase nothing about it. So
// delta maps old line numbers onto the new text with a line diff, then
// matches old and new diagnostics by (severity, source, code, message),
// preferring matches on the same mapped line. Whatever is left unmatched
// on the new side is new.
package delta

import (
	"sort"
	"strings"
)

// Diag is a diagnostic reduced to what delta and reporting need.
type Diag struct {
	Path     string
	Line     int // zero-based
	Col      int // zero-based
	Severity int
	Code     string
	Source   string
	Message  string
}

func (d Diag) key() string {
	return strings.Join([]string{
		string(rune('0' + d.Severity)), d.Source, d.Code, strings.Join(strings.Fields(d.Message), " "),
	}, "\x00")
}

// LineMap maps a zero-based line in the old text to the new text, or -1
// if the line was removed.
type LineMap func(line int) int

// Identity is the LineMap for unchanged files.
func Identity(line int) int { return line }

// maxDPCells bounds the LCS table for the changed middle of a file.
const maxDPCells = 4_000_000

// MapLines builds a LineMap from oldText to newText.
func MapLines(oldText, newText string) LineMap {
	a := strings.Split(oldText, "\n")
	b := strings.Split(newText, "\n")
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	midA := a[prefix : len(a)-suffix]
	midB := b[prefix : len(b)-suffix]
	mid := lcsMap(midA, midB)
	shift := len(b) - len(a)
	oldLen := len(a)
	return func(line int) int {
		switch {
		case line < 0:
			return -1
		case line < prefix:
			return line
		case line >= oldLen-suffix:
			return line + shift
		default:
			m := mid[line-prefix]
			if m < 0 {
				return -1
			}
			return m + prefix
		}
	}
}

// lcsMap returns, for each line of a, the index of the matching line in b
// under a longest common subsequence, or -1.
func lcsMap(a, b []string) []int {
	out := make([]int, len(a))
	for i := range out {
		out[i] = -1
	}
	if len(a) == 0 || len(b) == 0 || (len(a)+1)*(len(b)+1) > maxDPCells {
		return out
	}
	w := len(b) + 1
	dp := make([]int32, (len(a)+1)*w)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i*w+j] = dp[(i+1)*w+j+1] + 1
			} else if dp[(i+1)*w+j] >= dp[i*w+j+1] {
				dp[i*w+j] = dp[(i+1)*w+j]
			} else {
				dp[i*w+j] = dp[i*w+j+1]
			}
		}
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out[i] = j
			i++
			j++
		case dp[(i+1)*w+j] >= dp[i*w+j+1]:
			i++
		default:
			j++
		}
	}
	return out
}

// New returns the diagnostics in after that have no counterpart in
// before. maps supplies line maps for files whose text changed; files
// without an entry are assumed unchanged. The result is sorted by path
// and position.
func New(before, after map[string][]Diag, maps map[string]LineMap) []Diag {
	var out []Diag
	for path, news := range after {
		lm := maps[path]
		if lm == nil {
			lm = Identity
		}
		type oldDiag struct {
			line int
			used bool
		}
		olds := map[string][]*oldDiag{}
		for _, d := range before[path] {
			olds[d.key()] = append(olds[d.key()], &oldDiag{line: lm(d.Line)})
		}
		matched := make([]bool, len(news))
		// Pass 1: same key on the same (mapped) line.
		for i, d := range news {
			for _, o := range olds[d.key()] {
				if !o.used && o.line == d.Line {
					o.used, matched[i] = true, true
					break
				}
			}
		}
		// Pass 2: same key anywhere in the file, nearest first. This keeps
		// an error that merely moved (or whose line was rewritten) from
		// being reported as new.
		for i, d := range news {
			if matched[i] {
				continue
			}
			var best *oldDiag
			bestDist := 0
			for _, o := range olds[d.key()] {
				if o.used {
					continue
				}
				dist := 1 << 30
				if o.line >= 0 {
					dist = abs(o.line - d.Line)
				}
				if best == nil || dist < bestDist {
					best, bestDist = o, dist
				}
			}
			if best != nil {
				best.used, matched[i] = true, true
			}
		}
		for i, d := range news {
			if !matched[i] {
				out = append(out, d)
			}
		}
	}
	Sort(out)
	return out
}

// Sort orders diagnostics by path, line, and column.
func Sort(ds []Diag) {
	sort.SliceStable(ds, func(i, j int) bool {
		if ds[i].Path != ds[j].Path {
			return ds[i].Path < ds[j].Path
		}
		if ds[i].Line != ds[j].Line {
			return ds[i].Line < ds[j].Line
		}
		return ds[i].Col < ds[j].Col
	})
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
