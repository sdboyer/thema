package load

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/lacuna"
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

// A name an open struct admits through its ... is not one it declares, so a
// field one version declares and the other only admits is added or removed,
// as between closed structs: no implicit copy produces it, and the derived
// backward lens drops it with a DroppedField lacuna (#158, #159).
func TestAuthoringOpenStructs(t *testing.T) {
	const (
		first = "history/0.0.thema"
		open  = "Schema: {\n\ta!: string\n\t...\n}\n"
		ax    = `{a: "x"}`
		// ax, as Translate's result marshals it.
		axOut = `{"a":"x"}`
	)
	const addedRequired = `Schema: {
	a!: string
	b!: bool
	...
}

ForwardLens: {
	v0_0: _
	v1_0: {
		b: true
	}
}
`
	type translation struct {
		from, to thema.SyntacticVersion
		data     string
		want     string
		lacunas  []lacuna.Type
	}
	for name, c := range map[string]struct {
		files        map[string]string
		translations []translation
	}{
		"open target, added field": {
			files: map[string]string{first: open, "1.0.thema": addedRequired},
			translations: []translation{
				{thema.SV(1, 0), thema.SV(0, 0), `{a: "x", b: false}`, axOut, []lacuna.Type{lacuna.DroppedField}},
			},
		},
		"open target, written drop": {
			files: map[string]string{first: open, "1.0.thema": addedRequired + `
BackwardLens: {
	v1_0: _
	v0_0: {
		b: _drop
	}
	lacunas: {
		b: [{
			type:    LossyFieldMapping
			message: "b was false, which 0.0 cannot say"
			when:    !v1_0.b
		}]
	}
}
`},
			translations: []translation{
				{thema.SV(1, 0), thema.SV(0, 0), `{a: "x", b: false}`, axOut, []lacuna.Type{lacuna.LossyFieldMapping, lacuna.DroppedField}},
				{thema.SV(1, 0), thema.SV(0, 0), `{a: "x", b: true}`, axOut, []lacuna.Type{lacuna.DroppedField}},
			},
		},
		"open source, added optional field": {
			files: map[string]string{first: open, "1.0.thema": "Schema: {\n\ta!: string\n\tb?: bool\n\t...\n}\n"},
			translations: []translation{
				{thema.SV(0, 0), thema.SV(1, 0), ax, axOut, nil},
				{thema.SV(1, 0), thema.SV(0, 0), `{a: "x", b: true}`, axOut, []lacuna.Type{lacuna.DroppedField}},
				{thema.SV(1, 0), thema.SV(0, 0), ax, axOut, nil},
			},
		},
		"open, removed optional field": {
			files: map[string]string{first: "Schema: {\n\ta!: string\n\tb?: string\n\t...\n}\n", "0.1.thema": open},
			translations: []translation{
				{thema.SV(0, 0), thema.SV(0, 1), `{a: "x", b: "y"}`, `{"a":"x","b":"y"}`, nil},
				{thema.SV(0, 1), thema.SV(0, 0), ax, axOut, nil},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := fstest.MapFS{}
			for p, text := range c.files {
				dir["ship/"+p] = &fstest.MapFile{Data: []byte(text)}
			}
			ctx := cuecontext.New()
			lin, err := AuthoringLineage(dir, "ship", thema.NewRuntime(ctx))
			if err != nil {
				t.Fatal(cerrors.Details(err, nil))
			}
			for _, tr := range c.translations {
				inst, err := thema.SchemaP(lin, tr.from).Validate(ctx.CompileString(tr.data))
				if err != nil {
					t.Fatal(err)
				}
				out, lac, err := inst.Translate(tr.to)
				if err != nil {
					t.Fatal(err)
				}
				got, err := json.Marshal(out.Underlying())
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != tr.want {
					t.Errorf("%s %s -> %s: got %s, want %s", tr.data, tr.from, tr.to, got, tr.want)
				}
				var types []lacuna.Type
				for _, l := range lac.AsList() {
					types = append(types, l.Type)
				}
				if fmt.Sprint(types) != fmt.Sprint(tr.lacunas) {
					t.Errorf("%s %s -> %s: got lacunas %v, want %v", tr.data, tr.from, tr.to, types, tr.lacunas)
				}
			}
		})
	}
}
