package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/internal/util"
	tload "github.com/sdboyer/thema/load"
	"github.com/spf13/cobra"
)

var setupLineageOnce sync.Once

// runLineageInit runs `thema lineage init` with the given args, and returns
// its output.
//
// A failing init exits the process, so only use this for inits that are
// expected to succeed.
func runLineageInit(t *testing.T, args ...string) string {
	t.Helper()
	setupLineageOnce.Do(func() { setupLineageCommand(rootCmd) })

	// The commands, and the state their flags are bound to, are package
	// globals; reset the flags so earlier runs don't leak into this one.
	resets := []struct {
		cmd        *cobra.Command
		flag, def  string
		persistent bool
	}{
		{initLineageOpenAPICmd, "src-subpath", "", false},
		{initLineageJSONSchemaCmd, "src-subpath", "", false},
	}
	for _, r := range resets {
		fs := r.cmd.Flags()
		if r.persistent {
			fs = r.cmd.PersistentFlags()
		}
		if err := fs.Set(r.flag, r.def); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(append([]string{"lineage", "init"}, args...))
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("lineage init %s failed: %s\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String()
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

// bindTip binds a lineage whose only file is tip, its tip.thema, on the
// development path, and returns it with its tip.
func bindTip(t *testing.T, tip string) (thema.Lineage, *authoring.Tip) {
	t.Helper()
	files := []authoring.File{{Path: authoring.TipFile, Text: []byte(tip)}}
	c, err := authoring.CompileDevelopment(ctx, "foo", files)
	if err != nil {
		t.Fatalf("generated tip does not compile: %s\n%s", err, tip)
	}
	lin, err := authoring.BindDevelopment(rt, c)
	if err != nil {
		t.Fatalf("generated tip does not bind: %s\n%s", err, tip)
	}
	return lin, c.Tip
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

// TestLineageInit checks that each kind of init prints the tip of a new
// lineage: formatted, binding on the development path as 0.0, with the
// schema init printed in the normal form before it printed the authoring
// form. normalForm is that earlier output, verbatim.
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
			out := runLineageInit(t, tc.args...)
			if out != tc.want {
				t.Fatalf("lineage init %s printed:\n%s\nwant:\n%s", strings.Join(tc.args, " "), out, tc.want)
			}
			formatted, err := authoring.Format([]byte(out))
			if err != nil {
				t.Fatalf("formatting the generated tip: %s", err)
			}
			if string(formatted) != out {
				t.Errorf("generated tip is not formatted; formatted, it is:\n%s", formatted)
			}

			lin, tip := bindTip(t, out)
			if got := tip.String(); got != "tip (cuts as 0.0)" {
				t.Errorf("generated tip is %s, want tip (cuts as 0.0)", got)
			}
			if v := lin.Latest().Version(); v != thema.SV(0, 0) {
				t.Errorf("generated tip binds as %s, want 0.0", v)
			}

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
			if string(tipText.Text) != out {
				t.Errorf("generated tip differs from the schema init printed in the normal form, which is:\n%s", tipText.Text)
			}
		})
	}
}
