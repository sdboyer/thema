package thema

import (
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema/diag"
)

// Bind errors found from a lens's syntax still point at the line the author
// wrote, however the offending value is reached: the syntax used to find the
// problem has no positions, so the error must take its position from the
// lineage's own value.
func TestLensSyntaxErrorPositions(t *testing.T) {
	const unconditional = "listed unconditionally"
	ctx := cuecontext.New()
	rt := NewRuntime(ctx)
	// loadLineageValue puts 4 lines before src.
	const header = 4
	lens := func(body string) string {
		return `lin: thema.Lineage & {
	name: "positions"
	schemas: "0": {"0": schema: {a: string}, "1": schema: {a: string, b?: int}}
	lenses: "0.1->0.0": {
		v0_1: _
` + body + `
	}
}`
	}
	for name, c := range map[string]struct {
		body   string
		marker string // the error must point at the line containing this
		err    string
	}{
		"conditional result": {
			body:   "\t\tv0_0: {a: v0_1.a, if v0_1.a == \"x\" {extra: 1}}",
			marker: "v0_0:", err: "extra",
		},
		"conditional result via let": {
			body:   "\t\tv0_0: R\n\t\tlet R = {a: v0_1.a, if v0_1.a == \"x\" {extra: 1}}",
			marker: "v0_0:", err: "extra",
		},
		"conditional lacunas": {
			body:   "\t\tv0_0: {a: v0_1.a}\n\t\tlacunas: [if v0_1.b != _|_ {thema.Lacuna & {message: \"m\", type: \"DroppedField\"}}]",
			marker: "lacunas:", err: unconditional,
		},
		"conditional lacunas via let": {
			body:   "\t\tv0_0: {a: v0_1.a}\n\t\tlacunas: L\n\t\tlet L = [if v0_1.b != _|_ {thema.Lacuna & {message: \"m\", type: \"DroppedField\"}}]",
			marker: "lacunas:", err: unconditional,
		},
	} {
		t.Run(name, func(t *testing.T) {
			src := lens(c.body)
			want := header + 1
			for i, line := range strings.Split(src, "\n") {
				if strings.Contains(line, c.marker) {
					want += i
					break
				}
			}
			_, err := BindLineage(loadLineageRoot(t, ctx, src), cue.ParsePath("lin"), rt)
			if err == nil {
				t.Fatal("bind succeeded, want an error")
			}
			var found bool
			for _, e := range cerrors.Errors(err) {
				if !strings.Contains(e.Error(), c.err) {
					continue
				}
				found = true
				if got := e.Position().Line(); got != want {
					t.Errorf("error %q is at line %d, want %d (%q)", e.Error(), got, want, c.marker)
				}
			}
			if !found {
				t.Fatalf("no error mentions %q: %s", c.err, cerrors.Details(err, nil))
			}
		})
	}
}

// thema.Lineage is recognized by what it is, a field of thema's package, not
// by its name: an author's own field called Lineage, unified into the lineage,
// is the author's, and thema still unifies its own Lineage in.
func TestBindAuthorFieldNamedLineage(t *testing.T) {
	ctx := cuecontext.New()
	root := ctx.CompileString(`
Lineage: {name: string}
lin: Lineage & {
	name: "ship"
	schemas: "0": "0": schema: {a: string}
}
`)
	lin, err := BindLineage(root, cue.ParsePath("lin"), NewRuntime(ctx))
	if err != nil {
		t.Fatal(err)
	}
	if !lin.Underlying().LookupPath(cue.ParsePath("joinSchema")).Exists() {
		t.Error("the bound lineage was not unified with thema.Lineage: the author's field named Lineage was taken for it")
	}
}

// Bind takes the root CUE built from source and the path to the lineage.
// Anything else is refused by what it is, whatever else is wrong with it.
func TestBindRoot(t *testing.T) {
	const text = "name: \"h\"\nschemas: \"0\": \"0\": schema: {a?: string}\n"
	ctx := cuecontext.New()
	rt := NewRuntime(ctx)
	loaded := loadLineageRoot(t, ctx, "lin: thema.Lineage & {\n"+text+"}\n")
	for _, tc := range []struct {
		name     string
		root     cue.Value
		path     cue.Path
		code     string
		headline string
	}{
		{
			"built in Go, with an error beside the lineage",
			ctx.CompileString("a: 1\nlin: _").FillPath(cue.ParsePath("a"), 2).FillPath(cue.ParsePath("lin"), ctx.CompileString(text)),
			cue.ParsePath("lin"), "E0152",
			"a lineage is bound from the root of a value CUE built from source, and this root was built in Go",
		},
		{
			"a field of a loaded root",
			loaded.LookupPath(cue.ParsePath("lin")), cue.Path{}, "E0152",
			"a lineage is bound from the root of a value CUE built from source, and this value is its field lin",
		},
		{
			"an invalid path",
			loaded, cue.ParsePath("lin.["), "E0128",
			"the path to the lineage is not a valid CUE path",
		},
		{
			"source that does not compile",
			ctx.CompileString("lin: {"), cue.ParsePath("lin"), "E0128",
			"there is no value at lin to bind as a lineage",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BindLineage(tc.root, tc.path, rt)
			var got []string
			for _, d := range diag.Diagnostics(err) {
				got = append(got, string(d.Code)+" "+d.Headline)
			}
			if want := tc.code + " " + tc.headline; len(got) != 1 || got[0] != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}
