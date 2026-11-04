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

	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
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
		historyV00:        "Schema: {\n\tname: string\n}\n",
		authoring.TipFile: tip,
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Strings the CLI tests repeat.
const (
	historyV00    = "history/0.0.thema"
	minorTipLabel = "tip (cuts as 0.1)\n"
	tipFlag       = "--tip"
	v00           = "0.0"
	lineageCmd    = "lineage"
	tipName       = "tip"
	toFlag        = "--to"
	cutsAsKey     = "cutsAs"
	fromKey       = "from"
	toKey         = "to"
	v01           = "0.1"
	argo          = `{"name": "Argo"}`
)

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
		{"minor", minorTip, false, minorTipLabel, ""},
		{"major", majorTip, false, "tip (cuts as 1.0)\n", ""},
		{"not clean, checked", minorTip, true, minorTipLabel, "E1003"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeTipDir(t, c.tip)
			args := []string{lineageCmd, tipName, "-l", dir}
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

// lineage tip and lineage cut position their diagnostics relative to the
// working directory, naming the lineage's directory as -l gave it, and show
// the excerpt from there.
func TestLineageTipCutPositions(t *testing.T) {
	// The tip removes name and writes no lens for it, so it is neither clean
	// nor ready to cut.
	dir := writeTipDir(t, "Schema: {\n\tcrew: int\n}\n")
	for _, args := range [][]string{
		{lineageCmd, tipName, "-l", shipName, "--check-clean"},
		{lineageCmd, "cut", "-l", shipName},
	} {
		_, errOut, err := runThema(t, filepath.Dir(dir), "", args...)
		if err == nil {
			t.Fatalf("%v succeeded", args)
		}
		for _, want := range []string{"--> ship/tip.thema:", "1 | Schema: {"} {
			if !strings.Contains(errOut, want) {
				t.Errorf("%v: stderr lacks %q:\n%s", args, want, errOut)
			}
		}
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

	out, errOut, err := runThema(t, dir, doc, "data", "validate-any", "-l", dir, tipFlag)
	if err != nil {
		t.Fatalf("validate-any --tip: %v\n%s", err, errOut)
	}
	if out != minorTipLabel {
		t.Errorf("validate-any --tip = %q, want the tip", out)
	}

	out, errOut, err = runThema(t, dir, doc, "data", "validate", "-l", dir, tipFlag, "-v", tipName)
	if err != nil {
		t.Fatalf("validate --tip -v tip: %v\n%s\n%s", err, out, errOut)
	}
}

// thema data translate names the tip tipName in its JSON, with the version it
// cuts as beside it, and prints a translation that does not involve the tip
// as it would without --tip.
func TestDataTranslateTip(t *testing.T) {
	dir := writeTipDir(t, minorTip)
	cases := []struct {
		name, doc string
		args      []string
		want      map[string]any // the output's fields other than result and lacunas
	}{
		{"into the tip", argo, []string{tipFlag, toFlag, v01},
			map[string]any{fromKey: v00, toKey: tipName, cutsAsKey: v01}},
		{"into the tip, named tip", argo, []string{tipFlag, toFlag, tipName},
			map[string]any{fromKey: v00, toKey: tipName, cutsAsKey: v01}},
		{"out of the tip", `{"name": "Argo", "crew": 3}`, []string{tipFlag, toFlag, v00},
			map[string]any{fromKey: tipName, toKey: v00, cutsAsKey: v01}},
		{"within the history, with --tip", argo, []string{tipFlag, toFlag, v00},
			map[string]any{fromKey: v00, toKey: v00}},
		{"without --tip", argo, []string{toFlag, v00},
			map[string]any{fromKey: v00, toKey: v00}},
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
	out, errOut, err := runThema(t, dir, argo, "data", "translate", "-l", dir, tipFlag, toFlag, "1.0")
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
	_, errOut, err := runThema(t, dir, `{"a": "x"}`, "data", "validate-any", "-l", "ship.cue", "-p", "lin", tipFlag)
	if err == nil || !strings.Contains(errOut, "E0620") {
		t.Errorf("err = %v, stderr:\n%s\nwant E0620", err, errOut)
	}
}

// thema lineage gen gotypes prints Go types for every schema, or for the one
// -v names.
func TestLineageGenGoTypes(t *testing.T) {
	const (
		shipType = "type Ship struct {\n\tName string `json:\"name\"`\n}\n"
		pkgname  = "ships"
		types00  = "v0_0/types.go"
		pkgFlag  = "--pkgname"
		checkArg = "--check"
	)
	dir := writeTipDir(t, cleanTip)
	gen := []string{lineageCmd, "gen", "gotypes", "-l", dir}

	// Without -o, the files are printed as a txtar archive.
	for _, tc := range []struct {
		args []string
		file string
		pkg  string
	}{
		{nil, types00, "v0_0"},
		{[]string{"-v", v00, pkgFlag, pkgname}, "types.go", pkgname},
	} {
		out, errOut, err := runThema(t, dir, "", append(gen, tc.args...)...)
		if err != nil {
			t.Fatalf("gen gotypes %v: %v\n%s", tc.args, err, errOut)
		}
		a := txtar.Parse([]byte(out))
		if len(a.Files) != 1 || a.Files[0].Name != tc.file {
			t.Fatalf("gen gotypes %v printed %d files, want %s alone:\n%s", tc.args, len(a.Files), tc.file, out)
		}
		for _, want := range []string{"package " + tc.pkg + "\n", shipType} {
			if !strings.Contains(string(a.Files[0].Data), want) {
				t.Errorf("gen gotypes %v: %s lacks %q:\n%s", tc.args, tc.file, want, a.Files[0].Data)
			}
		}
	}

	// -o writes the files, which --check then finds up to date.
	outdir := filepath.Join(t.TempDir(), "gen")
	if _, errOut, err := runThema(t, dir, "", append(gen, "-o", outdir)...); err != nil {
		t.Fatalf("gen gotypes -o: %v\n%s", err, errOut)
	}
	written := filepath.Join(outdir, filepath.FromSlash(types00))
	b, err := os.ReadFile(written) //nolint:gosec // a path the test chose
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), shipType) {
		t.Errorf("gen gotypes -o wrote:\n%s", b)
	}
	if _, errOut, err := runThema(t, dir, "", append(gen, "-o", outdir, checkArg)...); err != nil {
		t.Errorf("gen gotypes --check of files it just wrote: %v\n%s", err, errOut)
	}

	for _, tc := range []struct {
		name string
		args []string
		code diag.Code
	}{
		{"--pkgname without -v", []string{pkgFlag, pkgname}, diag.E0620},
		{"--pkgname not an identifier", []string{"-v", v00, pkgFlag, "my-ships"}, diag.E0620},
		{"--pkgname a keyword", []string{"-v", v00, pkgFlag, "func"}, diag.E0620},
		{"--check without -o", []string{checkArg}, diag.E0620},
		{"-o a file", []string{"-o", written}, diag.E0616},
		{"--check of -o a file", []string{"-o", written, checkArg}, diag.E0628},
		{"--check of a changed file", []string{"-o", outdir, checkArg}, diag.E0628},
	} {
		if tc.code == diag.E0628 {
			if err := os.WriteFile(written, []byte("package v0_0\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		_, errOut, err := runThema(t, dir, "", append(gen, tc.args...)...)
		if err == nil || !strings.Contains(errOut, "error["+string(tc.code)+"]") {
			t.Errorf("%s: got %v, want %s in:\n%s", tc.name, err, tc.code, errOut)
		}
	}
}
