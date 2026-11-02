package compile

import (
	"fmt"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema/internal/cuetil"
)

// TestLookupPathFieldForms pins what cue.Value.LookupPath finds for each form
// of field, with each form of selector. The compiler looks schema paths up to
// learn what a version declares, so the selector it uses decides which fields
// it can see at all: a plain selector (cue.Str, cue.ParsePath) finds only
// regular fields, not optional or required ones, while an optional selector
// finds all three. The compiler's lookup therefore uses optional selectors.
func TestLookupPathFieldForms(t *testing.T) {
	v := cuecontext.New().CompileString(`
regular:   int
optional?: int
required!: int
nested: {
	optional?: int
}
`)
	sels := []struct {
		name string
		sel  func(string) cue.Selector
	}{
		{"plain", cue.Str},
		{"optional", func(s string) cue.Selector { return cue.Str(s).Optional() }},
		{"required", func(s string) cue.Selector { return cue.Str(s).Required() }},
	}
	fields := []string{"regular", "optional", "required"}
	var b strings.Builder
	for _, s := range sels {
		for _, f := range fields {
			fmt.Fprintf(&b, "%s selector, %s field: exists=%v\n", s.name, f, v.LookupPath(cue.MakePath(s.sel(f))).Exists())
		}
		fmt.Fprintf(&b, "%s selector, nested.optional: exists=%v\n", s.name, v.LookupPath(cue.MakePath(cue.Str("nested"), s.sel(fields[1]))).Exists())
	}
	want := `plain selector, regular field: exists=true
plain selector, optional field: exists=false
plain selector, required field: exists=false
plain selector, nested.optional: exists=false
optional selector, regular field: exists=true
optional selector, optional field: exists=true
optional selector, required field: exists=true
optional selector, nested.optional: exists=true
required selector, regular field: exists=true
required selector, optional field: exists=true
required selector, required field: exists=true
required selector, nested.optional: exists=true
`
	if got := b.String(); got != want {
		t.Errorf("LookupPath:\n%s\nwant:\n%s", got, want)
	}
}

// TestLookupOpenStruct pins that LookupPath finds any name in an open struct,
// as _, while lookup finds only the names the struct declares: a name its ...
// merely admits is not declared (#158, #159).
func TestLookupOpenStruct(t *testing.T) {
	v := cuecontext.New().CompileString(`{
	declared?: int
	...
}`)
	admitted := cue.MakePath(cue.Str("admitted").Optional())
	if got := v.LookupPath(admitted); !got.Exists() || got.IncompleteKind() != cue.TopKind {
		t.Errorf("LookupPath of a name only ... admits: exists=%v kind=%v, want an existing _", got.Exists(), got.IncompleteKind())
	}
	if lookup(v, cue.ParsePath("admitted")).Exists() {
		t.Error("lookup found a name only ... admits")
	}
	if !lookup(v, cue.ParsePath("declared")).Exists() {
		t.Error("lookup did not find a declared optional field")
	}
}

// TestLookupDisjunction pins that LookupPath finds nothing beneath a
// disjunction, while lookup looks the rest of the path up in each alternative:
// a path every alternative declares is declared, with the disjunction of the
// alternatives' values, or their one value where they agree; a path only some
// declare is not. A field is optional where any alternative leaves it so, and
// marked required only where every alternative marks it (#229).
func TestLookupDisjunction(t *testing.T) {
	ctx := cuecontext.New()
	v := ctx.CompileString(`
top: {method!: "GET", route!: string, n!: int, path!: {id!: string}, q!: {x!: int}} |
	{method!: "POST", route!: string, n?: int, path!: {id!: string}, q!: {x?: int, y!: int}, body!: {name!: string}}
nested: req!: top
`)
	if got := v.LookupPath(cue.ParsePath("top")).LookupPath(cue.MakePath(cue.Str("route").Optional())); got.Exists() {
		t.Errorf("LookupPath found route beneath a disjunction: %v", got)
	}
	e := &edge{}
	for _, base := range []struct{ name, prefix string }{{"top", ""}, {"nested", "req."}} {
		root := v.LookupPath(cue.ParsePath(base.name))
		at := func(p string) cue.Path { return cue.ParsePath(base.prefix + p) }
		for _, c := range []struct {
			path, want string
		}{
			{"route", "string"},
			{"path.id", "string"},
			{"method", `"GET" | "POST"`},
		} {
			got := lookup(root, at(c.path))
			if !got.Exists() {
				t.Errorf("%s: lookup did not find %s, which every alternative declares", base.name, c.path)
				continue
			}
			if err := cuetil.Equal(ctx.CompileString("_").Unify(got), ctx.CompileString(c.want)); err != nil {
				t.Errorf("%s: lookup of %s is %v, want %s", base.name, c.path, got, c.want)
			}
		}
		for _, p := range []string{"body", "body.name"} {
			if lookup(root, at(p)).Exists() {
				t.Errorf("%s: lookup found %s, which only one alternative declares", base.name, p)
			}
		}
		if !e.optional(root, at("n")) || !e.optionalAt(root, at("n")) {
			t.Errorf("%s: n is optional in one alternative, so it is optional", base.name)
		}
		if e.marked(root, at("n")) {
			t.Errorf("%s: n is marked required in only one alternative, so it is not marked", base.name)
		}
		if e.optional(root, at("route")) || !e.marked(root, at("route")) {
			t.Errorf("%s: every alternative requires route, so it is required", base.name)
		}
		// q's alternatives differ, so its value is their disjunction, whose
		// alternatives hold x.
		if !lookup(root, at("q.x")).Exists() || !e.optional(root, at("q.x")) {
			t.Errorf("%s: q.x is declared in every alternative of q, and optional in one", base.name)
		}
	}
}
