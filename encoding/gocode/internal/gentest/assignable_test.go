package gentest

import (
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/exemplars"

	defaultchangev0_0 "github.com/sdboyer/thema/encoding/gocode/internal/gentest/defaultchange/v0_0"
	defaultchangev1_0 "github.com/sdboyer/thema/encoding/gocode/internal/gentest/defaultchange/v1_0"
	disjunctv0_0 "github.com/sdboyer/thema/encoding/gocode/internal/gentest/disjunct/v0_0"
	disjunctv0_1 "github.com/sdboyer/thema/encoding/gocode/internal/gentest/disjunct/v0_1"
	expandv0_0 "github.com/sdboyer/thema/encoding/gocode/internal/gentest/expand/v0_0"
	expandv0_1 "github.com/sdboyer/thema/encoding/gocode/internal/gentest/expand/v0_1"
	expandv0_2 "github.com/sdboyer/thema/encoding/gocode/internal/gentest/expand/v0_2"
	expandv0_3 "github.com/sdboyer/thema/encoding/gocode/internal/gentest/expand/v0_3"
	narrowingv0_0 "github.com/sdboyer/thema/encoding/gocode/internal/gentest/narrowing/v0_0"
	narrowingv1_0 "github.com/sdboyer/thema/encoding/gocode/internal/gentest/narrowing/v1_0"
	renamev0_0 "github.com/sdboyer/thema/encoding/gocode/internal/gentest/rename/v0_0"
	renamev1_0 "github.com/sdboyer/thema/encoding/gocode/internal/gentest/rename/v1_0"
	shapesv0_0 "github.com/sdboyer/thema/encoding/gocode/internal/gentest/shapes/v0_0"
	singlev0_0 "github.com/sdboyer/thema/encoding/gocode/internal/gentest/single/v0_0"
)

// TestAssignable checks that each schema is assignable to the type generated
// for it, per thema.AssignableTo.
func TestAssignable(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	lins := exemplars.All(rt)
	shapes, err := ShapesLineage(rt)
	if err != nil {
		t.Fatal(err)
	}
	lins["shapes"] = shapes

	const expand, optional = "expand", "optional"
	tt := []struct {
		lineage string
		version thema.SyntacticVersion
		typ     any
		// unassignable lists the schema paths AssignableTo is expected to
		// reject, where the generator and AssignableTo's rules disagree:
		//
		//   - CUE's int is unbounded, which AssignableTo accepts only for
		//     math/big.Int, but the generator emits int64 (a bounded int,
		//     like int & >=1 & <=100, is accepted);
		//   - AssignableTo reads `null | {...}` as a disjunction of structs,
		//     which it accepts only for any, but the generator emits a
		//     pointer to the struct, as it does for `null | string`, which
		//     AssignableTo accepts.
		unassignable []string
	}{
		{"defaultchange", thema.SV(0, 0), &defaultchangev0_0.Defaultchange{}, nil},
		{"defaultchange", thema.SV(1, 0), &defaultchangev1_0.Defaultchange{}, nil},
		{"disjunct", thema.SV(0, 0), &disjunctv0_0.Disjunct{}, nil},
		{"disjunct", thema.SV(0, 1), &disjunctv0_1.Disjunct{}, nil},
		{expand, thema.SV(0, 0), &expandv0_0.Expand{}, nil},
		{expand, thema.SV(0, 1), &expandv0_1.Expand{}, []string{optional}},
		{expand, thema.SV(0, 2), &expandv0_2.Expand{}, []string{optional}},
		{expand, thema.SV(0, 3), &expandv0_3.Expand{}, []string{optional}},
		{"narrowing", thema.SV(0, 0), &narrowingv0_0.Narrowing{}, nil},
		{"narrowing", thema.SV(1, 0), &narrowingv1_0.Narrowing{}, nil},
		{"rename", thema.SV(0, 0), &renamev0_0.Rename{}, nil},
		{"rename", thema.SV(1, 0), &renamev1_0.Rename{}, nil},
		{"single", thema.SV(0, 0), &singlev0_0.Single{}, []string{"anint"}},
		{"shapes", thema.SV(0, 0), &shapesv0_0.Shapes{}, []string{"parent"}},
	}

	for _, tc := range tt {
		t.Run(tc.lineage+"/"+tc.version.String(), func(t *testing.T) {
			sch, err := lins[tc.lineage].Schema(tc.version)
			if err != nil {
				t.Fatal(err)
			}
			err = thema.AssignableTo(sch, tc.typ)
			if len(tc.unassignable) == 0 {
				if err != nil {
					t.Fatalf("schema is not assignable to its generated type:\n%s", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected AssignableTo to reject %v; it accepted the type", tc.unassignable)
			}
			lines := strings.Split(err.Error(), "\n")
			if len(lines) != len(tc.unassignable) {
				t.Fatalf("expected AssignableTo to reject exactly %v, got:\n%s", tc.unassignable, err)
			}
			for _, p := range tc.unassignable {
				found := false
				for _, line := range lines {
					// An optional or required field's path carries its marker.
					if strings.HasPrefix(line, p+": ") || strings.HasPrefix(line, p+"?: ") || strings.HasPrefix(line, p+"!: ") {
						found = true
					}
				}
				if !found {
					t.Errorf("expected AssignableTo to reject %s, got:\n%s", p, err)
				}
			}
		})
	}
}
