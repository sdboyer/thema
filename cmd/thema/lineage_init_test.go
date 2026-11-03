package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/util"
	tload "github.com/sdboyer/thema/load"
)

// Strings the init tests repeat. initDir is the lineage directory
// TestLineageInit writes, under a temporary directory it runs in.
const (
	initDir   = "foo"
	initName  = "init"
	nameFlag  = "--name"
	shipName  = "ship"
	nestedDir = "a/ship"
)

// runInit runs thema with args in dir, and fails the test if it fails.
func runInit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, errOut, err := runThema(t, dir, "", args...)
	if err != nil {
		t.Fatalf("thema %s failed: %s\n%s", strings.Join(args, " "), err, errOut)
	}
	return out
}

// initCodes runs thema with args in dir, expecting it to fail, and returns
// the codes of the diagnostics it reported.
func initCodes(t *testing.T, dir string, args ...string) []diag.Code {
	t.Helper()
	out, errOut, err := runThema(t, dir, "", append(args, "--diag-format", "json")...)
	if err == nil {
		t.Fatalf("thema %s succeeded:\n%s", strings.Join(args, " "), out)
	}
	var res struct {
		Diagnostics []diag.Diagnostic `json:"diagnostics"`
	}
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil {
		t.Fatalf("thema %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), jerr, out, errOut)
	}
	var codes []diag.Code
	for _, d := range res.Diagnostics {
		codes = append(codes, d.Code)
	}
	return codes
}

// bindInitDir binds the lineage in dir/name on the development path, as
// load.DevelopmentLineage binds it, and checks that it is a new lineage: named
// name, with a tip that cuts as 0.0.
func bindInitDir(t *testing.T, dir, name string) *tload.Development {
	t.Helper()
	d, err := tload.DevelopmentLineage(os.DirFS(dir), name, rt)
	if err != nil {
		t.Fatalf("the written lineage does not bind on the development path: %s", err)
	}
	if got := d.Tip.String(); got != "tip (cuts as 0.0)" {
		t.Errorf("the written tip is %s, want tip (cuts as 0.0)", got)
	}
	if got := d.Lineage.Name(); got != name {
		t.Errorf("the written lineage is named %s, want %s", got, name)
	}
	if v := d.Lineage.Latest().Version(); v != thema.SV(0, 0) {
		t.Errorf("the written lineage's latest version is %s, want 0.0", v)
	}
	return d
}

// bindNormalForm loads src as the sole file of package foo in a CUE module
// that depends on thema, and binds the lineage at its root.
func bindNormalForm(t *testing.T, src string) thema.Lineage {
	t.Helper()
	mfs := fstest.MapFS{
		"cue.mod/module.cue": &fstest.MapFile{Data: []byte(util.ModuleFile("example.com/foo"))},
		"foo.cue":            &fstest.MapFile{Data: []byte(src)},
	}
	binst, err := tload.InstanceWithThema(mfs, ".")
	if err != nil {
		t.Fatalf("generated lineage does not load: %s\n%s", err, src)
	}
	lin, err := thema.BindLineage(ctx.BuildInstance(binst), rt)
	if err != nil {
		t.Fatalf("generated lineage does not bind: %s\n%s", err, src)
	}
	return lin
}

func writeInput(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const initTestJSONSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "a": {"type": "string"},
    "b": {"type": "integer"}
  },
  "required": ["a"]
}`

const initTestOpenAPI = `{
  "openapi": "3.0.0",
  "info": {"title": "test", "version": "v1"},
  "paths": {},
  "components": {
    "schemas": {
      "Foo": {
        "type": "object",
        "properties": {
          "a": {"type": "string"},
          "b": {"type": "integer"}
        },
        "required": ["a"]
      }
    }
  }
}`

const openapiCmd = "openapi"

// TestLineageInit checks that each kind of init writes a new lineage's
// directory, whose tip is formatted, binds on the development path as 0.0,
// and holds the schema init printed in the normal form before it wrote the
// authoring form; normalForm is that earlier output, verbatim. With --stdout,
// init prints the same tip and writes nothing.
func TestLineageInit(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		want       string
		normalForm string
	}{
		{
			name: "empty",
			args: []string{"empty"},
			want: "Schema: {}\n",
			normalForm: `package foo

