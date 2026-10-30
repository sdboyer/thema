package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runThema runs the thema CLI in dir with args, by re-executing this test
// binary, so each run starts with fresh flags. It returns stdout, stderr and
// the run's error.
func runThema(t *testing.T, dir string, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...) //nolint:gosec // re-executes this test binary
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), runMainEnv+"=1")
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), err
}

// writeTipDir writes a lineage with history/0.0.thema and the given tip to a
// temporary directory, and returns the directory.
func writeTipDir(t *testing.T, tip string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ship")
	if err := os.MkdirAll(filepath.Join(dir, "history"), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"history/0.0.thema": "Schema: {\n\tname: string\n}\n",
		"tip.thema":         tip,
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const (
	cleanTip = "Schema: {\n\tname: string\n}\n"
	minorTip = "Schema: {\n\tname:  string\n\tcrew?: int\n}\n"
	majorTip = "Schema: {\n\tname: string\n\tcrew: int\n}\n"
)

func TestLineageTip(t *testing.T) {
	cases := []struct {
		name, tip string
		check     bool
		out       string
		code      string // in stderr, when the run fails
	}{
		{"clean", cleanTip, false, "tip (clean at 0.0)\n", ""},
		{"clean, checked", cleanTip, true, "tip (clean at 0.0)\n", ""},
		{"minor", minorTip, false, "tip (cuts as 0.1)\n", ""},
		{"major", majorTip, false, "tip (cuts as 1.0)\n", ""},
		{"not clean, checked", minorTip, true, "tip (cuts as 0.1)\n", "E1003"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeTipDir(t, c.tip)
			args := []string{"lineage", "tip", "-l", dir}
			if c.check {
				args = append(args, "--check-clean")
			}
			out, errOut, err := runThema(t, dir, "", args...)
			if out != c.out {
				t.Errorf("stdout = %q, want %q", out, c.out)
			}
			if (c.code == "") != (err == nil) || !strings.Contains(errOut, c.code) {
				t.Errorf("err = %v, stderr:\n%s\nwant code %q", err, errOut, c.code)
			}
		})
	}
}

// thema data reads the history alone, and with --tip the tip too, which -v
// names tip and the output reports as the tip.
func TestDataTip(t *testing.T) {
	dir := writeTipDir(t, minorTip)
	doc := `{"name": "Argo", "crew": 3}`

	out, _, err := runThema(t, dir, doc, "data", "validate-any", "-l", dir)
	if err == nil {
		t.Errorf("without --tip, the history accepts crew: stdout %q", out)
	}

	out, errOut, err := runThema(t, dir, doc, "data", "validate-any", "-l", dir, "--tip")
	if err != nil {
		t.Fatalf("validate-any --tip: %v\n%s", err, errOut)
	}
	if out != "tip (cuts as 0.1)\n" {
		t.Errorf("validate-any --tip = %q, want the tip", out)
	}

	out, errOut, err = runThema(t, dir, doc, "data", "validate", "-l", dir, "--tip", "-v", "tip")
	if err != nil {
		t.Fatalf("validate --tip -v tip: %v\n%s\n%s", err, out, errOut)
	}

	out, errOut, err = runThema(t, dir, `{"name": "Argo"}`, "data", "translate", "-l", dir, "--tip", "--to", "0.1")
	if err != nil {
		t.Fatalf("translate --tip: %v\n%s", err, errOut)
	}
	if !strings.Contains(out, `"to": "tip (cuts as 0.1)"`) || !strings.Contains(out, `"from": "0.0"`) {
		t.Errorf("translate --tip output:\n%s", out)
	}
}

// A major tip without its ForwardLens binds with --tip, with a warning, and
// translating into it fails with E1002.
func TestDataTipMissingLens(t *testing.T) {
	dir := writeTipDir(t, majorTip)
	out, errOut, err := runThema(t, dir, `{"name": "Argo"}`, "data", "translate", "-l", dir, "--tip", "--to", "1.0")
	if err == nil {
		t.Fatalf("translating into the tip succeeded:\n%s", out)
	}
	for _, code := range []string{"E1001", "E1002"} {
		if !strings.Contains(errOut, code) {
			t.Errorf("stderr lacks %s:\n%s", code, errOut)
		}
	}
}

func TestDataTipTakesAuthoringForm(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ship.cue"), []byte(stdinTestLineage), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := runThema(t, dir, `{"a": "x"}`, "data", "validate-any", "-l", "ship.cue", "-p", "lin", "--tip")
	if err == nil || !strings.Contains(errOut, "E0620") {
		t.Errorf("err = %v, stderr:\n%s\nwant E0620", err, errOut)
	}
}
