package gentest

import (
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/exemplars"
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
		{"defaultchange", thema.SV(0, 0), &DefaultchangeV0_0{}, nil},
		{"defaultchange", thema.SV(1, 0), &DefaultchangeV1_0{}, nil},
		{"disjunct", thema.SV(0, 0), &DisjunctV0_0{}, nil},
		{"disjunct", thema.SV(0, 1), &DisjunctV0_1{}, nil},
		{"expand", thema.SV(0, 0), &ExpandV0_0{}, nil},
		{"expand", thema.SV(0, 1), &ExpandV0_1{}, []string{"optional"}},
		{"expand", thema.SV(0, 2), &ExpandV0_2{}, []string{"optional"}},
		{"expand", thema.SV(0, 3), &ExpandV0_3{}, []string{"optional"}},
		{"narrowing", thema.SV(0, 0), &NarrowingV0_0{}, nil},
		{"narrowing", thema.SV(1, 0), &NarrowingV1_0{}, nil},
		{"rename", thema.SV(0, 0), &RenameV0_0{}, nil},
		{"rename", thema.SV(1, 0), &RenameV1_0{}, nil},
		{"single", thema.SV(0, 0), &SingleV0_0{}, []string{"anint"}},
		{"shapes", thema.SV(0, 0), &ShapesV0_0{}, []string{"parent"}},
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
