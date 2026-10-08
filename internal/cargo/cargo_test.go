package cargo

import (
	"path/filepath"
	"strings"
	"testing"
)

// Lines trimmed from real `cargo check --message-format=json` output.
const stdout = `{"reason":"compiler-artifact","package_id":"core 0.1.0"}
{"reason":"compiler-message","message":{"message":"this function takes 2 arguments but 1 argument was supplied","level":"error","code":{"code":"E0061"},"spans":[{"file_name":"crates/core/src/use_it.rs","line_start":2,"column_start":24,"is_primary":false,"label":"argument #2 of type ` + "`bool`" + ` is missing"},{"file_name":"crates/core/src/use_it.rs","line_start":2,"column_start":5,"is_primary":true,"label":null}]}}
{"reason":"compiler-message","message":{"message":"mismatched types","level":"error","code":{"code":"E0308"},"spans":[{"file_name":"crates/core/src/lib.rs","line_start":5,"column_start":5,"is_primary":true,"label":"expected ` + "`i32`, found `&str`" + `"}]}}
{"reason":"compiler-message","message":{"message":"this function takes 2 arguments but 1 argument was supplied","level":"error","code":{"code":"E0061"},"spans":[{"file_name":"crates/core/src/use_it.rs","line_start":2,"column_start":5,"is_primary":true,"label":null}]}}
{"reason":"compiler-message","message":{"message":"unused variable: ` + "`x`" + `","level":"warning","code":{"code":"unused_variables"},"spans":[{"file_name":"crates/core/src/lib.rs","line_start":9,"column_start":9,"is_primary":true,"label":null}]}}
{"reason":"compiler-message","message":{"message":"For more information about this error, try ` + "`rustc --explain E0061`" + `.","level":"failure-note","code":null,"spans":[]}}
{"reason":"compiler-message","message":{"message":"use of deprecated function","level":"error","code":null,"spans":[{"file_name":"/home/u/.cargo/registry/src/x/lib.rs","line_start":1,"column_start":1,"is_primary":true,"label":null}]}}
{"reason":"build-finished","success":false}
`

func TestParse(t *testing.T) {
	root := "/work"
	got := Parse(root, []byte(stdout), nil, true)
	useIt := got[filepath.Join(root, "crates/core/src/use_it.rs")]
	if len(useIt) != 1 {
		t.Fatalf("use_it.rs diagnostics = %+v, want one (deduplicated across targets)", useIt)
	}
	if d := useIt[0]; d.Line != 1 || d.Col != 4 || d.Code != "E0061" || d.Source != "rustc" || d.Severity != 1 {
		t.Fatalf("unexpected diagnostic %+v", d)
	}
	lib := got[filepath.Join(root, "crates/core/src/lib.rs")]
	if len(lib) != 2 || lib[0].Message != "mismatched types: expected `i32`, found `&str`" || lib[1].Severity != 2 {
		t.Fatalf("lib.rs diagnostics = %+v", lib)
	}
	for path := range got {
		if strings.Contains(path, ".cargo/registry") {
			t.Fatalf("registry diagnostics should be dropped: %s", path)
		}
	}
}

func TestParseManifestError(t *testing.T) {
	stderr := "error: unclosed table, expected `]`\n --> crates/core/Cargo.toml:1:9\n  |\n1 | [package\n  |         ^\n"
	got := Parse("/work", nil, []byte(stderr), true)
	ds := got["/work/crates/core/Cargo.toml"]
	if len(ds) != 1 || ds[0].Line != 0 || ds[0].Col != 8 || ds[0].Source != "cargo" || !strings.HasPrefix(ds[0].Message, "unclosed table") {
		t.Fatalf("manifest diagnostics = %+v", got)
	}

	// Without a location the error is attributed to the root manifest.
	got = Parse("/work", nil, []byte("error: failed to get `serde` as a dependency of package `core`\n"), true)
	if ds := got["/work/Cargo.toml"]; len(ds) != 1 {
		t.Fatalf("unlocated error = %+v", got)
	}

	// A failed build that reported compiler errors does not add stderr noise.
	got = Parse("/work", []byte(stdout), []byte("error: could not compile `core`\n"), true)
	if _, ok := got["/work/Cargo.toml"]; ok {
		t.Fatal("stderr summary should not become a diagnostic")
	}
}
