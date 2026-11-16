package diagtest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sdboyer/thema/diag"
)

// recorder is a testing.TB that keeps what Lint reports.
type recorder struct {
	testing.TB
	errs []string
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func TestLint(t *testing.T) {
	const namesRule = "names a checker's rule"
	for _, c := range []struct {
		name string
		d    diag.Diagnostic
		want string // a reported problem contains it; "" when none is reported
	}{
		{"an author's identifier ending in a kernel term", diag.Diagnostic{Headline: `v1_0.b in lens v0.0->v1.0 refers to shipLineage.schemas["0"]["0"].schema.a, a schema`}, ""},
		{"the kernel term itself", diag.Diagnostic{Headline: `v1_0.b refers to Lineage.schemas["0"]`}, `kernel term "Lineage."`},
		{"a kernel term beginning with a separator, inside a word", diag.Diagnostic{Headline: `x refers to lin_accum`}, `kernel term "_accum"`},
		{"a compatibility rule's identifier", diag.Diagnostic{Headline: "a is gone from v1.0", Notes: []diag.Note{{Label: "field-removed"}}}, namesRule},
		{"a totality rule's identifier, without a prefix", diag.Diagnostic{Headline: "v1_0.a is not total for some instance", Notes: []diag.Note{{Label: "copying v0_0.a; copy-narrowed"}}}, namesRule},
		{"a reference rule's identifier", diag.Diagnostic{Headline: "n refers to name", Notes: []diag.Note{{Label: "rule ref-lineage-meta"}}}, namesRule},
		{"a finding's own form", diag.Diagnostic{Headline: "a changed (rule whatever)"}, namesRule},
		{"a rule name in the detail", diag.Diagnostic{Headline: "v1_0.a is not total for some instance", Detail: "v1_0.a copies v0_0.a (rule source-absent)"}, "detail: " + namesRule},
		{"a rule name in an internal error's detail", diag.Diagnostic{Code: diag.E0901, Headline: "thema failed internally", Detail: "reference rule ref-lens-to-lens has no code"}, ""},
		{"a rule's words, not its identifier", diag.Diagnostic{Headline: "v1_0.a copies v0_0.a, which may be absent", Notes: []diag.Note{{Label: "a source field may be absent where it is read"}}}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.d.Code == "" {
				c.d.Code = diag.E0140
			}
			c.d.Primary = &diag.Pos{Origin: diag.OriginUser}
			r := &recorder{TB: t}
			Lint(r, []diag.Diagnostic{c.d}, Normal)
			switch {
			case c.want == "" && len(r.errs) > 0:
				t.Errorf("reported %q, want nothing", r.errs)
			case c.want != "" && !strings.Contains(strings.Join(r.errs, "\n"), c.want):
				t.Errorf("reported %q, want one containing %q", r.errs, c.want)
			}
		})
	}
}
