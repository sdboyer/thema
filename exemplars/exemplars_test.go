package exemplars

import (
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/sdboyer/thema"
)

var allctx = cuecontext.New()
var dirinst cue.Value
var allrt *thema.Runtime

func init() {
	dirinst = buildAll(allctx)
	allrt = thema.NewRuntime(allctx)
}

func TestExemplarValidity(t *testing.T) {
	iter, err := dirinst.Fields(cue.Definitions(false))
	if err != nil {
		t.Fatal(err)
	}

	for iter.Next() {
		v := iter.Value().LookupPath(cue.ParsePath("l"))
		name, _ := v.LookupPath(cue.ParsePath("name")).String()
		t.Run("Bind-"+name, func(t *testing.T) {
			t.Parallel()
			_, err := thema.BindLineage(v, allrt, nameOpts[name]...)
			if err != nil {
				// t.Fatal(errors.Details(err, nil))
				t.Fatalf("%T %+v", err, err)
			}
		})
	}
}

// Exercises the non-default branches of the defaultchange lenses, which
// previously read a nonexistent field.
func TestDefaultChangeNonDefaultBranch(t *testing.T) {
	lin, err := DefaultChangeLineage(allrt)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := thema.SchemaP(lin, thema.SV(0, 0)).Validate(allctx.CompileString(`{aunion: "baz"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []thema.SyntacticVersion{thema.SV(1, 0)} {
		tinst, _, err := inst.Translate(to)
		if err != nil {
			t.Fatalf("translating to %v: %v", to, err)
		}
		got, err := tinst.Underlying().LookupPath(cue.ParsePath("aunion")).String()
		if err != nil || got != "baz" {
			t.Fatalf("translating to %v: aunion = %q (err %v), want \"baz\"", to, got, err)
		}
		back, _, err := tinst.Translate(thema.SV(0, 0))
		if err != nil {
			t.Fatalf("translating back to 0.0: %v", err)
		}
		if got, _ := back.Underlying().LookupPath(cue.ParsePath("aunion")).String(); got != "baz" {
			t.Fatalf("round trip: aunion = %q, want \"baz\"", got)
		}
	}
}
