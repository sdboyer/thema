package contenthash

import (
	"regexp"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// schemaForm returns the canonical form of the schema expr, written as the only
// schema of a lineage, in a file that also holds scope.
func schemaForm(t *testing.T, scope, expr string) string {
	t.Helper()
	v := cuecontext.New().CompileString(scope + "\nlin: schemas: [{version: [0, 0], schema: " + expr + "}]")
	if err := v.Err(); err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	form, err := SchemaForm(v.LookupPath(cue.ParsePath("lin")), Version{0, 0})
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	return form
}

// Schemas used by more than one case.
const (
	refX     = `{a: #X}`
	inlineX  = `{a: {p: int}}`
	regularA = `{a: int}`
)

type pair struct {
	scope, a, b string
	// why says why the two have the same meaning, or don't.
	why string
}

// What the canonical form makes equal: the pairs here mean the same, and must
// have the same canonical form.
func TestSchemaFormEqual(t *testing.T) {
	for name, c := range map[string]pair{
		"formatting and comments": {
			a:   `{a: int, b: string}`,
			b:   "{\n\t// a doc comment\n\ta: int // trailing\n\n\n\tb:   string\n}",
			why: "comments and layout are presentation",
		},
		"field order": {
			a:   `{a: int, b: string, if true {c: 1}, [=~"^x"]: int}`,
			b:   `{[=~"^x"]: int, if true {c: 1}, b: string, a: int}`,
			why: "unification is commutative; the order of fields shows only in output, such as generated code",
		},
		"conjunct order": {
			a: `{a: int & >0}`, b: `{a: >0 & int}`,
			why: "& is commutative (CUE's export already orders these; the sort makes it not matter if that changes)",
		},
		"default marker, disjunct order": {
			a: `{a: *1 | int}`, b: `{a: int | *1}`,
			why: "| is commutative, and the marker belongs to its disjunct",
		},
		"enum order": {
			a: `{a: "x" | "y" | "z"}`, b: `{a: "z" | "x" | "y"}`,
			why: "| is commutative; the order shows only in output, such as a generated enum",
		},
		"let name": {
			a: `{let L = {p: int}, a: L, b: L}`, b: `{let Other = {p: int}, a: Other, b: Other}`,
			why: "a let's name is not part of what it means",
		},
		"name of a referenced definition": {
			scope: `#X: {p: int}`, a: refX,
			b: `{a: #Renamed}
#Renamed: {p: int}`,
			why: "CUE names the let it generates for #X after its path; the content is the same",
		},
		"reference to another schema vs copy": {
			scope: `_v0: {a: int, b: string}`,
			a:     `_v0 & {c?: int}`, b: `{a: int, b: string, c?: int}`,
			why: "CUE exports the referenced schema as its own struct literal, & the new one; the literals are merged",
		},
		"embedded reference vs copy": {
			scope: `_v0: {a: int, b: string}`,
			a:     `{_v0, c?: int}`, b: `{a: int, b: string, c?: int}`,
			why: "embedding a struct literal that embeds nothing itself adds its fields",
		},
		"refinement of a referenced schema vs copy": {
			scope: `_v0: {a: int, b: {p: string}}`,
			a:     `_v0 & {a: >0, b: q?: int}`, b: `{a: int & >0, b: {p: string, q?: int}}`,
			why: "a field declared in both literals is one field, with the & of both values",
		},
		"struct that only embeds": {
			scope: `_s: string`,
			a:     `{a: {_s}, b: {[...int]}}`, b: `{a: string, b: [...int]}`,
			why: "{X} is X; CUE's export writes string & strings.MinRunes(1) as either, depending on context (see testdata/kitchen.txtar)",
		},
		"field declared twice": {
			a: `{a: {p: int}, a: {q: string}}`, b: `{a: {p: int, q: string}}`,
			why: "a field declared twice is one field",
		},
		"duplicate conjunct": {
			a: `{a: int} & {a: int} & {b: int}`, b: `{a: int, b: int}`,
			why: "unification is idempotent",
		},
	} {
		t.Run(name, func(t *testing.T) {
			fa, fb := schemaForm(t, c.scope, c.a), schemaForm(t, c.scope, c.b)
			if fa != fb {
				t.Errorf("%s, but the forms differ:\n%s\n---\n%s", c.why, fa, fb)
			}
		})
	}
}

// What the canonical form keeps apart: the pairs here mean different things, or
// may, and must have different canonical forms. The first ones are pairs with
// the same meaning that the canonical form deliberately doesn't make equal.
func TestSchemaFormDiffer(t *testing.T) {
	for name, c := range map[string]pair{
		"definition vs inline struct": {
			scope: `#X: {p: int}`, a: refX, b: inlineX,
			why: "#X is closed, recursively, and the inline struct is open",
		},
		"definition vs close()": {
			scope: `#X: {p: int, q: {r: int}}`, a: refX, b: `{a: close({p: int, q: {r: int}})}`,
			why: "close() closes only the outermost struct; the canonical form doesn't attempt to prove closedness equivalent",
		},
		"let vs inline": {
			a: `{let L = {p: int}, a: L}`, b: inlineX,
			why: "CUE's export keeps the author's lets. They mean the same, but inlining a let soundly means substituting syntax across CUE's scopes (self, aliases, comprehension variables, shadowing), so the canonical form leaves lets in place",
		},
		"default": {
			a: `{a: int | *1}`, b: `{a: int | 1}`,
			why: "a default is filled into instances",
		},
		"optional vs regular": {
			a: `{a?: int}`, b: regularA,
			why: "an optional field may be absent from instances",
		},
		"required vs regular": {
			a: `{a!: int}`, b: regularA,
			why: "a required field must be present in instances",
		},
		"open vs closed by ...": {
			a: `{a: {p: int}}`, b: `{a: {p: int, ...}}`,
			why: "... keeps a struct open where it is used closed (Validate closes schemas)",
		},
		"attribute": {
			a: `{a: int @cuetsy(kind="type")}`, b: regularA,
			why: "attributes direct code generators; the canonical form keeps them",
		},
		"conjunction of definitions vs one definition": {
			scope: `#A: {a: int}
#B: {a: int, c?: int}
#C: {a: int, c?: int}`,
			a: `{x: #A & #B}`, b: `{x: #C}`,
			why: "#A & #B allows only the fields both allow; struct literals within a definition are not merged",
		},
		"conjunction of definitions at the top": {
			scope: `#A: {a: int}
#B: {a: int, c?: int}
#C: {a: int, c?: int}`,
			a: `#A & #B`, b: `#C`,
			why: "CUE exports #A & #B as {_#def, _#def: {a: int} & {a: int, c?: int}}; merging those literals would allow c",
		},
		"quoted hidden label": {
			a: `{s: {_a: int} & {"_a": >0}}`, b: `{s: {_a: int & >0}}`,
			why: `"_a" is a regular field, not the hidden _a; they are not merged`,
		},
		"merge would capture a reference": {
			a: `{x: int, s: {b: x} & {x: string}}`, b: `{x: int, s: {b: x, x: string}}`,
			why: "in a, b refers to the outer x (int); merging the literals would make it refer to the inner x (string), as in b",
		},
		"embedding would capture a reference": {
			a: `{x: int, s: {{x: string}, b: x}}`, b: `{x: int, s: {x: string, b: x}}`,
			why: "in a, b refers to the outer x: the embedded literal's fields aren't in b's scope",
		},
		"reference used twice": {
			scope: `_v0: {a: int}`,
			a:     `_v0 & {b: int} & _v0`, b: `{a: int, b: int}`,
			why: "they mean the same, but CUE binds a value a schema refers to twice in a let, and lets are not inlined (see let vs inline)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			fa, fb := schemaForm(t, c.scope, c.a), schemaForm(t, c.scope, c.b)
			if fa == fb {
				t.Errorf("%s, but the forms are the same:\n%s", c.why, fa)
			}
		})
	}
}

