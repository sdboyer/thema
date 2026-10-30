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
// remedy in the normal form's syntax: the checker's advice to write a field
// under a presence test is dropped, since the authoring form has no way to
// write one (#155).
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
				if !strings.Contains(f.Detail, "_|_") {
					t.Fatalf("the checker's sentence has no remedy to drop: %s", f.Detail)
				}
				a := diag.Address{Kind: diag.AddrLensResultField, From: diag.Version(thema.SV(0, 0)), To: diag.Version(thema.SV(1, 0)), Field: []string{"l"}}
				got := v.Rewrite(a, f.Detail)
				if strings.Contains(got, "_|_") {
					t.Errorf("rewritten sentence suggests the normal form's syntax: %s", got)
				}
				if !strings.Contains(got, "v0_0.l is optional in history/0.0.thema") {
					t.Errorf("rewritten sentence lost what is wrong: %s", got)
				}
			}
		})
	}
}
