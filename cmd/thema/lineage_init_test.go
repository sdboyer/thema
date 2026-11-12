package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
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
	stdoutFlg = "--stdout"
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

// initCodes runs thema with args in dir, expecting it to fail as report
// reports a failure with --diag-format json: exit status 1, nothing on
// stderr, and diagnostics on stdout. It returns their codes.
func initCodes(t *testing.T, dir string, args ...string) []diag.Code {
	t.Helper()
	out, errOut, err := runThema(t, dir, "", append(args, "--diag-format", "json")...)
	line := strings.Join(args, " ")
	if err == nil {
		t.Fatalf("thema %s succeeded:\n%s", line, out)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("thema %s: %v, want exit status 1\nstdout:\n%s\nstderr:\n%s", line, err, out, errOut)
	}
	if errOut != "" {
		t.Errorf("thema %s wrote to stderr, where --diag-format json writes nothing:\n%s", line, errOut)
	}
	var res struct {
		Diagnostics []diag.Diagnostic `json:"diagnostics"`
	}
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil {
		t.Fatalf("thema %s: %v\nstdout:\n%s\nstderr:\n%s", line, jerr, out, errOut)
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
	lin, err := thema.BindLineage(ctx.BuildInstance(binst), cue.Path{}, rt)
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
			want: "Schema: {\n\ta!: string\n\tb?: int\n\t...\n}\n",
			normalForm: `package foo

import "github.com/sdboyer/thema"

thema.Lineage
name: "foo"
schemas: "0": "0": schema: {
	a!: string
	b?: int
	...
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
			if out := runInit(t, empty, append(args, stdoutFlg)...); out != tc.want {
				t.Errorf("lineage init %s --stdout printed:\n%s\nwant:\n%s", strings.Join(tc.args, " "), out, tc.want)
			}
			if ents, err := os.ReadDir(empty); err != nil || len(ents) != 0 {
				t.Errorf("lineage init --stdout wrote %v (%v)", ents, err)
			}
		})
	}
}

const initTestOpenAPIRefs = `{
  "openapi": "3.0.0",
  "info": {"title": "test", "version": "v1"},
  "paths": {},
  "components": {
    "schemas": {
      "Foo": {
        "type": "object",
        "properties": {
          "a": {"type": "string"},
          "closed": {"type": "object", "additionalProperties": false, "properties": {"c": {"type": "string"}}},
          "bar": {"$ref": "#/components/schemas/Bar"}
        },
        "required": ["a"]
      },
      "Bar": {"type": "object", "properties": {"z": {"type": "string"}}}
    }
  }
}`

// TestLineageInitOpenAPISubpath checks that init openapi --src-subpath makes
// the schema it points at the lineage's schema: a regular value, open or
// closed as the document says, not the definition openapi.Extract makes of a
// component, which CUE exports wrapped as {_#def, _#def: ...}.
func TestLineageInitOpenAPISubpath(t *testing.T) {
	dir := t.TempDir()
	in := writeInput(t, "oapi.json", initTestOpenAPIRefs)
	runInit(t, dir, lineageCmd, initName, openapiCmd, in, "--src-subpath", "#/components/schemas/Foo", "-l", initDir)
	text, err := os.ReadFile(filepath.Join(dir, initDir, authoring.TipFile)) //nolint:gosec // a file under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	want := `Schema: {
	a!:      string
	closed?: close({c?: string})
	bar?:    #Bar
	#Bar: {
		z?: string
		...
	}
	...
}
`
	if string(text) != want {
		t.Errorf("lineage init openapi --src-subpath wrote:\n%s\nwant:\n%s", text, want)
	}

	sch := bindInitDir(t, dir, initDir).Lineage.Latest()
	v := sch.Underlying().LookupPath(cue.MakePath(cue.Str("schema")))
	if v.IsClosed() {
		t.Error("the bound schema is closed; the OpenAPI schema allows other properties")
	}
	iter, err := v.Fields(cue.Hidden(true), cue.Definitions(true), cue.Optional(true))
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	for iter.Next() {
		fields = append(fields, iter.Selector().String())
	}
	if got := strings.Join(fields, ","); got != "a!,closed?,bar?,#Bar" {
		t.Errorf("the bound schema has fields %s, want a!,closed?,bar?,#Bar", got)
	}

	for _, tc := range []struct {
		data  string
		valid bool
	}{
		{`{"a": "x"}`, true},
		{`{"a": "x", "other": 1}`, true},
		{`{"a": "x", "bar": {"z": "y", "other": 1}}`, true},
		{`{"a": "x", "closed": {"c": "y"}}`, true},
		{`{"a": "x", "closed": {"c": "y", "other": 1}}`, false},
		{`{}`, false},
	} {
		_, err := sch.Validate(ctx.CompileString(tc.data))
		if (err == nil) != tc.valid {
			t.Errorf("validating %s: %v, want valid %v", tc.data, err, tc.valid)
		}
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
		{name: "--stdout with -l", args: []string{stdoutFlg, "-l", shipName}, code: diag.E0620},
		{name: "--stdout with --name", args: []string{stdoutFlg, nameFlag, shipName}, code: diag.E0620},
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

// TestLineageInitNameRendered checks that a name init's bind rejects is
// rendered against the tip init would have written, which is not on disk,
// with help naming the directory the name came from, and that nothing is
// written.
func TestLineageInitNameRendered(t *testing.T) {
	dir := t.TempDir()
	out, errOut, err := runThema(t, dir, "", lineageCmd, initName, "empty", "-l", "my-ship")
	if err == nil {
		t.Fatalf("lineage init -l my-ship succeeded:\n%s", out)
	}
	want := `error[E0137]: the lineage's name is "my-ship", which is not an identifier
 --> my-ship/tip.thema:1:1
  |
1 | Schema: {}
  | ^^^^^^
  |
  = detail: name: invalid value "my-ship" (out of bound =~"^[A-Za-z][A-Za-z0-9_]*$")
  = help: a name starts with a letter and continues with letters and digits
  = help: the name is the base name of the directory init was given, my-ship; nothing was written
`
	if errOut != want {
		t.Errorf("lineage init -l my-ship reported:\n%s\nwant:\n%s", errOut, want)
	}
	if ents, err := os.ReadDir(dir); err != nil || len(ents) != 0 {
		t.Errorf("lineage init -l my-ship wrote %v (%v)", ents, err)
	}
}

// TestLineageInitInput checks how init reads its input document: from a
// file, by its extension, or from stdin, as JSON or YAML; and that it
// reports an input it cannot convert.
func TestLineageInitInput(t *testing.T) {
	const fooSchema = "Schema: {\n\ta!: string\n\tb?: int\n\t...\n}\n"
	yamlDoc := `openapi: "3.0.0"
info: {title: test, version: v1}
paths: {}
components:
  schemas:
    Foo:
      type: object
      properties:
        a: {type: string}
        b: {type: integer}
      required: [a]
`
	swagger := `{"swagger": "2.0", "info": {"title": "t", "version": "v"}, "paths": {}, "definitions": {}}`
	unknown := `{"openapi": "4.0.0", "info": {"title": "t", "version": "v"}, "paths": {}}`
	subpath := []string{"--src-subpath", "#/components/schemas/Foo", stdoutFlg}
	cases := []struct {
		name  string
		args  []string
		stdin string
		want  string
		code  diag.Code
	}{
		{name: "stdin JSON", args: subpath, stdin: initTestOpenAPI, want: fooSchema},
		{name: "stdin YAML", args: subpath, stdin: yamlDoc, want: fooSchema},
		{name: "YAML file", args: append([]string{writeInput(t, "oapi.yaml", yamlDoc)}, subpath...), want: fooSchema},
		{name: "stdin neither", args: subpath, stdin: "openapi: [\n", code: diag.E0611},
		{name: "CUE file", args: append([]string{writeInput(t, "oapi.cue", "openapi: \"3.0.0\"\n")}, subpath...), code: diag.E0614},
		{name: "no version", args: []string{writeInput(t, "swagger.json", swagger), stdoutFlg}, code: diag.E0624},
		{name: "no version subpath", args: append([]string{writeInput(t, "swagger.json", swagger)}, subpath...), code: diag.E0624},
		{name: "unknown version", args: []string{writeInput(t, "v4.json", unknown), stdoutFlg}, code: diag.E0624},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{lineageCmd, initName, openapiCmd}, tc.args...)
			if tc.code == "" {
				out, errOut, err := runThema(t, t.TempDir(), tc.stdin, args...)
				if err != nil {
					t.Fatalf("%v\n%s", err, errOut)
				}
				if out != tc.want {
					t.Errorf("printed:\n%s\nwant:\n%s", out, tc.want)
				}
				return
			}
			out, errOut, err := runThema(t, t.TempDir(), tc.stdin, append(args, "--diag-format", "json")...)
			if err == nil {
				t.Fatalf("succeeded:\n%s", out)
			}
			var res struct {
				Diagnostics []diag.Diagnostic `json:"diagnostics"`
			}
			if jerr := json.Unmarshal([]byte(out), &res); jerr != nil {
				t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", jerr, out, errOut)
			}
			if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != tc.code {
				t.Errorf("reported %+v, want %s", res.Diagnostics, tc.code)
			}
		})
	}
}

// TestLineageInitJSONSchemaSubpath checks that init jsonschema --src-subpath
// makes the one schema it points at the lineage's schema, with the schemas
// it refers to as definitions beside it, rather than taking each member of
// that schema to be a schema.
func TestLineageInitJSONSchemaSubpath(t *testing.T) {
	doc := `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$defs": {
    "ship": {
      "type": "object",
      "properties": {"name": {"type": "string"}, "crew": {"$ref": "#/$defs/person"}},
      "required": ["name"]
    },
    "person": {"type": "object", "properties": {"n": {"type": "string"}}}
  },
  "type": "object",
  "properties": {"a": {"type": "string"}}
}`
	dir := t.TempDir()
	runInit(t, dir, lineageCmd, initName, "jsonschema", writeInput(t, "s.json", doc), "--src-subpath", "#/$defs/ship", "-l", initDir)
	v := bindInitDir(t, dir, initDir).Lineage.Latest().Underlying().LookupPath(cue.MakePath(cue.Str("schema")))
	iter, err := v.Fields(cue.Hidden(true), cue.Definitions(true), cue.Optional(true))
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	for iter.Next() {
		fields = append(fields, iter.Selector().String())
	}
	if got := strings.Join(fields, ","); got != "name!,crew?,#person" {
		t.Errorf("the bound schema has fields %s, want name!,crew?,#person", got)
	}
}

const initTestJSONSchemaOpenness = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$defs": {
    "ship": {
      "type": "object",
      "properties": {
        "name": {"type": "string"},
        "deck": {"type": "object", "properties": {"d": {"type": "integer"}}},
        "hold": {"type": "object", "additionalProperties": false, "properties": {"c": {"type": "string"}}},
        "crew": {"$ref": "#/$defs/crew"}
      },
      "required": ["name"]
    },
    "crew": {"type": "object", "additionalProperties": false, "properties": {"n": {"type": "string"}}}
  },
  "type": "object",
  "properties": {
    "a": {"type": "string"},
    "inner": {"type": "object", "properties": {"x": {"type": "integer"}}},
    "shut": {"type": "object", "additionalProperties": false, "properties": {"y": {"type": "string"}}}
  },
  "required": ["a"]
}`

