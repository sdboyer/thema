package compile

import (
	"slices"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/google/go-cmp/cmp"
)

// An empty nested body the author writes for a struct the source lacks is
// walked for what the struct requires, so that a missing field is reported
// inside it, but it writes nothing: the normal form is what it would be
// without the body, and decompiling and recompiling it gives it back. A round
// trip fixture cannot hold this input, since decompiling omits the empty body.
func TestEmptyBodyWritesNothing(t *testing.T) {
	files := []File{
		{Path: "history/0.0.thema", Text: []byte("Schema: {\n\tid: string\n}\n")},
		{Path: "history/1.0.thema", Text: []byte(`Schema: {
	name: string
	meta: {
		note?: string
	}
}

ForwardLens: {
	v0_0: _
	v1_0: {
		name: v0_0.id
		meta: {}
	}
}
`)},
		{Path: TipFile, Text: []byte("Schema: {\n\tname: string\n\tmeta: {\n\t\tnote?: string\n\t}\n}\n")},
	}
	ctx := cuecontext.New()
	c, err := Compile(ctx, "ship", files)
	if err != nil {
		t.Fatal(err)
	}
	text, err := c.Text()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "meta: {}") {
		t.Errorf("the empty body is in the normal form:\n%s", text)
	}
	name, dfiles, err := Decompile(ctx.BuildFile(c.File))
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Compile(ctx, name, dfiles)
	if err != nil {
		t.Fatal(err)
	}
	text2, err := c2.Text()
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(text, text2); d != "" {
		t.Errorf("recompiled normal form differs (-first +recompiled):\n%s", d)
	}
}

// A help that offers fields to write offers them as CUE writes them: a field
// beneath another inside its struct, never by a dotted path, which CUE does
// not parse as a label. What nested renders parses back to the fields it was
// given, beneath the struct it was given.
func TestNested(t *testing.T) {
	p := func(s string) cue.Path { return cue.ParsePath(s) }
	cases := []struct {
		name  string
		base  cue.Path
		paths []cue.Path
		want  string
	}{
		{"top level", cue.MakePath(), []cue.Path{p("a")}, "a: 1"},
		{"beneath a struct", cue.MakePath(), []cue.Path{p("meta.owner")}, "meta: {owner: 1}"},
		{"inside the struct written", p("meta"), []cue.Path{p("meta.owner")}, "owner: 1"},
		{"siblings share their struct", cue.MakePath(), []cue.Path{p("meta.owner"), p("id"), p("meta.team")}, "meta: {owner: 1, team: 1}, id: 1"},
		{"deeper", p("a"), []cue.Path{p("a.b.c"), p("a.b.d.e")}, "b: {c: 1, d: {e: 1}}"},
		{"a key that is not an identifier", cue.MakePath(), []cue.Path{cue.MakePath(cue.Str("x-y"), cue.Str("z"))}, `"x-y": {z: 1}`},
		// Writing meta.team writes meta, so meta needs no value of its own.
		{"a path beneath another", cue.MakePath(), []cue.Path{p("meta"), p("meta.team")}, "meta: {team: 1}"},
	}
	ctx := cuecontext.New()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := nested(c.base, "1", c.paths...)
			if got != c.want {
				t.Errorf("nested = %q, want %q", got, c.want)
			}
			v := ctx.CompileString("{" + got + "}")
			if v.Err() != nil {
				t.Fatalf("%q does not compile as CUE: %v", got, v.Err())
			}
			var want, back []string
			for _, q := range c.paths {
				// A path another lies beneath is written by it, not as a leaf.
				if slices.ContainsFunc(c.paths, func(r cue.Path) bool {
					return len(r.Selectors()) > len(q.Selectors()) && strings.HasPrefix(r.String(), q.String()+".")
				}) {
					continue
				}
				want = append(want, cue.MakePath(q.Selectors()[len(c.base.Selectors()):]...).String())
			}
			var walk func(v cue.Value, at cue.Path)
			walk = func(v cue.Value, at cue.Path) {
				iter, err := v.Fields()
				if err != nil || !isStruct(v) {
					back = append(back, at.String())
					return
				}
				for iter.Next() {
					walk(iter.Value(), appendPath(at, iter.Selector()))
				}
			}
			walk(v, cue.MakePath())
			slices.Sort(want)
			slices.Sort(back)
			if d := cmp.Diff(want, back); d != "" {
				t.Errorf("%q parses back to other fields (-given +parsed):\n%s", got, d)
			}
		})
	}
}
