package load_test

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue/cuecontext"

	tload "github.com/sdboyer/thema/load"
)

// A module that requires thema's CUE at ThemaVersion loads, and one that
// requires any other version does not: ThemaVersion is the version a
// program writing a module.cue must use, without reaching into thema.
func TestThemaVersion(t *testing.T) {
	module := func(v string) fstest.MapFS {
		return fstest.MapFS{
			"cue.mod/module.cue": {Data: []byte(fmt.Sprintf("module: \"example.com/v\"\nlanguage: version: \"v0.18.0\"\ndeps: \"github.com/sdboyer/thema@v0\": v: %q\n", v))},
			"v.cue":              {Data: []byte("package v\n\nimport \"github.com/sdboyer/thema\"\n\nlin: thema.Lineage\n")},
		}
	}

	inst, err := tload.InstanceWithThema(module(tload.ThemaVersion), ".")
	if err != nil {
		t.Fatal(err)
	}
	if err := cuecontext.New().BuildInstance(inst).Err(); err != nil {
		t.Fatalf("requiring ThemaVersion: %v", err)
	}

	inst, err = tload.InstanceWithThema(module("v0.0.1"), ".")
	if err == nil {
		err = cuecontext.New().BuildInstance(inst).Err()
	}
	if err == nil {
		t.Fatal("requiring v0.0.1 loaded")
	}
	// The loader flattens thema's refusal into its own text; the refusal
	// names the version to require.
	if want := fmt.Sprintf("to %q in cue.mod/module.cue", tload.ThemaVersion); !strings.Contains(err.Error(), want) {
		t.Fatalf("requiring v0.0.1: want a refusal naming %s, got %v", tload.ThemaVersion, err)
	}
}
