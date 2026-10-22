package thema

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
)

// The lineage the tutorials build up to (docs/ship.cue) must bind, and behave as
// the tutorials describe.
func TestDocsShip(t *testing.T) {
	src, err := os.ReadFile("docs/ship.cue")
	if err != nil {
		t.Fatal(err)
	}
	// loadLineageValue supplies the package clause and import itself.
	body := string(src)
	body = body[strings.Index(body, "lin:"):]

	ctx := cuecontext.New()
	lin, err := bindString(t, NewRuntime(ctx), body)
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}

	inst, err := lin.First().Validate(ctx.CompileString(`{firstfield: "foobar"}`))
	if err != nil {
		t.Fatal(err)
	}
	out, lac, err := inst.Translate(SV(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := out.Underlying().LookupPath(cue.ParsePath("secondfield")).Int64(); got != -1 {
		t.Errorf("secondfield = %d, want the placeholder -1", got)
	}
	if l := lac.AsList(); len(l) != 1 || l[0].Type != LacunaPlaceholder {
		t.Errorf("want one Placeholder lacuna, got %+v", l)
	}
}

// Each complete lineage in the authoring tutorial must be accepted or rejected
// exactly as the tutorial's prose says.
func TestDocsAuthoringSnippets(t *testing.T) {
	b, err := os.ReadFile("docs/authoring.md")
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{
		false, // scaffolding: no schemas yet
		true,  // a single schema
		false, // 0.1 adds a required field: not backward compatible
		true,  // 0.1 adds an optional field, with a lens back
		false, // 1.0 without the lenses a major version requires
		true,  // 1.0 with lenses
		true,  // the complete lineage, with lacunas
	}
	snippets := regexp.MustCompile("(?s)```cue\n(.*?)```").FindAllStringSubmatch(string(b), -1)
	if len(snippets) != len(want) {
		t.Fatalf("found %d CUE snippets in docs/authoring.md, expectations for %d", len(snippets), len(want))
	}
	rt := NewRuntime(cuecontext.New())
	for i, m := range snippets {
		body := m[1][strings.Index(m[1], "lin:"):]
		_, err := bindString(t, rt, body)
		if (err == nil) != want[i] {
			t.Errorf("snippet %d: bound = %v, want %v (%v)", i, err == nil, want[i], err)
		}
	}
}