import "github.com/sdboyer/thema"

thema.Lineage
name: "foo"
schemas: "0": "0": schema: {}
`,
		},
		{
			name: "jsonschema",
			args: []string{"jsonschema", writeInput(t, "s.json", initTestJSONSchema)},
			want: "Schema: {\n\ta!: string\n\tb?: int\n}\n",
			normalForm: `package foo

import "github.com/sdboyer/thema"

thema.Lineage
name: "foo"
schemas: "0": "0": schema: {
	a!: string
	b?: int
}
`,
		},
		{
			name: openapiCmd,
			args: []string{openapiCmd, writeInput(t, "oapi.json", initTestOpenAPI)},
			want: "Schema: {\n\t#Foo: {\n\t\ta!: string\n\t\tb?: int\n\t\t...\n\t}\n}\n",
			normalForm: `package foo

import "github.com/sdboyer/thema"

thema.Lineage
name: "foo"
schemas: "0": "0": schema: #Foo: {
	a!: string
	b?: int
	...
}
`,
		},
		{
			name: "openapi subpath",
			args: []string{openapiCmd, "--src-subpath", "#Foo", writeInput(t, "oapi.json", initTestOpenAPI)},
			want: "Schema: {\n\t_#def\n\t_#def: {\n\t\ta!: string\n\t\tb?: int\n\t}\n}\n",
			normalForm: `package foo

import "github.com/sdboyer/thema"

thema.Lineage
name: "foo"
schemas: "0": "0": schema: {
	_#def
	_#def: {
		a!: string
		b?: int
	}
}
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			args := append([]string{lineageCmd, initName}, tc.args...)
			out := runInit(t, dir, append(args, "-l", initDir)...)
			tipPath := filepath.Join(initDir, authoring.TipFile)
			if out != tipPath+"\n" {
				t.Errorf("lineage init printed %q, want the path it wrote, %s", out, tipPath)
			}
			text, err := os.ReadFile(filepath.Join(dir, tipPath)) //nolint:gosec // a file under t.TempDir()
			if err != nil {
				t.Fatal(err)
			}
			tip := string(text)
			if tip != tc.want {
				t.Fatalf("lineage init %s wrote:\n%s\nwant:\n%s", strings.Join(tc.args, " "), tip, tc.want)
			}
			formatted, err := authoring.Format(text)
			if err != nil {
				t.Fatalf("formatting the generated tip: %s", err)
			}
			if string(formatted) != tip {
				t.Errorf("generated tip is not formatted; formatted, it is:\n%s", formatted)
			}
			bindInitDir(t, dir, initDir)

			// The earlier output's schema, decompiled, is the tip a lineage
			// has just after that schema was cut as 0.0.
			_, files, err := authoring.Decompile(bindNormalForm(t, tc.normalForm).Underlying())
			if err != nil {
				t.Fatal(err)
			}
			tipText := files[len(files)-1]
			if tipText.Path != authoring.TipFile {
				t.Fatalf("decompiling wrote %s last, want %s", tipText.Path, authoring.TipFile)
			}
			if string(tipText.Text) != tip {
				t.Errorf("generated tip differs from the schema init printed in the normal form, which is:\n%s", tipText.Text)
			}

			// --stdout prints the same tip, and writes nothing.
			empty := t.TempDir()
			if out := runInit(t, empty, append(args, "--stdout")...); out != tc.want {
				t.Errorf("lineage init %s --stdout printed:\n%s\nwant:\n%s", strings.Join(tc.args, " "), out, tc.want)
			}
			if ents, err := os.ReadDir(empty); err != nil || len(ents) != 0 {
				t.Errorf("lineage init --stdout wrote %v (%v)", ents, err)
			}
		})
	}
}

