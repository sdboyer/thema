package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// writeAuthoringDir writes a two-version lineage in the authoring form to a
// temporary directory, with 1.0.thema out of layout, and returns the
// directory.
func writeAuthoringDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ship")
	if err := os.MkdirAll(filepath.Join(dir, "history"), 0o750); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"history/0.0.thema": "Schema: {\n\tbefore: string\n}\n",
		"1.0.thema":         "Schema: {after: string}\nForwardLens: {v0_0: _, v1_0: {after: v0_0.before}}\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// thema lineage fmt rewrites the files not in the one layout, and lists them.
func TestLineageFmt(t *testing.T) {
	setupLineageOnce.Do(func() { setupLineageCommand(rootCmd) })
	dir := writeAuthoringDir(t)
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetArgs([]string{"lineage", "fmt", "-l", dir})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "1.0.thema\n"; got != want {
		t.Errorf("listed %q, want %q", got, want)
	}
	got, err := os.ReadFile(filepath.Join(dir, "1.0.thema")) //nolint:gosec // a file this test wrote under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	want := "Schema: {\n\tafter: string\n}\n\nForwardLens: {\n\tv0_0: _\n\tv1_0: {\n\t\tafter: v0_0.before\n\t}\n}\n"
	if string(got) != want {
		t.Errorf("1.0.thema formatted as\n%s\nwant\n%s", got, want)
	}
}

// -l accepts a lineage's authoring directory, loading it through the compiler.
func TestLoadAuthoringDir(t *testing.T) {
	dir := writeAuthoringDir(t)
	lla := &lineageLoadArgs{inputLinFilePath: dir}
	dl, err := lla.dynLoad()
	if err != nil {
		t.Fatal(err)
	}
	if dl.lin.Name() != "ship" {
		t.Errorf("name = %q, want ship", dl.lin.Name())
	}
	if got := dl.sch.Version().String(); got != "1.0" {
		t.Errorf("latest = %s, want 1.0", got)
	}
}
