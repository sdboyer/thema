package load

import (
	"fmt"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema/internal/util"
)

const modFilePath = "cue.mod/module.cue"

// A module that requires an older thema than the embedded one loads through
// InstanceWithThema, against the embedded copy: a name that copy's
// thema.Lineage refuses is refused, and one it accepts is not.
func TestInstanceWithThemaOlder(t *testing.T) {
	fsys := fstest.MapFS{
		modFilePath: {Data: []byte(fmt.Sprintf("module: %q\nlanguage: version: %q\ndeps: %q: v: %q\n",
			"example.com/ship", util.LanguageVersion, util.ThemaModulePath, "v0.0.1"))},
		"ship.cue": {Data: []byte("package ship\n\nimport \"github.com/sdboyer/thema\"\n\nlin: thema.Lineage & {name: \"ship\"}\nbad: thema.Lineage & {name: \"9ship\"}\n")},
	}
	inst, err := InstanceWithThema(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	v := cuecontext.New().BuildInstance(inst)
	if err := v.LookupPath(cue.ParsePath("lin")).Validate(); err != nil {
		t.Errorf("lin: %v", err)
	}
	if err := v.LookupPath(cue.ParsePath("bad")).Validate(); err == nil {
		t.Error("bad's name was accepted; thema.Lineage didn't resolve")
	}
}
