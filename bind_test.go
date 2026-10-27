package thema

import (
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
)

// Bind errors found from a lens's syntax still point at the line the author
// wrote, however the offending value is reached: the syntax used to find the
// problem has no positions, so the error must take its position from the
// lineage's own value.
func TestLensSyntaxErrorPositions(t *testing.T) {
	const unconditional = "listed unconditionally"
	ctx := cuecontext.New()
	rt := NewRuntime(ctx)
	// loadLineageValue puts 4 lines before src.
	const header = 4
	lens := func(body string) string {
		return `lin: thema.Lineage & {
	name: "positions"
	schemas: "0": {"0": schema: {a: string}, "1": schema: {a: string, b?: int}}
	lenses: "0.1->0.0": {
		v0_1: _
` + body + `
	}
}`
	}
	for name, c := range map[string]struct {
		body   string
		marker string // the error must point at the line containing this
		err    string
	}{
		"conditional result": {
			body:   "\t\tv0_0: {a: v0_1.a, if v0_1.a == \"x\" {extra: 1}}",
			marker: "v0_0:", err: "extra",
		},
		"conditional result via let": {
			body:   "\t\tv0_0: R\n\t\tlet R = {a: v0_1.a, if v0_1.a == \"x\" {extra: 1}}",
			marker: "v0_0:", err: "extra",
		},
		"conditional lacunas": {
			body:   "\t\tv0_0: {a: v0_1.a}\n\t\tlacunas: [if v0_1.b != _|_ {thema.Lacuna & {message: \"m\", type: \"DroppedField\"}}]",
			marker: "lacunas:", err: unconditional,
		},
		"conditional lacunas via let": {
			body:   "\t\tv0_0: {a: v0_1.a}\n\t\tlacunas: L\n\t\tlet L = [if v0_1.b != _|_ {thema.Lacuna & {message: \"m\", type: \"DroppedField\"}}]",
			marker: "lacunas:", err: unconditional,
		},
	} {
		t.Run(name, func(t *testing.T) {
			src := lens(c.body)
			want := header + 1
			for i, line := range strings.Split(src, "\n") {
				if strings.Contains(line, c.marker) {
					want += i
					break
				}
			}
			_, err := BindLineage(loadLineageValue(t, ctx, src), rt)
			if err == nil {
				t.Fatal("bind succeeded, want an error")
			}
			var found bool
			for _, e := range cerrors.Errors(err) {
				if !strings.Contains(e.Error(), c.err) {
					continue
				}
				found = true
				if got := e.Position().Line(); got != want {
					t.Errorf("error %q is at line %d, want %d (%q)", e.Error(), got, want, c.marker)
				}
			}
			if !found {
				t.Fatalf("no error mentions %q: %s", c.err, cerrors.Details(err, nil))
			}
		})
	}
}
