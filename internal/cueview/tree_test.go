package cueview

import (
	"fmt"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
)

// What Tree.Conjuncts finds for the field r of each case, rendered as each
// conjunct's text, then @ and the label of the field it was reached through
// (- for none).
func TestTreeConjuncts(t *testing.T) {
	ctx := cuecontext.New()
	for name, c := range map[string]struct {
		src  string // the case's fields, beside input: _; _ext and #O are outside it
		want string
	}{
		"literal":                    {`r: {a: 1}`, `{a: 1}@-`},
		"field":                      {`x: {a: 1}, r: x`, `{a: 1}@x`},
		"hidden field":               {`_h: {h: 1}, r: _h`, `{h: 1}@_h`},
		"field declared twice":       {`x: {a: 1}, x: {b: 2}, r: x`, `{ a: 1 b: 2 }@x`}, // the export merges them
		"let":                        {`let L = {l: 1}, r: L`, `{l: 1}@-`},
		"field alias":                {`x~(X): {al: 1}, r: X`, `{al: 1}@x`},
		"self":                       {`let S = self, p: {se: 1}, r: S.p`, `{se: 1}@p`},
		"selection":                  {`x: {y: {s: 1}}, r: x.y`, `{s: 1}@y`},
		"selection into input":       {`r: input.a.b`, `input.a.b@input`},
		"index":                      {`x: [{a: 1}, {b: 2}], r: x[1]`, `{b: 2}@x`},
		"index past comprehension":   {`x: [if input.c {0}, {b: 2}], r: x[1]`, `x[1]@x`},
		"unification":                {`x: {a: 1}, r: x & {b: 2}`, `{a: 1}@x {b: 2}@-`},
		"disjunction":                {`r: *{d: 1} | {b: 2}`, `{d: 1}@- {b: 2}@-`},
		"embedding":                  {`x: {em: 1}, r: {x}`, `{em: 1}@x`},
		"close":                      {`r: close({c: 1})`, `{c: 1}@-`},
		"outside, concrete":          {`r: _ext`, `{e: 1}@-`},
		"definition":                 {`#D: {a: int}, r: #D`, `{a: int}@#D`},
		"outside definition":         {`r: {k: #O}`, `{k: O.#x}@-`},
		"through outside definition": {`r: #O.p`, `{q: int}@#x`},
		"for value":                  {`r: [for v in input.l {v}][0]`, `[for v in input.l { v }][0]@-`},
		"predeclared":                {`r: int`, `int@-`},
		"reference chain":            {`x: y, y: z, z: input.a, r: x`, `input.a@input`},
	} {
		t.Run(name, func(t *testing.T) {
			v := ctx.CompileString("_ext: {e: 1}\n#O: {p: {q: int}}\ncase: {input: _\n" + c.src + "}").LookupPath(cue.ParsePath("case"))
			if err := v.Err(); err != nil {
				t.Fatal(err)
			}
			tr, err := NewTree(v)
			if err != nil {
				t.Fatal(err)
			}
			var r *ast.Field
			for _, root := range tr.Root() {
				s, ok := root.Expr.(*ast.StructLit)
				if !ok {
					continue
				}
				for _, fd := range FieldDecls(s.Elts, func(x ast.Expr) []ast.Expr { return []ast.Expr{x} }) {
					if n, _, _ := ast.LabelName(fd.Field.Label); n == "r" {
						r = fd.Field
					}
				}
			}
			if r == nil {
				t.Fatal("no field r in the exported case")
			}
			var got []string
			for _, cj := range tr.Conjuncts(r.Value) {
				s, err := Text(cj.Expr)
				if err != nil {
					t.Fatal(err)
				}
				via := "-"
				if cj.Via != nil {
					via, _, _ = ast.LabelName(cj.Via.Label)
				}
				got = append(got, strings.Join(strings.Fields(s), " ")+"@"+via)
			}
			if g := strings.Join(got, " "); g != c.want {
				t.Errorf("got  %s\nwant %s", g, c.want)
			}
		})
	}
}

// A recursively closed value is exported wrapped, as {_#def, _#def: {...}};
// FieldDecls, given Conjuncts, sees its fields through the wrapper.
func TestTreeDefinitionRoot(t *testing.T) {
	v := cuecontext.New().CompileString(`#D: {a: int, b: string}`).LookupPath(cue.ParsePath("#D"))
	tr, err := NewTree(v)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, root := range tr.Root() {
		s, ok := root.Expr.(*ast.StructLit)
		if !ok {
			t.Fatalf("root is %T", root.Expr)
		}
		for _, fd := range FieldDecls(s.Elts, func(x ast.Expr) []ast.Expr {
			var out []ast.Expr
			for _, c := range tr.Conjuncts(x) {
				out = append(out, c.Expr)
			}
			return out
		}) {
			n, _, _ := ast.LabelName(fd.Field.Label)
			got = append(got, n)
		}
	}
	if g := strings.Join(got, ","); g != "a,b,_#def" {
		t.Errorf("got %s, want a,b,_#def", g)
	}
}

// The exporter writes an embedding beside a comprehension as a bare expression
// declaration, and beside plain fields as an *ast.EmbedDecl (see the
// comprehensions golden). FieldDecls and soleEmbed take both as embeddings.
func TestTreeEmbedDecl(t *testing.T) {
	v := cuecontext.New().CompileString(`
input: _
cases: {
	besideComprehension: {input, if input.x != _|_ {extra: 1}}
	besideField:         {input, extra: 1}
}`).LookupPath(cue.ParsePath("cases"))
	tr, err := NewTree(v)
	if err != nil {
		t.Fatal(err)
	}
	decls := map[string]string{}
	for _, root := range tr.Root() {
		s, ok := root.Expr.(*ast.StructLit)
		if !ok {
			continue
		}
		for _, fd := range FieldDecls(s.Elts, func(x ast.Expr) []ast.Expr { return []ast.Expr{x} }) {
			name, _, _ := ast.LabelName(fd.Field.Label)
			lit, ok := fd.Field.Value.(*ast.StructLit)
			if !ok {
				t.Fatalf("%s: value is %T", name, fd.Field.Value)
			}
			decls[name] = fmt.Sprintf("%T", lit.Elts[0])
			if got := soleEmbed(lit.Elts[:1]); got == nil {
				t.Errorf("%s: soleEmbed does not see the embedding", name)
			}
		}
	}
	want := map[string]string{"besideComprehension": "*ast.Ident", "besideField": "*ast.EmbedDecl"}
	if fmt.Sprint(decls) != fmt.Sprint(want) {
		t.Errorf("first declarations: got %v, want %v", decls, want)
	}
}
