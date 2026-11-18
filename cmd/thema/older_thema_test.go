package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sdboyer/thema/internal/util"
)

// data validate loads a lineage whose module requires an older thema than
// this program embeds, against the embedded copy.
func TestDataValidateOlderThema(t *testing.T) {
	dir := t.TempDir()
	for name, text := range map[string]string{
		"mod/cue.mod/module.cue": fmt.Sprintf("module: %q\nlanguage: version: %q\ndeps: %q: {v: %q}\n",
			"example.com/older", util.LanguageVersion, util.ThemaModulePath, "v0.0.1"),
		"mod/x.cue": "package x\n\nimport \"github.com/sdboyer/thema\"\n\nlin: thema.Lineage & {\n\tname: \"x\"\n\tschemas: \"0\": \"0\": schema: a: string\n}\n",
		"data.json": "{\"a\": \"Argo\"}\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, errOut, err := runThema(t, dir, "", "data", "validate", "-l", "mod", "-p", "lin", "data.json")
	if err != nil {
		t.Fatalf("%v\nstdout: %s\nstderr: %s", err, out, errOut)
	}
}