// TestLineageInitDir checks how init finds the new lineage's directory: a
// lineage's name is its directory's base name, so --name and -l can never
// disagree in what they write.
func TestLineageInitDir(t *testing.T) {
	cases := []struct {
		name string
		args []string
		// dir is the lineage directory written, relative to the directory
		// init runs in, and lin the lineage's name; code is the diagnostic
		// when init refuses, writing nothing.
		dir, lin string
		code     diag.Code
	}{
		{name: "-l", args: []string{"-l", shipName}, dir: shipName, lin: shipName},
		{name: "-l nested", args: []string{"-l", "a/b/ship"}, dir: "a/b/ship", lin: shipName},
		{name: "--name", args: []string{nameFlag, shipName}, dir: shipName, lin: shipName},
		{name: "-l and --name agree", args: []string{"-l", nestedDir, nameFlag, shipName}, dir: nestedDir, lin: shipName},
		{name: "current directory", dir: ".", lin: "cwd"},
		{name: "-l and --name disagree", args: []string{"-l", nestedDir, nameFlag, "boat"}, code: diag.E0620},
		{name: "--name is a path", args: []string{nameFlag, nestedDir}, code: diag.E0620},
		{name: "--stdout with -l", args: []string{"--stdout", "-l", shipName}, code: diag.E0620},
		{name: "--stdout with --name", args: []string{"--stdout", nameFlag, shipName}, code: diag.E0620},
		{name: "name not an identifier", args: []string{"-l", "my-ship"}, code: diag.E0137},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			// Init runs in cwd, so that the current directory has a name a
			// lineage may have.
			run := filepath.Join(root, "cwd")
			if err := os.Mkdir(run, 0o750); err != nil {
				t.Fatal(err)
			}
			args := append([]string{lineageCmd, initName, "empty"}, tc.args...)
			if tc.code != "" {
				if codes := initCodes(t, run, args...); len(codes) != 1 || codes[0] != tc.code {
					t.Errorf("lineage init %s reported %v, want %s", strings.Join(tc.args, " "), codes, tc.code)
				}
				if ents, err := os.ReadDir(run); err != nil || len(ents) != 0 {
					t.Errorf("lineage init %s wrote %v (%v)", strings.Join(tc.args, " "), ents, err)
				}
				return
			}
			out := runInit(t, run, args...)
			if want := filepath.Join(tc.dir, authoring.TipFile) + "\n"; out != want {
				t.Errorf("lineage init %s printed %q, want %q", strings.Join(tc.args, " "), out, want)
			}
			lindir := filepath.Clean(filepath.Join(run, tc.dir))
			bindInitDir(t, filepath.Dir(lindir), tc.lin)
		})
	}
}

// TestLineageInitRefusesLineage checks that init never writes into a
// directory that already holds a lineage's version files, and writes into
// one that holds anything else.
func TestLineageInitRefusesLineage(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		code  diag.Code
	}{
		{"tip", map[string]string{authoring.TipFile: cleanTip}, diag.E0623},
		{"history only", map[string]string{historyV00: cleanTip}, diag.E0623},
		{"other version file", map[string]string{"draft.thema": cleanTip}, diag.E0623},
		{"other files", map[string]string{"README.md": "ship\n", "ship.cue": "package ship\n"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			lin := filepath.Join(dir, shipName)
			for name, text := range tc.files {
				p := filepath.Join(lin, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{lineageCmd, initName, "empty", "-l", "ship"}
			if tc.code == "" {
				runInit(t, dir, args...)
				bindInitDir(t, dir, shipName)
				return
			}
			if codes := initCodes(t, dir, args...); len(codes) != 1 || codes[0] != tc.code {
				t.Errorf("lineage init reported %v, want %s", codes, tc.code)
			}
			for name, text := range tc.files {
				got, err := os.ReadFile(filepath.Join(lin, filepath.FromSlash(name))) //nolint:gosec // a file under t.TempDir()
				if err != nil || string(got) != text {
					t.Errorf("%s changed: %q (%v)", name, got, err)
				}
			}
		})
	}
}
