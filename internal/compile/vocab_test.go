package compile

import (
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/lenscheck"
)

// A finding about a lens the authoring form cannot have written suggests no
// remedy in the normal form's syntax: the checker's help to write a field
// under a presence test is not given, since the authoring form has no way to
// write one (#155). What is wrong is still said whole.
func TestRewriteDropsNormalFormRemedy(t *testing.T) {
	ctx := cuecontext.New()
	source := ctx.CompileString(`l?: [...{t: string}]`)
	target := ctx.CompileString(`l?: [...{u: string}]`)
	cases := map[string]string{
		"result": `v0_0: _
v1_0: {l: [for item in v0_0.l {u: item.t}]}`,
		"lacuna": `v0_0: _
v1_0: {}
lacunas: [{condition: true, sourceFields: [{path: "l", value: v0_0.l}], message: "m", type: "DroppedField"}]`,
	}
	v := vocab{paths: map[thema.SyntacticVersion]string{thema.SV(0, 0): "history/0.0.thema", thema.SV(1, 0): "history/1.0.thema"}}
	for name, lens := range cases {
		t.Run(name, func(t *testing.T) {
			res := lenscheck.Check(ctx, ctx.CompileString(lens), source, target, "v0_0", "v1_0")
			if len(res.Findings) == 0 {
				t.Fatal("want a finding: the lens reads a list the source may omit")
			}
			for _, f := range res.Findings {
				if !strings.Contains(f.Help, "_|_") {
					t.Fatalf("the checker's help has no remedy to drop: %q", f.Help)
				}
				a := diag.Address{Kind: diag.AddrLensResultField, From: diag.Version(thema.SV(0, 0)), To: diag.Version(thema.SV(1, 0)), Field: []string{"l"}}
				if h, ok := v.RewriteHelp(a, f.Help); ok {
					t.Errorf("rewritten help suggests the normal form's syntax: %s", h)
				}
				if got := v.Rewrite(a, f.Detail); !strings.Contains(got, "v0_0.l is optional in history/0.0.thema") {
					t.Errorf("rewritten sentence lost what is wrong: %s", got)
				}
			}
		})
	}
}

// A sentence that describes presence tests the author's lens is written under
// keeps every one of them: only help is ever dropped, never a clause of what
// is wrong.
func TestRewriteKeepsDescribedConditions(t *testing.T) {
	v := vocab{paths: map[thema.SyntacticVersion]string{thema.SV(0, 0): "history/0.0.thema", thema.SV(1, 0): "history/1.0.thema"}}
	a := diag.Address{Kind: diag.AddrLensResultField, From: diag.Version(thema.SV(0, 0)), To: diag.Version(thema.SV(1, 0)), Field: []string{"b"}}
	sentence := "is written only under v0_0.a != _|_; v0_0.a == _|_ && v0_0.c != _|_, which the checker cannot prove cover every instance"
	if got := v.Rewrite(a, sentence); got != sentence {
		t.Errorf("Rewrite cut what is wrong:\n got %s\nwant %s", got, sentence)
	}
}

// The checker's help to test a number's type before comparing it is a
// condition in the normal form's syntax, which the authoring form cannot
// write: it is not given, and what is wrong is still said. The authoring form
// cannot reach it today (a _match arm keyed by a number does not parse,
// E0102), so the checker is called directly.
func TestRewriteDropsTypeTest(t *testing.T) {
	ctx := cuecontext.New()
	lens := ctx.CompileString(`v0_0: _
v1_0: {
	if v0_0.a == 5 {b: v0_0.a}
	if v0_0.a != 5 {b: 0}
}`)
	res := lenscheck.Check(ctx, lens, ctx.CompileString(`a!: number`), ctx.CompileString(`b!: int`), "v0_0", "v1_0")
	v := vocab{paths: map[thema.SyntacticVersion]string{thema.SV(0, 0): "history/0.0.thema", thema.SV(1, 0): "history/1.0.thema"}}
	a := diag.Address{Kind: diag.AddrLensResultField, From: diag.Version(thema.SV(0, 0)), To: diag.Version(thema.SV(1, 0)), Field: []string{"b"}}
	var tested int
	for _, f := range res.Findings {
		if !strings.HasPrefix(f.Help, "test its type first") {
			continue
		}
		tested++
		if h, ok := v.RewriteHelp(a, f.Help); ok {
			t.Errorf("rewritten help suggests the normal form's syntax: %s", h)
		}
		if got := v.Rewrite(a, f.Detail); !strings.Contains(got, "v0_0.a may be a float") {
			t.Errorf("rewritten sentence lost what is wrong: %s", got)
		}
	}
	if tested == 0 {
		t.Fatalf("want a finding whose help tests the type first, got %v", res.Findings)
	}
}