// TestLineageInitJSONSchemaOpenness checks that init jsonschema keeps the
// document's openness in the bound schema, which Validate closes: an object
// allows other properties unless its additionalProperties is false, at the
// root and nested, for the whole document and for the one schema
// --src-subpath points at.
func TestLineageInitJSONSchemaOpenness(t *testing.T) {
	type check struct {
		data  string
		valid bool
	}
	cases := []struct {
		name    string
		subpath string
		checks  []check
	}{
		{
			name: "document",
			checks: []check{
				{`{"a": "x"}`, true},
				{`{"a": "x", "other": 1}`, true},
				{`{"a": "x", "inner": {"x": 1, "other": 1}}`, true},
				{`{"a": "x", "shut": {"y": "z"}}`, true},
				{`{"a": "x", "shut": {"y": "z", "other": 1}}`, false},
				{`{}`, false},
			},
		},
		{
			name:    shipName,
			subpath: "#/$defs/ship",
			checks: []check{
				{`{"name": "x"}`, true},
				{`{"name": "x", "other": 1}`, true},
				{`{"name": "x", "deck": {"d": 1, "other": 1}}`, true},
				{`{"name": "x", "hold": {"c": "y"}}`, true},
				{`{"name": "x", "hold": {"c": "y", "other": 1}}`, false},
				{`{"name": "x", "crew": {"n": "y"}}`, true},
				{`{"name": "x", "crew": {"n": "y", "other": 1}}`, false},
			},
		},
		{
			name:    "closed",
			subpath: "#/$defs/crew",
			checks: []check{
				{`{"n": "y"}`, true},
				{`{"n": "y", "other": 1}`, false},
			},
		},
	}
	in := writeInput(t, "s.json", initTestJSONSchemaOpenness)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			args := []string{lineageCmd, initName, "jsonschema", in, "-l", initDir}
			if tc.subpath != "" {
				args = append(args, "--src-subpath", tc.subpath)
			}
			runInit(t, dir, args...)
			text, err := os.ReadFile(filepath.Join(dir, initDir, authoring.TipFile)) //nolint:gosec // a file under t.TempDir()
			if err != nil {
				t.Fatal(err)
			}
			formatted, err := authoring.Format(text)
			if err != nil {
				t.Fatalf("formatting the generated tip: %s", err)
			}
			if string(formatted) != string(text) {
				t.Errorf("generated tip is not formatted; formatted, it is:\n%s", formatted)
			}
			sch := bindInitDir(t, dir, initDir).Lineage.Latest()
			for _, c := range tc.checks {
				_, err := sch.Validate(ctx.CompileString(c.data))
				if (err == nil) != c.valid {
					t.Errorf("validating %s: %v, want valid %v\ntip:\n%s", c.data, err, c.valid, text)
				}
			}
		})
	}
}