// The differ cases that rest on closedness or scoping, checked against CUE:
// data that one of each pair accepts and the other rejects, so that the cases
// test what they claim to.
func TestSchemaFormDifferMeaning(t *testing.T) {
	ctx := cuecontext.New()
	for _, c := range []struct {
		scope, a, b, data string
	}{
		{`#A: {a: int}, #B: {a: int, c?: int}, #C: {a: int, c?: int}`, `#A & #B`, `#C`, `{a: 1, c: 1}`},
		{``, `{x: int, s: {b: x} & {x: string}}.s`, `{x: int, s: {b: x, x: string}}.s`, `{x: "s", b: 1}`},
		{``, `{x: int, s: {{x: string}, b: x}}.s`, `{x: int, s: {x: string, b: x}}.s`, `{x: "s", b: 1}`},
	} {
		accepts := func(expr string) bool {
			v := ctx.CompileString(c.scope + "\nx: " + expr).LookupPath(cue.ParsePath("x"))
			if err := v.Err(); err != nil {
				t.Fatalf("%s: %v", expr, err)
			}
			return v.Unify(ctx.CompileString(c.data)).Validate(cue.Concrete(true)) == nil
		}
		if accepts(c.a) == accepts(c.b) {
			t.Errorf("%s and %s agree on %s: the differ case built on them tests nothing", c.a, c.b, c.data)
		}
	}
}

