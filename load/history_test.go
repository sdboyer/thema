package load

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
)

// splitShip is shipDir with its history kept in a tree of its own, under
// .history/.
func splitShip() fstest.MapFS {
	fsys := fstest.MapFS{}
	for k, v := range shipDir {
		if rest, ok := strings.CutPrefix(k, "lineages/ship/history/"); ok {
			k = ".history/lineages/ship/history/" + rest
		}
		fsys[k] = v
	}
	return fsys
}

// Both entry points read the history from HistoryAt's directory, and
// position it there.
func TestHistoryAt(t *testing.T) {
	const dir, hist = "lineages/ship", ".history/lineages/ship"

	t.Run("AuthoringLineage", func(t *testing.T) {
		lin, err := AuthoringLineage(splitShip(), dir, thema.NewRuntime(cuecontext.New()), HistoryAt(hist))
		if err != nil {
			t.Fatal(cerrors.Details(err, nil))
		}
		if got := versionsOf(lin); !slices.Equal(got, []string{"0.0", "1.0"}) {
			t.Errorf("versions = %v, want [0.0 1.0]", got)
		}
	})

	t.Run("positions", func(t *testing.T) {
		// Binding refuses narrowingShip's forward lens, in 1.0's file,
		// which is under HistoryAt's directory.
		fsys := fstest.MapFS{}
		for k, v := range narrowingShip {
			if rest, ok := strings.CutPrefix(k, "ship/history/"); ok {
				k = ".history/ship/history/" + rest
			}
			fsys[k] = v
		}
		_, err := AuthoringLineage(fsys, "ship", thema.NewRuntime(cuecontext.New()), HistoryAt(".history/ship"))
		ds := diag.Diagnostics(err)
		if len(ds) == 0 {
			t.Fatalf("want the narrowing lens refused, got %v", err)
		}
		for _, d := range ds {
			if d.Primary == nil || d.Primary.File != ".history/ship/history/1.0.thema" {
				t.Errorf("%s %q is positioned at %v, want .history/ship/history/1.0.thema", d.Code, d.Headline, d.Primary)
			}
		}
	})

	t.Run("DevelopmentLineage", func(t *testing.T) {
		fsys := splitShip()
		fsys[dir+"/tip.thema"] = &fstest.MapFile{Data: []byte("Schema: {\n\tfirstfield:  string\n\tsecondfield: int\n\tthird?: bool\n}\n")}
		d, err := DevelopmentLineage(fsys, dir, thema.NewRuntime(cuecontext.New()), HistoryAt(hist))
		if err != nil {
			t.Fatal(cerrors.Details(err, nil))
		}
		if got := strings.Join(versionsOf(d.Lineage), ","); got != "0.0,1.0,1.1" || !d.Provisional(thema.SV(1, 1)) {
			t.Errorf("versions = %s, tip %v; want 0.0,1.0,1.1 with 1.1 provisional", got, d.Tip)
		}
	})

	t.Run("without HistoryAt", func(t *testing.T) {
		// The lineage's own directory has no history/, so it has no versions.
		_, err := AuthoringLineage(splitShip(), dir, thema.NewRuntime(cuecontext.New()))
		if got := codesOf(err); len(got) != 1 || got[0] != string(diag.E0110) {
			t.Errorf("got %v, want [E0110]", got)
		}
	})

	t.Run("two histories", func(t *testing.T) {
		fsys := splitShip()
		for k, v := range shipDir {
			fsys[k] = v
		}
		_, err := DevelopmentLineage(fsys, dir, thema.NewRuntime(cuecontext.New()), HistoryAt(hist))
		if got := codesOf(err); len(got) != 1 || got[0] != string(diag.E0629) {
			t.Errorf("got %v, want [E0629]", got)
		}
	})
}

// termVocabulary names every construct by one term, to tell its sentences
// from the authoring form's.
type termVocabulary struct{}

func (termVocabulary) Path(diag.Address) []string { return nil }
func (termVocabulary) Term(diag.Address) string   { return "the marked construct" }

// BindOptions reaches binding, after the authoring form's vocabulary, so
// that a caller's option takes effect.
func TestBindOptions(t *testing.T) {
	_, err := AuthoringLineage(narrowingShip, "ship", thema.NewRuntime(cuecontext.New()), BindOptions(thema.WithVocabulary(termVocabulary{})))
	ds := diag.Diagnostics(err)
	if len(ds) == 0 {
		t.Fatalf("want the narrowing lens refused, got %v", err)
	}
	for _, d := range ds {
		if !strings.Contains(d.Headline, "the marked construct") {
			t.Errorf("headline is not in the vocabulary BindOptions passed: %q", d.Headline)
		}
	}
}
