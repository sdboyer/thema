package load

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
)

// split is fsys with the history of the lineage in dir kept in a tree of
// its own: dir/history/ moves to .history/dir/history/.
func split(fsys fstest.MapFS, dir string) fstest.MapFS {
	out := fstest.MapFS{}
	for k, v := range fsys {
		if rest, ok := strings.CutPrefix(k, dir+"/history/"); ok {
			k = ".history/" + dir + "/history/" + rest
		}
		out[k] = v
	}
	return out
}

// loader is a one-call loader, reduced to its error.
type loader struct {
	name string
	load func(fsys fstest.MapFS, dir string, opts ...LineageOption) error
}

var loaders = []loader{
	{"AuthoringLineage", func(fsys fstest.MapFS, dir string, opts ...LineageOption) error {
		_, err := AuthoringLineage(fsys, dir, thema.NewRuntime(cuecontext.New()), opts...)
		return err
	}},
	{"DevelopmentLineage", func(fsys fstest.MapFS, dir string, opts ...LineageOption) error {
		_, err := DevelopmentLineage(fsys, dir, thema.NewRuntime(cuecontext.New()), opts...)
		return err
	}},
}

// Both loaders read the history from HistoryAt's directory, and position it
// there.
func TestHistoryAt(t *testing.T) {
	const dir, hist = "lineages/ship", ".history/lineages/ship"

	t.Run("AuthoringLineage", func(t *testing.T) {
		lin, err := AuthoringLineage(split(shipDir, dir), dir, thema.NewRuntime(cuecontext.New()), HistoryAt(hist))
		if err != nil {
			t.Fatal(cerrors.Details(err, nil))
		}
		if got := versionsOf(lin); !slices.Equal(got, []string{"0.0", "1.0"}) {
			t.Errorf("versions = %v, want [0.0 1.0]", got)
		}
		// Without HistoryAt, the lineage's own directory has no history/,
		// so it has no versions.
		_, err = AuthoringLineage(split(shipDir, dir), dir, thema.NewRuntime(cuecontext.New()))
		if got := codesOf(err); len(got) != 1 || got[0] != string(diag.E0110) {
			t.Errorf("without HistoryAt: got %v, want [E0110]", got)
		}
	})

	t.Run("DevelopmentLineage", func(t *testing.T) {
		fsys := split(shipDir, dir)
		fsys[dir+"/tip.thema"] = &fstest.MapFile{Data: []byte("Schema: {\n\tfirstfield:  string\n\tsecondfield: int\n\tthird?: bool\n}\n")}
		d, err := DevelopmentLineage(fsys, dir, thema.NewRuntime(cuecontext.New()), HistoryAt(hist))
		if err != nil {
			t.Fatal(cerrors.Details(err, nil))
		}
		if got := strings.Join(versionsOf(d.Lineage), ","); got != "0.0,1.0,1.1" || !d.Provisional(thema.SV(1, 1)) {
			t.Errorf("versions = %s, tip %v; want 0.0,1.0,1.1 with 1.1 provisional", got, d.Tip)
		}
	})

	t.Run("the tip's positions", func(t *testing.T) {
		// A tip whose lens names its instance by a version number is
		// refused in the tip, which stays in the lineage's directory.
		fsys := split(shipDir, dir)
		fsys[dir+"/tip.thema"] = &fstest.MapFile{Data: []byte("Schema: {\n\tfirstfield: string\n}\n\nBackwardLens: {\n\tv2_0: _\n\tv1_0: {\n\t\tfirstfield:  v2_0.firstfield\n\t\tsecondfield: 0\n\t}\n}\n")}
		_, err := DevelopmentLineage(fsys, dir, thema.NewRuntime(cuecontext.New()), HistoryAt(hist))
		ds := diag.Diagnostics(err)
		if len(ds) == 0 {
			t.Fatalf("want the tip refused, got %v", err)
		}
		for _, d := range ds {
			if d.Primary == nil || d.Primary.File != dir+"/tip.thema" {
				t.Errorf("%s %q is positioned at %v, want %s/tip.thema", d.Code, d.Headline, d.Primary, dir)
			}
		}
	})

	for _, l := range loaders {
		t.Run(l.name, func(t *testing.T) {
			t.Run("positions", func(t *testing.T) {
				// Binding refuses narrowingShip's forward lens, in 1.0's
				// file, which is under HistoryAt's directory.
				err := l.load(split(narrowingShip, "ship"), "ship", HistoryAt(".history/ship"))
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

			both := split(shipDir, dir)
			maps.Copy(both, shipDir)
			for _, c := range []struct {
				name string
				fsys fstest.MapFS
				dir  string
				opts []LineageOption
				want diag.Code
			}{
				// The lineage's own directory still holds its history.
				{"two histories", both, dir, []LineageOption{HistoryAt(hist)}, diag.E0629},
				{"invalid HistoryAt", split(shipDir, dir), dir, []LineageOption{HistoryAt("../" + hist)}, diag.E0625},
				// The lineage's directory is read before anything positions
				// its files under it.
				{"invalid directory", split(shipDir, dir), "./" + dir, []LineageOption{HistoryAt(hist)}, diag.E0601},
			} {
				t.Run(c.name, func(t *testing.T) {
					if got := codesOf(l.load(c.fsys, c.dir, c.opts...)); len(got) != 1 || got[0] != string(c.want) {
						t.Errorf("got %v, want [%s]", got, c.want)
					}
				})
			}
		})
	}
}

// termVocabulary names every construct by one term, to tell its sentences
// from the authoring form's.
type termVocabulary struct{}

func (termVocabulary) Path(diag.Address) []string { return nil }
func (termVocabulary) Term(diag.Address) string   { return "the marked construct" }

// BindOptions reaches binding in both loaders, after the authoring form's
// vocabulary, so that a caller's option takes effect.
func TestBindOptions(t *testing.T) {
	for _, l := range loaders {
		t.Run(l.name, func(t *testing.T) {
			err := l.load(narrowingShip, "ship", BindOptions(thema.WithVocabulary(termVocabulary{})))
			ds := diag.Diagnostics(err)
			if len(ds) == 0 {
				t.Fatalf("want the narrowing lens refused, got %v", err)
			}
			for _, d := range ds {
				if !strings.Contains(d.Headline, "the marked construct") {
					t.Errorf("headline is not in the vocabulary BindOptions passed: %q", d.Headline)
				}
			}
		})
	}
}
