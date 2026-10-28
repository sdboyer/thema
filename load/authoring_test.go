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
