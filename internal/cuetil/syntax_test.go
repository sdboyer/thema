package cuetil

import (
	"strings"
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

func TestComprehensionFieldPaths(t *testing.T) {
	ctx := cuecontext.New()
	v := ctx.CompileString(`
x: _
r: {
	a: 1
	if x.y != _|_ {
		b: 2
		n: {c: 3}
	}
	m: {
		if x.z == 1 {d: 4}
		e: 5
	}
}
`)
	var got []string
	for _, p := range ComprehensionFieldPaths(v.LookupPath(cue.ParsePath("r"))) {
		got = append(got, p.String())
	}
	want := []string{"b", "n", "n.c", "m.d"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}
