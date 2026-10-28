package load

import (
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
)

// An authoring directory, as an external module would hold one.
var shipDir = fstest.MapFS{
	"lineages/ship/history/0.0.thema": {Data: []byte("Schema: {\n\tfirstfield: string\n}\n")},
	"lineages/ship/1.0.thema": {Data: []byte(`Schema: {
	firstfield:  string
	secondfield: int
}

ForwardLens: {
	v0_0: _
	v1_0: {
		secondfield: -1
	}
	lacunas: {
		secondfield: [{
			type:    Placeholder
			message: "-1 is a placeholder"
		}]
	}
}
`)},
}

func TestAuthoringLineage(t *testing.T) {
	ctx := cuecontext.New()
	lin, err := AuthoringLineage(shipDir, "lineages/ship", thema.NewRuntime(ctx))
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if lin.Name() != "ship" {
		t.Errorf("name = %q, want ship", lin.Name())
	}
	if got, want := lin.Latest().Version(), thema.SV(1, 0); got != want {
		t.Errorf("latest = %s, want %s", got, want)
	}
	inst, err := lin.First().Validate(ctx.CompileString(`{firstfield: "x"}`))
	if err != nil {
		t.Fatal(err)
	}
	out, lac, err := inst.Translate(thema.SV(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := out.Underlying().LookupPath(cue.ParsePath("secondfield")).Int64(); n != -1 {
		t.Errorf("secondfield = %d, want -1", n)
	}
	if len(lac.AsList()) != 1 {
		t.Errorf("want one lacuna, got %v", lac.AsList())
	}
}

// An error is positioned in the authoring file.
func TestAuthoringLineageErrorPosition(t *testing.T) {
	bad := fstest.MapFS{
		"ship/history/0.0.thema": {Data: []byte("Schema: {\n\ta: string\n}\n")},
		"ship/1.0.thema":         {Data: []byte("Schema: {\n\tb: int\n}\n")},
	}
	_, err := AuthoringLineage(bad, "ship", thema.NewRuntime(cuecontext.New()))
	if err == nil {
		t.Fatal("want an error: 1.0 maps nothing")
	}
	var found bool
	for _, e := range cerrors.Errors(err) {
		if e.Position().Filename() == "1.0.thema" {
			found = true
		}
	}
	if !found {
		t.Errorf("no error positioned in 1.0.thema:\n%s", cerrors.Details(err, nil))
	}
}

// Bind's findings about a compiled lineage are worded in the authoring form's
// terms: the ForwardLens in its version file, not the normal form's lens edge.
func TestAuthoringLineageVocabulary(t *testing.T) {
	narrowing := fstest.MapFS{
		"ship/history/0.0.thema": {Data: []byte("Schema: {\n\ta: int32\n}\n")},
		"ship/1.0.thema": {Data: []byte(`Schema: {
	a: int32 & >=0
}

ForwardLens: {
	v0_0: _
	v1_0: {
		a: v0_0.a * 2
	}
}

BackwardLens: {
	v1_0: _
	v0_0: {
		a: v1_0.a
	}
}
`)},
	}
	_, err := AuthoringLineage(narrowing, "ship", thema.NewRuntime(cuecontext.New()))
	if err == nil {
		t.Fatal("want an error: the forward lens computes a value the target field may reject")
	}
	ds := diag.Diagnostics(err)
	if len(ds) == 0 {
		t.Fatalf("no diagnostics in %v", err)
	}
	for _, d := range ds {
		if strings.Contains(d.Headline, "0.0->1.0") || !strings.Contains(d.Headline, "1.0.thema") {
			t.Errorf("headline is not in the authoring form's terms: %q", d.Headline)
		}
	}
}

// A required field with a concrete value, carried unchanged across a minor
// step, binds and translates both ways (#154). The step's BackwardLens copies k,
// and every instance of 0.1 has k, so the copy is an instance of 0.0.
func TestAuthoringRequiredConcrete(t *testing.T) {
	fsys := fstest.MapFS{
		"ship/history/0.0.thema": {Data: []byte("Schema: {\n\tk!: \"U\"\n}\n")},
		"ship/0.1.thema":         {Data: []byte("Schema: {\n\tk!: \"U\"\n\te?: string\n}\n")},
	}
	ctx := cuecontext.New()
	lin, err := AuthoringLineage(fsys, "ship", thema.NewRuntime(ctx))
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	for _, step := range []struct {
		from, to thema.SyntacticVersion
		data     string
	}{
		{thema.SV(0, 0), thema.SV(0, 1), `{k: "U"}`},
		{thema.SV(0, 1), thema.SV(0, 0), `{k: "U"}`},
		{thema.SV(0, 1), thema.SV(0, 0), `{k: "U", e: "x"}`},
	} {
		sch, err := lin.Schema(step.from)
		if err != nil {
			t.Fatal(err)
		}
		inst, err := sch.Validate(ctx.CompileString(step.data))
		if err != nil {
			t.Fatalf("%s %s: %v", step.from, step.data, err)
		}
		out, _, err := inst.Translate(step.to)
		if err != nil {
			t.Fatalf("%s->%s %s: %s", step.from, step.to, step.data, cerrors.Details(err, nil))
		}
		if k, err := out.Underlying().LookupPath(cue.ParsePath("k")).String(); err != nil || k != "U" {
			t.Errorf("%s->%s %s: k = %q (%v), want \"U\"", step.from, step.to, step.data, k, err)
		}
		if e := out.Underlying().LookupPath(cue.ParsePath("e")); step.to == thema.SV(0, 0) && e.Exists() {
			t.Errorf("%s->%s %s: e survived into 0.0", step.from, step.to, step.data)
		}
	}
}
