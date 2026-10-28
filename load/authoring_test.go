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
// as between closed structs: no implicit copy produces it (#159). What an
// instance holds under an open struct that no lens maps, translation carries
// wherever the target struct is open, promotes into a field the target
// declares when the value is an instance of it, and otherwise sets aside with
// a DroppedField lacuna (#158, #193). The derived backward lens leaves such a
// field to translation; a written _drop still drops it.
func TestAuthoringOpenStructs(t *testing.T) {
	const (
		first = "history/0.0.thema"
		open  = "Schema: {\n\ta!: string\n\t...\n}\n"
		ax    = `{a: "x"}`
		// ax, as Translate's result marshals it.
		axOut  = `{"a":"x"}`
		abf    = `{a: "x", b: false}`
		abt    = `{a: "x", b: true}`
		abfOut = `{"a":"x","b":false}`
		abtOut = `{"a":"x","b":true}`
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
		// back, if set, is the result of translating want back to from,
		// which must raise no lacuna.
		back string
	}
	for name, c := range map[string]struct {
		files        map[string]string
		translations []translation
		// err, if set, is the code loading the lineage fails with.
		err diag.Code
	}{
		"open target, added field": {
			files: map[string]string{first: open, "1.0.thema": addedRequired},
			translations: []translation{
				// Backward, b is carried as a field 0.0 does not declare, and is
				// promoted again on the way forward (#158).
				{from: thema.SV(1, 0), to: thema.SV(0, 0), data: abf, want: abfOut, back: abfOut},
				{from: thema.SV(0, 0), to: thema.SV(1, 0), data: abf, want: abfOut, back: abfOut},
				// Without a b, the lens's b stands, and is carried back.
				{from: thema.SV(0, 0), to: thema.SV(1, 0), data: ax, want: abtOut, back: abtOut},
				// A b that is no bool is set aside, and the lens's b stands.
				{from: thema.SV(0, 0), to: thema.SV(1, 0), data: `{a: "x", b: "s"}`, want: abtOut, lacunas: []lacuna.Type{lacuna.DroppedField}},
				// A field neither declares rides along both ways.
				{from: thema.SV(0, 0), to: thema.SV(1, 0), data: `{a: "x", z: 1}`, want: `{"a":"x","b":true,"z":1}`, back: `{"a":"x","b":true,"z":1}`},
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
				{from: thema.SV(1, 0), to: thema.SV(0, 0), data: abf, want: axOut, lacunas: []lacuna.Type{lacuna.LossyFieldMapping, lacuna.DroppedField}},
				{from: thema.SV(1, 0), to: thema.SV(0, 0), data: abt, want: axOut, lacunas: []lacuna.Type{lacuna.DroppedField}},
			},
		},
		"open source, added optional field": {
			files: map[string]string{first: open, "1.0.thema": "Schema: {\n\ta!: string\n\tb?: bool\n\t...\n}\n"},
			translations: []translation{
				{from: thema.SV(0, 0), to: thema.SV(1, 0), data: ax, want: axOut},
				// A b that is a bool is promoted; one that is not is set aside
				// (#159).
				{from: thema.SV(0, 0), to: thema.SV(1, 0), data: abt, want: abtOut, back: abtOut},
				{from: thema.SV(0, 0), to: thema.SV(1, 0), data: `{a: "x", b: "x"}`, want: axOut, lacunas: []lacuna.Type{lacuna.DroppedField}},
				{from: thema.SV(1, 0), to: thema.SV(0, 0), data: abt, want: abtOut},
				{from: thema.SV(1, 0), to: thema.SV(0, 0), data: ax, want: axOut},
			},
		},
		"open source, closed target": {
			files: map[string]string{first: open, "1.0.thema": "Schema: {\n\ta!: string\n}\n\nForwardLens: {\n\tv0_0: _\n\tv1_0: {}\n}\n"},
			translations: []translation{
				{from: thema.SV(0, 0), to: thema.SV(1, 0), data: `{a: "x", z: 1, y: "s"}`, want: axOut, lacunas: []lacuna.Type{lacuna.DroppedField}},
			},
		},
		"open, removed optional field": {
			files: map[string]string{first: "Schema: {\n\ta!: string\n\tb?: string\n\t...\n}\n", "0.1.thema": open},
			translations: []translation{
				{from: thema.SV(0, 0), to: thema.SV(0, 1), data: `{a: "x", b: "y"}`, want: `{"a":"x","b":"y"}`, back: `{"a":"x","b":"y"}`},
				{from: thema.SV(0, 1), to: thema.SV(0, 0), data: ax, want: axOut},
			},
		},
		// Going forward, a field the new version removes must still be moved
		// or dropped, however open the new version is: a forgotten rename
		// must not ride along under its old name.
		"open target, removed field unmapped": {
			files: map[string]string{
				first:       "Schema: {\n\tbefore: string\n\t...\n}\n",
				"1.0.thema": "Schema: {\n\tafter: string\n\t...\n}\n\nForwardLens: {\n\tv0_0: _\n\tv1_0: {\n\t\tafter: \"x\"\n\t}\n}\n",
			},
			err: diag.E0347,
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := fstest.MapFS{}
			for p, text := range c.files {
				dir["ship/"+p] = &fstest.MapFile{Data: []byte(text)}
			}
			ctx := cuecontext.New()
			lin, err := AuthoringLineage(dir, "ship", thema.NewRuntime(ctx))
			if c.err != "" {
				if !hasCode(err, c.err) {
					t.Fatalf("loading: got %v, want %s", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(cerrors.Details(err, nil))
			}
			translate := func(from, to thema.SyntacticVersion, data string) (string, []lacuna.Type) {
				t.Helper()
				inst, err := thema.SchemaP(lin, from).Validate(ctx.CompileString(data))
				if err != nil {
					t.Fatal(err)
				}
				out, lac, err := inst.Translate(to)
				if err != nil {
					t.Fatal(err)
				}
				got, err := json.Marshal(out.Underlying())
				if err != nil {
					t.Fatal(err)
				}
				var types []lacuna.Type
				for _, l := range lac.AsList() {
					types = append(types, l.Type)
				}
				return string(got), types
			}
			for _, tr := range c.translations {
				got, types := translate(tr.from, tr.to, tr.data)
				if got != tr.want {
					t.Errorf("%s %s -> %s: got %s, want %s", tr.data, tr.from, tr.to, got, tr.want)
				}
				if fmt.Sprint(types) != fmt.Sprint(tr.lacunas) {
					t.Errorf("%s %s -> %s: got lacunas %v, want %v", tr.data, tr.from, tr.to, types, tr.lacunas)
				}
				if tr.back == "" {
					continue
				}
				back, types := translate(tr.to, tr.from, got)
				if back != tr.back || len(types) > 0 {
					t.Errorf("%s %s -> %s -> %s: got %s with lacunas %v, want %s with none", tr.data, tr.from, tr.to, tr.from, back, types, tr.back)
				}
			}
		})
	}
}

func hasCode(err error, code diag.Code) bool {
	for _, d := range diag.Diagnostics(err) {
		if d.Code == code {
			return true
		}
	}
	return false
}
