package diagtest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sdboyer/thema/diag"
)

// recorder is a testing.TB that keeps the errors reported to it.
type recorder struct {
	testing.TB
	errs []string
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

// An error in a file under history/ needs an immutability violation for the
// file, and gives no help unless it is one. An error about the lineage's name
// is not about the file it is positioned in, and the tip is not under
// history/.
func TestLintHistory(t *testing.T) {
	at := func(file string) *diag.Pos { return &diag.Pos{File: file, Line: 1, Col: 1} }
	lead := func(file string) diag.Diagnostic {
		return diag.Diagnostic{Code: diag.E0209, Severity: diag.SeverityError, Headline: "immutability violation: v1.0 is under history/, and has an error", Primary: at(file), Help: []string{"restore it"}}
	}
	detail := func(file string, help ...string) diag.Diagnostic {
		return diag.Diagnostic{Code: diag.E0340, Severity: diag.SeverityError, Headline: "v0.0 does not declare nope", Primary: at(file), Help: help}
	}
	cases := []struct {
		name string
		ds   []diag.Diagnostic
		want []string // a substring of each error, in order
	}{
		{"led, no help", []diag.Diagnostic{lead("history/1.0.thema"), detail("history/1.0.thema")}, nil},
		{"under a directory, led", []diag.Diagnostic{lead("ops/ship/history/1.0.thema"), detail("ops/ship/history/1.0.thema")}, nil},
		{"not led", []diag.Diagnostic{detail("history/1.0.thema")}, []string{"no immutability violation for the file"}},
		{"led for another file", []diag.Diagnostic{lead("history/0.0.thema"), detail("history/1.0.thema")}, []string{"no immutability violation for the file"}},
		{"help on a detail", []diag.Diagnostic{lead("history/1.0.thema"), detail("history/1.0.thema", "add crew: <mapping> to v1_0")}, []string{"says how to edit history/1.0.thema"}},
		{"in the tip, with help", []diag.Diagnostic{detail("tip.thema", "add crew: <mapping> to tip")}, nil},
		{"the lineage's name", []diag.Diagnostic{{Code: diag.E0254, Severity: diag.SeverityError, Headline: "the lineage is named boat, but was published as ship", Primary: at("history/1.0.thema"), Address: &diag.Address{Kind: diag.AddrName}, Help: []string{"name it ship"}}}, nil},
		{"a warning", []diag.Diagnostic{{Code: diag.E1001, Severity: diag.SeverityWarning, Headline: "v1.0 has no ForwardLens", Primary: at("history/1.0.thema"), Help: []string{"write it"}}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &recorder{TB: t}
			lintHistory(r, c.ds)
			if len(r.errs) != len(c.want) {
				t.Fatalf("errors = %q, want %d", r.errs, len(c.want))
			}
			for i, w := range c.want {
				if !strings.Contains(r.errs[i], w) {
					t.Errorf("error %d = %q, want it to contain %q", i, r.errs[i], w)
				}
			}
		})
	}
}
