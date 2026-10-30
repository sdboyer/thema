package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCollection writes a collection of two lineages, ship and boat, each
// with history/0.0.thema, to a temporary directory, and returns the
// directory.
func writeCollection(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fleet")
	for _, name := range []string{"ship", "boat"} {
		dir := filepath.Join(root, name, "history")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "0.0.thema"), []byte("Schema: {\n\tname: string\n}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p) //nolint:gosec // a file the test wrote
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// compile writes each lineage's lineage.cue, lists the ones it wrote, and
// writes nothing the second time; --check fails until it has run, and after
// a version is added, until it runs again.
func TestLineageCompile(t *testing.T) {
	root := writeCollection(t)
	ship := filepath.Join(root, "ship", "lineage.cue")
	boat := filepath.Join(root, "boat", "lineage.cue")

	_, errOut, err := runThema(t, root, "", "lineage", "compile", "--check", "-l", root)
	if err == nil || strings.Count(errOut, "E0151") != 2 {
		t.Fatalf("--check before compile: err = %v, stderr:\n%s", err, errOut)
	}
	if _, serr := os.Stat(ship); serr == nil {
		t.Fatal("--check wrote lineage.cue")
	}

	out, errOut, err := runThema(t, root, "", "lineage", "compile", "-l", root)
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, errOut)
	}
	if !strings.Contains(out, filepath.Join("boat", "lineage.cue")) || !strings.Contains(out, filepath.Join("ship", "lineage.cue")) {
		t.Errorf("compile listed %q, want both lineage.cue", out)
	}
	if text := readFile(t, ship); !strings.Contains(text, "package ship\n") || !strings.Contains(text, `name: "ship"`) {
		t.Errorf("ship's lineage.cue:\n%s", text)
	}
	readFile(t, boat)

	out, errOut, err = runThema(t, root, "", "lineage", "compile", "-l", root)
	if err != nil || out != "" {
		t.Errorf("compile again: out %q, err %v\n%s", out, err, errOut)
	}
	if _, errOut, err = runThema(t, root, "", "lineage", "compile", "--check", "-l", root); err != nil {
		t.Errorf("--check after compile: %v\n%s", err, errOut)
	}

	// A version added to one lineage: its lineage.cue is stale, and compile
	// of it alone rewrites it.
	v01 := "Schema: {\n\tname:  string\n\tcrew?: int\n}\n"
	if err = os.WriteFile(filepath.Join(root, "ship", "history", "0.1.thema"), []byte(v01), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, err = runThema(t, root, "", "lineage", "compile", "--check", "-l", root)
	if err == nil || strings.Count(errOut, "E0151") != 1 || !strings.Contains(errOut, "it holds versions 0.0, and history/ holds 0.0, 0.1") {
		t.Errorf("--check after 0.1: err = %v, stderr:\n%s", err, errOut)
	}
	out, errOut, err = runThema(t, root, "", "lineage", "compile", "-l", filepath.Join(root, "ship"))
	if err != nil || !strings.Contains(out, "lineage.cue") {
		t.Errorf("compile of ship: out %q, err %v\n%s", out, err, errOut)
	}
	if text := readFile(t, ship); !strings.Contains(text, "crew?: int") {
		t.Errorf("ship's lineage.cue after 0.1:\n%s", text)
	}
}

// A lineage that does not bind fails, and the others are still written.
func TestLineageCompileFailure(t *testing.T) {
	root := writeCollection(t)
	broken := "Schema: {\n\tname: int\n}\n"
	if err := os.WriteFile(filepath.Join(root, "boat", "history", "0.1.thema"), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := runThema(t, root, "", "lineage", "compile", "-l", root)
	if err == nil || !strings.Contains(errOut, "E0215") {
		t.Errorf("err = %v, stderr:\n%s", err, errOut)
	}
	if _, serr := os.Stat(filepath.Join(root, "boat", "lineage.cue")); serr == nil {
		t.Error("boat, which does not bind, has a lineage.cue")
	}
	readFile(t, filepath.Join(root, "ship", "lineage.cue"))
}
