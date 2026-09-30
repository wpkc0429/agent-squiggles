package bashcmd

import (
	"encoding/json"
	"testing"
)

func TestIsReadOnly(t *testing.T) {
	cases := map[string]bool{
		"ls -la":                             true,
		"cat main.go | head -20":             true,
		"rg -n 'func Greet' --type go":       true,
		"git status && git diff HEAD~1":      true,
		"sed -n '1,40p' main.go":             true,
		"grep -r foo . 2>&1 | wc -l":         true,
		"find . -name '*.go' 2>/dev/null":    true,
		"FOO=1 ls":                           true,
		"echo 'a > b'":                       true,
		"sed -i 's/a/b/' main.go":            false,
		"echo hi > out.txt":                  false,
		"cat a >> b":                         false,
		"go build ./...":                     false,
		"npm test":                           false,
		"gofmt -w .":                         false,
		"git checkout -- main.go":            false,
		"git stash":                          false,
		"find . -name '*.orig' -delete":      false,
		"ls | xargs rm":                      false,
		"echo $(touch x)":                    false,
		"python3 -c 'open(\"a\",\"w\")'":     false,
		"cat a.txt | tee b.txt":              false,
		"/usr/bin/git log --oneline -5":      true,
		"sort -o sorted.txt input.txt":       false,
		"ls; rm -rf build":                   false,
		"test -f go.mod && echo yes || true": true,
	}
	for cmd, want := range cases {
		if got := IsReadOnly(cmd); got != want {
			t.Errorf("IsReadOnly(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestCommandText(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"command": "ls -la"})
	if got := CommandText(raw); got != "ls -la" {
		t.Fatalf("got %q", got)
	}
	raw, _ = json.Marshal(map[string]any{"command": []string{"bash", "-lc", "ls"}})
	if got := CommandText(raw); got != "bash -lc ls" {
		t.Fatalf("got %q", got)
	}
}
