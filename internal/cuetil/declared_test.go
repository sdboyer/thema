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
		{"pattern declares", `{[=~"^x"]: int, ...}`, `{xa: 1, xb: 2, y: 3}`, []string{"y"}},
		{"map", `{m: [string]: {v: int, ...}}`, `{m: {k: {v: 1, extra: 2}}}`, []string{"m.k.extra"}},
		{"list elements", `{l: [...{a: int, ...}]}`, `{l: [{a: 1}, {a: 2, x: 3}]}`, []string{"l[1].x"}},
		{"disjunction, one branch matches", `{d: {a: int, ...} | {b: int, ...}}`, `{d: {b: 1}}`, nil},
		{"disjunction, no branch matches", `{d: {a: int, ...} | {b: int, ...}}`, `{d: {b: 1, x: 2}}`, []string{"d.x"}},
		{"scalars unaffected", `{a: int | string}`, `{a: "x"}`, nil},
		{"defaulted disjunction, non-default branch", `{d: *{a: int, ...} | {b: int, ...}}`, `{d: {b: 1}}`, nil},
		{"defaulted disjunction, undeclared", `{d: *{a: int, ...} | {b: int, ...}}`, `{d: {b: 1, x: 2}}`, []string{"d.x"}},
		{"defaulted map disjunction", `{m: {[string]: int} | *{}}`, `{m: {k: 1}}`, nil},
		{"defaulted list disjunction", `{l: [...{a: int, ...}] | *[]}`, `{l: [{a: 1, x: 2}]}`, []string{"l[0].x"}},
		{"nullable struct", `{n: null | {a: int, ...}}`, `{n: {a: 1, x: 2}}`, []string{"n.x"}},
		{"embedded definition", `{#D: {a: int, ...}, s: {#D, b?: int}}`, `{s: {a: 1, b: 2, x: 3}}`, []string{"s.x"}},
		{"hidden and definition data labels ignored", `{a: int}`, `{a: 1, _h: 2, #d: 3}`, nil},
		{"top declares everything", `{a: string, meta: _}`, `{a: "x", meta: {deep: {x: 1}, l: [{y: 2}]}}`, nil},
		{"recursive definition", `{#Node: {v: int, next?: #Node}, root: #Node}`, `{root: {v: 1, next: {v: 2, next: {v: 3}}}}`, nil},
		{"recursive definition, undeclared deep", `{#Node: {v: int, next?: #Node}, root: #Node}`, `{root: {v: 1, next: {v: 2, x: 3}}}`, []string{"root.next.x"}},
		{"conjunction with defaulted disjunction", `_ & (*{a: int} | {b: string})`, `{b: "x"}`, nil},
		{"conjunction with defaulted disjunction, undeclared", `_ & (*{a: int} | {b: string})`, `{b: "x", z: 1}`, []string{"z"}},
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
		"m.anything": true, "m.k.v": true, "m.k.z": false,
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
