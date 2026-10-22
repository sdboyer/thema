package compat

import (
	"testing"

	"cuelang.org/go/cue/cuecontext"
)

func TestThemaCompatible(t *testing.T) {
	ctx := cuecontext.New()
	for _, c := range []struct {
		name, old, new string
		compat         bool
	}{
		{"add-optional", `{a: string}`, `{a: string, b?: int}`, true},
		{"add-required", `{a: string}`, `{a: string, b: int}`, false},
		{"remove-optional", `{a: string, b?: int}`, `{a: string}`, false},
		{"remove-required", `{a: string, b: int}`, `{a: string}`, false},
		{"optional-to-required", `{a: string, b?: int}`, `{a: string, b: int}`, false},
		{"nested-add-optional", `{n: {a: string}}`, `{n: {a: string, b?: int}}`, true},
		{"widen-union", `{u: "x" | "y"}`, `{u: "x" | "y" | "z"}`, true},
		{"narrow-union", `{u: "x" | "y" | "z"}`, `{u: "x" | "y"}`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := ThemaCompatible(ctx, ctx.CompileString(c.old), ctx.CompileString(c.new))
			if got := err == nil; got != c.compat {
				t.Errorf("compatible = %v, want %v (err: %v)", got, c.compat, err)
			}
		})
	}
}
