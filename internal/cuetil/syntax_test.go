package cuetil

import (
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// describe renders b for comparison: each child is its name, prefixed with ?
// if conditional, then =in if passed through, or its own children in braces;
// + marks a value with fields its syntax can't name.
func describe(b *Built) string {
	var parts []string
	names := make([]string, 0, len(b.children))
	for n := range b.children {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := b.children[n]
		s := n
		if c.conditional {
			s = "?" + s
		}
		switch {
		case c.passThrough:
			s += "=in"
		case len(c.children) > 0 || c.open:
			s += describe(c)
		}
		parts = append(parts, s)
	}
	out := "{" + strings.Join(parts, " ") + "}"
	if b.open {
		out += "+"
	}
	return out
}

// How a lens's result is classified, for each way of writing it.
func TestLensResultClassification(t *testing.T) {
	ctx := cuecontext.New()
	for name, c := range map[string]struct {
		lens string // the lens's body; input: _ is supplied
		want string
	}{
		"direct":               {`result: {a: input.a, n: input.n}`, `{a=in n=in}`},
		"literal":              {`result: {a: "x", n: {p: 1}}`, `{a n{p}}`},
		"let":                  {`let N = input.n, result: {n: N}`, `{n=in}`},
		"let of literal":       {`let N = {p: input.a, q: 1}, result: {n: N}`, `{n{p=in q}}`},
		"self":                 {`result: {let S = self, p: input.a, q: S.p}`, `{p=in q=in}`},
		"alias":                {`result: {x~(X): input.a, y: X}`, `{x=in y=in}`},
		"hidden to input":      {`result: {_h: input.n, x: _h}`, `{_h=in x=in}`},
		"hidden helper":        {`result: {_h: {extra: 1}, x: _h}`, `{_h{extra} x{extra}}`},
		"embedded input":       {`result: {input, b: 1}`, `{b}`},
		"unified with literal": {`result: {n: input.n & {x: 1}}`, `{n{x}}`},
		"comprehension":        {`result: {a: input.a, if input.a == "boom" {extra: 1}}`, `{a=in ?extra}`},
		"nested comprehension": {`result: {m: {if input.z == 1 {d: 4}}, e: 5}`, `{e m{?d}}`},
		"under hidden":         {`result: {_h: {if input.a == "x" {extra: 1}}}`, `{_h{?extra}}`},
		"embedded hidden":      {`result: {_h: {if input.a == "x" {extra: 1}}, _h}`, `{_h{?extra} ?extra}`},
		"for over input":       {`result: {for k, v in input.m {"\(k)": v}}`, `{}+`},
		"dynamic label":        {`result: {("x" + input.a): 1}`, `{}+`},
		"via definition":       {`#R: {a: string, if input.a == "x" {extra: 1}}, result: #R & {a: input.a}`, `{a ?extra}`},
		"nested definition":    {`#R: {p: int}, result: {k: #R & {p: 1}}`, `{k{p}}`},
		"close":                {`result: close({a: input.a})`, `{a=in}`},
		"selector into helper": {`_h: {y: {extra: 1}}, result: {x: _h.y}`, `{x{extra}}`},
	} {
		t.Run(name, func(t *testing.T) {
			v := ctx.CompileString("lens: {input: _\n" + c.lens + "}").LookupPath(cue.ParsePath("lens"))
			if err := v.Err(); err != nil {
				t.Fatal(err)
			}
			ls, err := ParseLens(v)
			if err != nil {
				t.Fatal(err)
			}
			if got := describe(ls.Result()); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

// The classification depends only on what the lens is, however it was
// constructed, in CUE or in Go.
func TestLensSyntaxConstructionIndependent(t *testing.T) {
	ctx := cuecontext.New()
	const body = `input: _
	let N = input.n
	result: {
		a: input.a
		n: N
		_h: {extra: 1}
		x: _h
		if input.a == "boom" {c: 1}
	}
	lacunas: [if input.b != _|_ {{message: "m"}}]`
	const want = `{_h{extra} a=in ?c n=in x{extra}}`
	path := cue.ParsePath("lens")
	for name, mk := range map[string]func() cue.Value{
		"literal": func() cue.Value { return ctx.CompileString("lens: {" + body + "}").LookupPath(path) },
		"reference": func() cue.Value {
			return ctx.CompileString("_L: {" + body + "}\nlens: _L").LookupPath(path)
		},
		"embedded reference": func() cue.Value {
			return ctx.CompileString("_L: {" + body + "}\nlens: {_L}").LookupPath(path)
		},
		"definition": func() cue.Value {
			return ctx.CompileString("#L: {" + body + "}\nlens: #L").LookupPath(path)
		},
		"split conjuncts": func() cue.Value {
			return ctx.CompileString("lens: {" + body + "}\nlens: {input: _}").LookupPath(path)
		},
		"Go Unify": func() cue.Value {
			return ctx.CompileString("lens: {" + body + "}").Unify(ctx.CompileString("lens: input: _")).LookupPath(path)
		},
		"Go FillPath of lens": func() cue.Value {
			v := ctx.CompileString("lens: {" + body + "}").LookupPath(path)
			return ctx.CompileString("lens: _").FillPath(path, v).LookupPath(path)
		},
		"Go FillPath of input": func() cue.Value {
			v := ctx.CompileString("lens: {" + body + "}").LookupPath(path)
			return v.FillPath(cue.ParsePath("input"), ctx.CompileString(`{a: "x", n: {p: 1}}`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			ls, err := ParseLens(mk())
			if err != nil {
				t.Fatal(err)
			}
			if got := describe(ls.Result()); got != want {
				t.Errorf("result: got %s, want %s", got, want)
			}
			if !ls.LacunasConditional() {
				t.Error("conditional lacunas not detected")
			}
		})
	}
}

func TestLacunasConditional(t *testing.T) {
	ctx := cuecontext.New()
	for lacunas, want := range map[string]bool{
		`[{message: "m"}]`:                         false,
		`[if input.b != _|_ {{message: "m"}}]`:     true,
		`[for x in input.l {{message: "m"}}]`:      true,
		"L\nlet L = [if input.b != _|_ {{m: 1}}]":  true,
		"_l\n_l: [{message: \"m\"}, if true {{}}]": true,
	} {
		v := ctx.CompileString("lens: {input: _, result: _, lacunas: " + lacunas + "}").LookupPath(cue.ParsePath("lens"))
		ls, err := ParseLens(v)
		if err != nil {
			t.Fatal(err)
		}
		if got := ls.LacunasConditional(); got != want {
			t.Errorf("%s: got %v, want %v", lacunas, got, want)
		}
	}
}
