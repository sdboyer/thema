package cuetil

import (
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

func TestListBuiltByComprehension(t *testing.T) {
	ctx := cuecontext.New()
	v := ctx.CompileString(`
x: _
plain: [1, 2]
comp: [if x.y != _|_ {1}]
forcomp: [for i in [1] {i}]
mixed: [1, if x.y != _|_ {2}]
`)
	for path, want := range map[string]bool{"plain": false, "comp": true, "forcomp": true, "mixed": true} {
		if got := ListBuiltByComprehension(v.LookupPath(cue.ParsePath(path))); got != want {
			t.Errorf("%s: got %v, want %v", path, got, want)
		}
	}
}
