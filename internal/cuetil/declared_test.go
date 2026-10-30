package cuetil

import (
	"reflect"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

func TestUndeclaredFields(t *testing.T) {
	ctx := cuecontext.New()
	for _, c := range []struct {
		name, sch, data string
		want            []string
	}{
		{"all declared", `{a: int, b?: string, c!: bool}`, `{a: 1, b: "x", c: true}`, nil},
		{"open struct declares nothing", `{a: int, ...}`, `{a: 1, x: 2}`, []string{"x"}},
		{"closed literal", `{a: int}`, `{a: 1, x: 2}`, []string{"x"}},
		{"nested open", `{n: {a: int, ...}}`, `{n: {a: 1, x: 2}}`, []string{"n.x"}},
		// Only a predictable name declares: a key a pattern matches is
		// undeclared, reported whole (#197).
		{"pattern declares nothing", `{[=~"^x"]: int, ...}`, `{xa: 1, xb: 2, y: 3}`, []string{"xa", "xb", "y"}},
		{"map", `{m: [string]: {v: int, ...}}`, `{m: {k: {v: 1, extra: 2}}}`, []string{"m.k"}},
		{"named field beside a pattern", `{m: {a?: int, [string]: int}}`, `{m: {a: 1, b: 2}}`, []string{"m.b"}},
		{"list elements", `{l: [...{a: int, ...}]}`, `{l: [{a: 1}, {a: 2, x: 3}]}`, []string{"l[1].x"}},
		{"disjunction, one branch matches", `{d: {a: int, ...} | {b: int, ...}}`, `{d: {b: 1}}`, nil},
		{"disjunction, no branch matches", `{d: {a: int, ...} | {b: int, ...}}`, `{d: {b: 1, x: 2}}`, []string{"d.x"}},
		{"scalars unaffected", `{a: int | string}`, `{a: "x"}`, nil},
		{"defaulted disjunction, non-default branch", `{d: *{a: int, ...} | {b: int, ...}}`, `{d: {b: 1}}`, nil},
		{"defaulted disjunction, undeclared", `{d: *{a: int, ...} | {b: int, ...}}`, `{d: {b: 1, x: 2}}`, []string{"d.x"}},
		{"defaulted map disjunction", `{m: {[string]: int} | *{}}`, `{m: {k: 1}}`, []string{"m.k"}},
		{"defaulted list disjunction", `{l: [...{a: int, ...}] | *[]}`, `{l: [{a: 1, x: 2}]}`, []string{"l[0].x"}},
		{"nullable struct", `{n: null | {a: int, ...}}`, `{n: {a: 1, x: 2}}`, []string{"n.x"}},
		{"embedded definition", `{#D: {a: int, ...}, s: {#D, b?: int}}`, `{s: {a: 1, b: 2, x: 3}}`, []string{"s.x"}},
		{"hidden and definition data labels ignored", `{a: int}`, `{a: 1, _h: 2, #d: 3}`, nil},
		{"top declares everything", `{a: string, meta: _}`, `{a: "x", meta: {deep: {x: 1}, l: [{y: 2}]}}`, nil},
		{"recursive definition", `{#Node: {v: int, next?: #Node}, root: #Node}`, `{root: {v: 1, next: {v: 2, next: {v: 3}}}}`, nil},
		{"recursive definition, undeclared deep", `{#Node: {v: int, next?: #Node}, root: #Node}`, `{root: {v: 1, next: {v: 2, x: 3}}}`, []string{"root.next.x"}},
		{"conjunction with defaulted disjunction", `_ & (*{a: int} | {b: string})`, `{b: "x"}`, nil},
		{"conjunction with defaulted disjunction, undeclared", `_ & (*{a: int} | {b: string})`, `{b: "x", z: 1}`, []string{"z"}},
		// Reached through a reference, a disjunction's alternatives are those
		// of the value referred to (as a lineage's schema is, at
		// schemas.x.y.schema).
		{"reference to defaulted disjunction", `{#H: *{a: int} | {b: string}, w: #H}`, `{w: {b: "x"}}`, nil},
		{"conjunction with reference to defaulted disjunction", `{_d: *{a: int} | {b: string}, v: _ & _d}`, `{v: {b: "x"}}`, nil},
		{"conjunction with reference to defaulted disjunction, undeclared", `{_d: *{a: int} | {b: string}, v: _ & _d}`, `{v: {b: "x", z: 1}}`, []string{"v.z"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, p := range UndeclaredFields(ctx, ctx.CompileString(c.data), ctx.CompileString(c.sch)) {
				got = append(got, p.String())
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestDeclaresPath(t *testing.T) {
	ctx := cuecontext.New()
	sch := ctx.CompileString(`{
	a: int
	b?: string
	c!: {d: int, e?: {f: int}}
	open: {x: int, ...}
	m: [string]: {v: int}
	l: [...{w: int}]
	any: _
	dj: {p: int} | {q: int}
}`)
	for path, want := range map[string]bool{
		"a": true, "b": true, "c": true, "c.d": true, "c.e.f": true,
		"open.x": true, "open.y": false,
		"m": true, "m.anything": false, "m.k.v": false,
		"l[0].w": true, "l[3].w": true, "l[0].z": false,
		"any.deep.down": true,
		"dj.p":          true, "dj.q": true, "dj.r": false,
		"nope": false, "a.b": false,
	} {
		if got := DeclaresPath(ctx, sch, cue.ParsePath(path)); got != want {
			t.Errorf("%s: got %v, want %v", path, got, want)
		}
	}
}

func TestPatternKey(t *testing.T) {
	ctx := cuecontext.New()
	sch := ctx.CompileString(`{
	a: int
	labels: [string]: string
	m: {named?: int, [=~"^x"]: {v: int}}
	open: {x: int, ...}
	l: [...{tags: [string]: int}]
	any: _
	dj: {p: int} | {[string]: int}
}`)
	for path, want := range map[string]string{
		"a":               "",
		"labels":          "",
		"labels.env":      "labels.env",
		"labels.env.deep": "labels.env",
		"m.named":         "",
		"m.xy":            "m.xy",
		"m.xy.v":          "m.xy",
		"m.y":             "",
		"open.y":          "",
		"l[2].tags.k":     "l[2].tags.k",
		"any.k":           "",
		"dj.p":            "",
		"dj.q":            "dj.q",
		"nope":            "",
	} {
		got := ""
		if k, ok := PatternKey(ctx, sch, cue.ParsePath(path)); ok {
			got = k.String()
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", path, got, want)
		}
	}
}

// Whether a name is declared, or matched only by a pattern, depends only on
// what the schema is, however it was constructed, in CUE or in Go.
func TestPatternKeyConstructionIndependent(t *testing.T) {
	ctx := cuecontext.New()
	const body = `{a: string, labels: {team?: string, [string]: string}, ...}`
	path := cue.ParsePath("s")
	for name, mk := range map[string]func() cue.Value{
		"literal":            func() cue.Value { return ctx.CompileString("s: " + body).LookupPath(path) },
		"reference":          func() cue.Value { return ctx.CompileString("_S: " + body + "\ns: _S").LookupPath(path) },
		"embedded reference": func() cue.Value { return ctx.CompileString("_S: " + body + "\ns: {_S}").LookupPath(path) },
		"definition":         func() cue.Value { return ctx.CompileString("#S: " + body + "\ns: #S").LookupPath(path) },
		"split conjuncts": func() cue.Value {
			return ctx.CompileString("s: {a: string, labels: [string]: string, ...}\ns: labels: team?: string").LookupPath(path)
		},
		"Go Unify": func() cue.Value {
			return ctx.CompileString(`{a: string, labels: [string]: string, ...}`).Unify(ctx.CompileString(`{labels: team?: string, ...}`))
		},
		"Go FillPath": func() cue.Value {
			return ctx.CompileString(`{a: string, labels: {team?: string, ...}, ...}`).FillPath(cue.ParsePath("labels"), ctx.CompileString(`[string]: string`))
		},
		"closed": func() cue.Value { return CloseRecursive(ctx, ctx.CompileString(body)) },
	} {
		t.Run(name, func(t *testing.T) {
			sch := mk()
			if err := sch.Err(); err != nil {
				t.Fatal(err)
			}
			for path, want := range map[string]string{"labels.env": "labels.env", "labels.team": "", "a": "", "z": ""} {
				got := ""
				if k, ok := PatternKey(ctx, sch, cue.ParsePath(path)); ok {
					got = k.String()
				}
				if got != want {
					t.Errorf("%s: got %q, want %q", path, got, want)
				}
			}
			if DeclaresPath(ctx, sch, cue.ParsePath("labels.env")) {
				t.Error("labels.env declared")
			}
			if !DeclaresPath(ctx, sch, cue.ParsePath("labels.team")) {
				t.Error("labels.team not declared")
			}
			var got []string
			for _, p := range UndeclaredFields(ctx, ctx.CompileString(`{a: "x", labels: {team: "t", env: "e"}, z: 1}`), sch) {
				got = append(got, p.String())
			}
			if !reflect.DeepEqual(got, []string{"labels.env", "z"}) {
				t.Errorf("undeclared: got %v, want [labels.env z]", got)
			}
		})
	}
}
