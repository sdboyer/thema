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
	"github.com/sdboyer/thema/internal/util"
	tload "github.com/sdboyer/thema/load"
	"github.com/spf13/cobra"
)

var setupLineageOnce sync.Once

// runLineageInit runs `thema lineage init` with the given args, naming the
// lineage "foo", and returns its output.
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
		{initLineageCmd, "name", "", true},
		{initLineageCmd, "package-name", "", true},
		{initLineageCmd, "no-package", "false", true},
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
	rootCmd.SetArgs(append([]string{"lineage", "init"}, append(args, "--name", "foo")...))
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("lineage init %s failed: %s\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String()
}

// bindGenerated loads src as the sole file of package foo in a CUE module that
// depends on thema, and binds the lineage at its root.
func bindGenerated(t *testing.T, src string) thema.Lineage {
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

func TestLineageInitEmpty(t *testing.T) {
	out := runLineageInit(t, "empty")
	if !strings.HasPrefix(out, "package foo\n") {
		t.Fatalf("expected package foo, got:\n%s", out)
	}
	lin := bindGenerated(t, out)
	if _, err := lin.Latest().Validate(ctx.CompileString(`{}`)); err != nil {
		t.Fatalf("expected empty schema to accept {}: %s", err)
	}
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

func TestLineageInitJSONSchema(t *testing.T) {
	out := runLineageInit(t, "jsonschema", writeInput(t, "s.json", initTestJSONSchema))
	if !strings.HasPrefix(out, "package foo\n") {
		t.Fatalf("expected package foo, got:\n%s", out)
	}
	sch := bindGenerated(t, out).Latest()
	if _, err := sch.Validate(ctx.CompileString(`{a: "x", b: 1}`)); err != nil {
		t.Errorf("expected valid data to validate: %s", err)
	}
	if _, err := sch.Validate(ctx.CompileString(`{b: 1}`)); err == nil {
		t.Errorf("expected data missing required field a to fail validation")
	}
}

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

func TestLineageInitOpenAPI(t *testing.T) {
	out := runLineageInit(t, "openapi", "--src-subpath", "#Foo", writeInput(t, "oapi.json", initTestOpenAPI))
	if !strings.HasPrefix(out, "package foo\n") {
		t.Fatalf("expected package foo, got:\n%s", out)
	}
	sch := bindGenerated(t, out).Latest()
	if _, err := sch.Validate(ctx.CompileString(`{a: "x", b: 1}`)); err != nil {
		t.Errorf("expected valid data to validate: %s", err)
	}
	if _, err := sch.Validate(ctx.CompileString(`{a: 1}`)); err == nil {
		t.Errorf("expected data with wrongly typed field a to fail validation")
	}
}
