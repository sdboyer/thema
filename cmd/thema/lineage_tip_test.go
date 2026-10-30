package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

}

// thema data translate names the tip "tip" in its JSON, with the version it
// cuts as beside it, and prints a translation that does not involve the tip
// as it would without --tip.
func TestDataTranslateTip(t *testing.T) {
	dir := writeTipDir(t, minorTip)
	cases := []struct {
		name, doc string
		args      []string
		want      map[string]any // the output's fields other than result and lacunas
	}{
		{"into the tip", `{"name": "Argo"}`, []string{"--tip", "--to", "0.1"},
			map[string]any{"from": "0.0", "to": "tip", "cutsAs": "0.1"}},
		{"into the tip, named tip", `{"name": "Argo"}`, []string{"--tip", "--to", "tip"},
			map[string]any{"from": "0.0", "to": "tip", "cutsAs": "0.1"}},
		{"out of the tip", `{"name": "Argo", "crew": 3}`, []string{"--tip", "--to", "0.0"},
			map[string]any{"from": "tip", "to": "0.0", "cutsAs": "0.1"}},
		{"within the history, with --tip", `{"name": "Argo"}`, []string{"--tip", "--to", "0.0"},
			map[string]any{"from": "0.0", "to": "0.0"}},
		{"without --tip", `{"name": "Argo"}`, []string{"--to", "0.0"},
			map[string]any{"from": "0.0", "to": "0.0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, errOut, err := runThema(t, dir, c.doc, append([]string{"data", "translate", "-l", dir}, c.args...)...)
			if err != nil {
				t.Fatalf("%v\n%s", err, errOut)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("output is not JSON: %v\n%s", err, out)
			}
			delete(got, "result")
			delete(got, "lacunas")
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v; output:\n%s", got, c.want, out)
			}
		})
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
