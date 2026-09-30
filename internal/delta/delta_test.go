package delta

import (
	"reflect"
	"strings"
	"testing"
)

func TestMapLines(t *testing.T) {
	old := "a\nb\nc\nd\ne"
	cur := "a\nNEW\nb\nc\ne\nf"
	lm := MapLines(old, cur)
	got := []int{lm(0), lm(1), lm(2), lm(3), lm(4)}
	want := []int{0, 2, 3, -1, 4}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("map = %v, want %v", got, want)
	}
}

func TestMapLinesHuge(t *testing.T) {
	// Middles too large for the LCS table degrade to "removed" without
	// breaking the prefix/suffix mapping.
	a := strings.Repeat("x\n", 3000)
	b := strings.Repeat("y\n", 3000)
	lm := MapLines("head\n"+a+"tail", "head\n"+b+"tail")
	if lm(0) != 0 || lm(1) != -1 || lm(3001) != 3001 {
		t.Fatalf("unexpected mapping: %d %d %d", lm(0), lm(1), lm(3001))
	}
}

func d(path string, line int, msg string) Diag {
	return Diag{Path: path, Line: line, Severity: 1, Source: "compiler", Message: msg}
}

func TestNewIgnoresPreexistingAndMovedErrors(t *testing.T) {
	oldText := "package a\n\nfunc f() int { return \"s\" }\n"
	newText := "package a\n\n// comment\n\nfunc f() int { return \"s\" }\n\nfunc g() { undefinedThing() }\n"
	before := map[string][]Diag{"a.go": {d("a.go", 2, "cannot use \"s\" as int value")}}
	after := map[string][]Diag{"a.go": {
		d("a.go", 4, "cannot use \"s\" as int value"),
		d("a.go", 6, "undefined: undefinedThing"),
	}}
	got := New(before, after, map[string]LineMap{"a.go": MapLines(oldText, newText)})
	if len(got) != 1 || got[0].Message != "undefined: undefinedThing" {
		t.Fatalf("New = %+v", got)
	}
}

func TestNewCountsDuplicates(t *testing.T) {
	// The same message twice where there used to be one: exactly one is new,
	// and it is the one that is not on the mapped line.
	before := map[string][]Diag{"a.go": {d("a.go", 3, "undefined: x")}}
	after := map[string][]Diag{"a.go": {d("a.go", 3, "undefined: x"), d("a.go", 9, "undefined: x")}}
	got := New(before, after, nil)
	if len(got) != 1 || got[0].Line != 9 {
		t.Fatalf("New = %+v", got)
	}
}

func TestNewCrossFile(t *testing.T) {
	before := map[string][]Diag{}
	after := map[string][]Diag{"main.go": {d("main.go", 8, "not enough arguments in call to a.Greet")}}
	got := New(before, after, nil)
	if len(got) != 1 || got[0].Path != "main.go" {
		t.Fatalf("New = %+v", got)
	}
}

func TestNewWhitespaceInsensitiveMessages(t *testing.T) {
	before := map[string][]Diag{"m.go": {d("m.go", 1, "not enough arguments\n\thave (string)")}}
	after := map[string][]Diag{"m.go": {d("m.go", 1, "not enough arguments have (string)")}}
	if got := New(before, after, nil); len(got) != 0 {
		t.Fatalf("New = %+v", got)
	}
}

func TestNewRewrittenLineKeepsExistingError(t *testing.T) {
	// The line holding an existing error was edited (so its line maps to
	// -1), but the same error is still there: not new.
	oldText := "x := broken()\n"
	newText := "x := broken() // tweak\n"
	before := map[string][]Diag{"a.go": {d("a.go", 0, "undefined: broken")}}
	after := map[string][]Diag{"a.go": {d("a.go", 0, "undefined: broken")}}
	if got := New(before, after, map[string]LineMap{"a.go": MapLines(oldText, newText)}); len(got) != 0 {
		t.Fatalf("New = %+v", got)
	}
}
