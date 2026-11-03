package thema

import (
	"slices"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema/internal/cueview"
)

// TestHiddenLabels checks that hiddenLabels finds, in a schema-like value,
// exactly the hidden labels its SelfContained view writes as fields, which is
// what a version's ID is computed over (internal/contenthash): wherever the
// value declares one, through definitions, lets, disjunctions, lists and
// pattern constraints. CUE's wrapper for a closed value, {_#def, _#def: ...},
// is the view's, not a field, and is not expected; a _#def the value declares
// is.
func TestHiddenLabels(t *testing.T) {
	cases := map[string]string{
		"top":            `{_h: int}`,
		"nested":         `{a: {_h: int}}`,
		"definition":     `{_#d: {a: int}, x: _#d}`,
		"let body":       `{let L = {_h: 1, a: 2}, x: L.a}`,
		"let disjunct":   `{let L = {_h: 1} | {b: 2}, x: L}`,
		"let referenced": `{let L = {_h: int}, x: {y: L}}`,
		"disjunction":    `{a: {_h: int} | {b: int}}`,
		"default":        `{a: *{_h: 1} | {b: 2}}`,
		"conjunction":    `{a: {_h: int} & {b: int}}`,
		"disjunct of &":  `{a: ({_h: int} | {b: int}) & {c: int}}`,
		"optional":       `{a?: {_h: int}}`,
		"required":       `{_h?: int, _g!: int}`,
		"list element":   `{a: [...{_h: int}]}`,
		"list literal":   `{a: [{_h: 1}]}`,
		"list tail":      `{a: [int, ...{_h: int}]}`,
		"pattern":        `{[string]: {_h: int}}`,
		"pattern regexp": `{[=~"^x"]: {_h: int}}`,
		"in definition":  `{#D: {_h: int}, a: #D}`,
		"closed":         `{a: close({_h: int})}`,
		"embedded":       `{_h: {a: int}, _h}`,
		"own wrapper":    `{_#def, _#def: {a: string}}`,
		"none":           `{a: int, #D: {b: int}, x: #D, let L = 1, y: L, "_q": int}`,
	}
	ctx := cuecontext.New()
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			v := ctx.CompileString("s: "+src, cue.Filename("s.cue")).LookupPath(cue.ParsePath("s"))
			if err := v.Err(); err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, h := range hiddenLabels(v, v.Path()) {
				if !h.pos.IsValid() {
					t.Errorf("%s has no position", h.name)
				}
				if !slices.Contains(got, h.name) {
					got = append(got, h.name)
				}
			}
			n, err := cueview.SelfContained(v)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			ast.Walk(n, func(n ast.Node) bool {
				s, ok := n.(*ast.StructLit)
				if !ok {
					return true
				}
				wrapped := false
				for _, d := range s.Elts {
					if e, ok := d.(*ast.EmbedDecl); ok {
						if id, ok := e.Expr.(*ast.Ident); ok && id.Name == "_#def" {
							wrapped = true
						}
					}
				}
				for _, d := range s.Elts {
					if f, ok := d.(*ast.Field); ok {
						if id, ok := f.Label.(*ast.Ident); ok && strings.HasPrefix(id.Name, "_") && !slices.Contains(want, id.Name) {
							if wrapped && id.Name == "_#def" && name != "own wrapper" {
								continue
							}
							want = append(want, id.Name)
						}
					}
				}
				return true
			}, nil)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("hiddenLabels found %v; the SelfContained view declares %v", got, want)
			}
		})
	}
}
