package report

import (
	"strings"
	"testing"

	"github.com/wpkc0429/agent-squiggles/internal/delta"
)

func TestFormat(t *testing.T) {
	diags := []delta.Diag{
		{Path: "/w/a/a.go", Line: 2, Col: 5, Severity: 1, Source: "compiler", Message: "undefined: x"},
		{Path: "/w/main.go", Line: 8, Col: 37, Severity: 1, Source: "compiler", Message: "not enough arguments in call to a.Greet\n\thave (string)\n\twant (string, int)"},
		{Path: "/w/main.go", Line: 9, Col: 0, Severity: 2, Message: "unused"},
	}
	out := Format(diags, Options{
		Root:   "/w",
		Edited: map[string]bool{"/w/a/a.go": true},
		Max:    2,
		Texts: map[string]string{
			"/w/a/a.go":  "package a\n\nfunc F() { x }\n",
			"/w/main.go": strings.Repeat("\n", 8) + "\tfmt.Println(a.Greet(\"x\"))\n",
		},
	})
	for _, want := range []string{
		"your last edit introduced 2 new errors and 1 new warning",
		"fix them before you finish",
		"a/a.go:3:6: error: undefined: x [compiler]\n    func F() { x }",
		"main.go:9:38: error: not enough arguments in call to a.Greet; have (string); want (string, int) [compiler] (in a file you did not edit)\n    fmt.Println(a.Greet(\"x\"))",
		"…and 1 more.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if Format(nil, Options{}) != "" {
		t.Error("empty input should render nothing")
	}
}

func TestSummary(t *testing.T) {
	got := Summary([]delta.Diag{{Severity: 1}})
	if got != "agent-squiggles: 1 new error introduced" {
		t.Fatalf("Summary = %q", got)
	}
}