func TestSchemaFormErrors(t *testing.T) {
	v := cuecontext.New().CompileString(`lin: schemas: [{version: [0, 0], schema: {a: 1 & 2}}]`)
	if _, err := SchemaForm(v.LookupPath(cue.ParsePath("lin")), Version{0, 0}); err == nil {
		t.Error("a schema with an error has a canonical form; every error would have the same one")
	}
	if _, err := SchemaForm(v.LookupPath(cue.ParsePath("lin")), Version{0, 1}); err == nil {
		t.Error("a missing version has a canonical form")
	}
}

// The schema's version, examples and the rest of its entry are not hashed; the
// lineage's joinSchema is, unified with the schema, since instances are
// validated against both.
func TestSchemaFormEntry(t *testing.T) {
	ctx := cuecontext.New()
	form := func(src string) string {
		t.Helper()
		v := ctx.CompileString(src)
		if err := v.Err(); err != nil {
			t.Fatal(err)
		}
		var ver Version
		if strings.Contains(src, "[3, 1]") {
			ver = Version{3, 1}
		}
		f, err := SchemaForm(v.LookupPath(cue.ParsePath("lin")), ver)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	base := form(`lin: schemas: [{version: [0, 0], schema: {k: string, a: int}}]`)
	for name, src := range map[string]string{
		"version omitted": `lin: schemas: [{schema: {k: string, a: int}}]`,
		"other version":   `lin: schemas: [{version: [3, 1], schema: {k: string, a: int}}]`,
		"examples":        `lin: schemas: [{version: [0, 0], schema: {k: string, a: int}, examples: e: {k: "x", a: 1}}]`,
		"joinSchema":      `lin: {joinSchema: {k: string}, schemas: [{version: [0, 0], schema: {a: int}}]}`,
	} {
		if got := form(src); got != base {
			t.Errorf("%s: form differs:\n%s\n---\n%s", name, got, base)
		}
	}
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{64}:[0-9a-f]{64}$`)

func TestIDString(t *testing.T) {
	v := cuecontext.New().CompileString(`lin: schemas: [{version: [0, 0], schema: {a: int}}]`)
	id, err := VersionID(v.LookupPath(cue.ParsePath("lin")), Version{0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if s := id.String(); !idPattern.MatchString(s) {
		t.Errorf("ID %s is not <64 hex>:<64 hex>", s)
	}
}
