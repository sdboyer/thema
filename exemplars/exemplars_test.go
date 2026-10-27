package exemplars

import (
	"fmt"
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

// The exemplars' lacunas fire exactly when their conditions hold.
func TestExemplarLacunas(t *testing.T) {
	for name, c := range map[string]struct {
		lin      func(*thema.Runtime, ...thema.BindOption) (thema.Lineage, error)
		from, to thema.SyntacticVersion
		data     string
		want     []thema.LacunaType
	}{
		"narrowing, lossy":        {NarrowingLineage, thema.SV(0, 0), thema.SV(1, 0), `{boolish: "maybe"}`, []thema.LacunaType{thema.LacunaLossyFieldMapping}},
		"narrowing, clean":        {NarrowingLineage, thema.SV(0, 0), thema.SV(1, 0), `{boolish: "true"}`, nil},
		"defaultchange, forward":  {DefaultChangeLineage, thema.SV(0, 0), thema.SV(1, 0), `{aunion: "foo"}`, []thema.LacunaType{thema.LacunaChangedDefault}},
		"defaultchange, backward": {DefaultChangeLineage, thema.SV(1, 0), thema.SV(0, 0), `{aunion: "bar"}`, []thema.LacunaType{thema.LacunaChangedDefault}},
		"defaultchange, other":    {DefaultChangeLineage, thema.SV(0, 0), thema.SV(1, 0), `{aunion: "baz"}`, nil},
		"expand, baz":             {ExpandLineage, thema.SV(0, 3), thema.SV(0, 2), `{init: "x", withDefault: "baz"}`, []thema.LacunaType{thema.LacunaLossyFieldMapping}},
		"expand, kept":            {ExpandLineage, thema.SV(0, 3), thema.SV(0, 2), `{init: "x", withDefault: "bar"}`, nil},
		"expand, absent":          {ExpandLineage, thema.SV(0, 3), thema.SV(0, 2), `{init: "x"}`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			lin, err := c.lin(allrt)
			if err != nil {
				t.Fatal(err)
			}
			inst, err := thema.SchemaP(lin, c.from).Validate(allctx.CompileString(c.data))
			if err != nil {
				t.Fatal(err)
			}
			_, lac, err := inst.Translate(c.to)
			if err != nil {
				t.Fatal(err)
			}
			var got []thema.LacunaType
			for _, l := range lac.AsList() {
				got = append(got, l.Type)
			}
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("got lacunas %v, want %v", got, c.want)
			}
		})
	}
}
