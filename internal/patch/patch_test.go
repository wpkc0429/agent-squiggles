package patch

import (
	"encoding/json"
	"reflect"
	"testing"
)

const sample = `*** Begin Patch
*** Add File: hello.go
+package main
*** Update File: src/app.ts
*** Move to: src/main.ts
@@ export function greet
-export function greet(name: string) {
+export function greet(name: string, n: number) {
*** Delete File: old.py
*** End Patch`

func TestParse(t *testing.T) {
	got := Parse(sample)
	want := []File{
		{Op: Add, Path: "hello.go"},
		{Op: Update, Path: "src/app.ts", MoveTo: "src/main.ts"},
		{Op: Delete, Path: "old.py"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v, want %+v", got, want)
	}
	if paths := Paths(got); !reflect.DeepEqual(paths, []string{"hello.go", "src/app.ts", "src/main.ts", "old.py"}) {
		t.Fatalf("Paths = %v", paths)
	}
}

func TestCommandText(t *testing.T) {
	for name, input := range map[string]any{
		"object": map[string]any{"command": sample},
		"argv":   map[string]any{"command": []string{"apply_patch", sample}},
		"string": sample,
	} {
		raw, _ := json.Marshal(input)
		if got := CommandText(raw); got != sample {
			t.Errorf("%s: CommandText = %q", name, got)
		}
	}
}
