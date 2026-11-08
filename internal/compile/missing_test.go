package compile

import (
	"slices"
	"testing"

	"cuelang.org/go/cue/token"

	cerrors "cuelang.org/go/cue/errors"
	"github.com/sdboyer/thema/diag"
)

// A finding E1001 folds in keeps its notes, after its own, each where it was,
// or where the finding is when it has no position: nothing the finding said
// is dropped, and every renderer lists it under the finding.
func TestMissingLensKeepsFindingNotes(t *testing.T) {
	prev, err := parseVersion(File{Path: "history/0.0.thema", Text: []byte("Schema: {\n\tid: string\n}\n")}, "history/0.0.thema", false)
	if err != nil {
		t.Fatal(err)
	}
	prev.v = [2]uint{0, 0}
	v, err := parseVersion(File{Path: TipFile, Text: []byte("Schema: {\n\tid: int\n}\n")}, TipFile, true)
	if err != nil {
		t.Fatal(err)
	}
	v.v = [2]uint{1, 0}
	at := func(f *version, line int) token.Pos { return f.tf.Pos(len("Schema: {\n")*(line-1), token.NoRelPos) }
	finding := diag.New(diag.E0358).At(at(v, 2)).
		Headline("a finding").
		Note(token.NoPos, "loose").
		Note(at(prev, 2), "in the source").
		Note(at(v, 2), "in the tip").
		Err()
	var errs cerrors.Error = finding
	d := diag.Diagnostics(missingLens(v, prev, fieldForward, errs))[0]
	type note struct {
		file  string
		line  int
		label string
	}
	var got []note
	for _, n := range d.Notes {
		if n.Pos == nil {
			got = append(got, note{label: n.Label})
			continue
		}
		got = append(got, note{n.Pos.File, n.Pos.Line, n.Label})
	}
	want := []note{
		{TipFile, 2, "a finding (E0358)"},
		{TipFile, 2, "loose"},
		{"history/0.0.thema", 2, "in the source"},
		{TipFile, 2, "in the tip"},
	}
	if len(got) < len(want) || !slices.Equal(got[:len(want)], want) {
		t.Errorf("notes:\n got %v\nwant %v first", got, want)
	}
}
