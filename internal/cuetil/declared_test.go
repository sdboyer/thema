package cuetil

import (
	"reflect"
	"testing"

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
