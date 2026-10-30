package load

import (
	"slices"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
)

// fleet is a collection: lineages that bind on both paths (ship, and boat,
// whose tip is a minor change), one that binds only on the normal path
// (dinghy, with no tip), ones that bind on neither (barge, whose history is
// broken; tug-boat, whose name is not an identifier; raft, whose version is
// not under history/), and what a collection does not read: a directory with
// no version files, a lineage beneath a subdirectory, and files beside the
// lineages.
func fleet() fstest.MapFS {
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	fsys := fstest.MapFS{
		"fleet/boat/history/0.0.thema":         file("Schema: {\n\tname: string\n}\n"),
		"fleet/boat/tip.thema":                 file("Schema: {\n\tname:  string\n\tsails?: int\n}\n"),
		"fleet/dinghy/history/0.0.thema":       file("Schema: {\n\toars: int\n}\n"),
		"fleet/barge/history/0.0.thema":        file("Schema: {\n\tload: string\n}\n"),
		"fleet/barge/history/0.1.thema":        file("Schema: {\n\tload: int\n}\n"),
		"fleet/barge/tip.thema":                file("Schema: {\n\tload: int\n}\n"),
		"fleet/tug-boat/history/0.0.thema":     file("Schema: {\n\tpower: int\n}\n"),
		"fleet/tug-boat/tip.thema":             file("Schema: {\n\tpower: int\n}\n"),
		"fleet/raft/0.0.thema":                 file("Schema: {\n\tlogs: int\n}\n"),
		"fleet/docs/ships.md":                  file("About the fleet.\n"),
		"fleet/harbor/yacht/tip.thema":         file("Schema: {\n\tname: string\n}\n"),
		"fleet/harbor/yacht/history/0.0.thema": file("Schema: {\n\tname: string\n}\n"),
		"fleet/README.md":                      file("The fleet.\n"),
		"fleet/lineages_test.go":               file("package fleet\n"),
	}
	for k, v := range shipDir {
		fsys["fleet/ship/"+k[len("lineages/ship/"):]] = v
	}
	fsys["fleet/ship/example.cue"] = file("category: \"Ships\"\n")
	return fsys
}

// codeSet returns the distinct codes of the diagnostics in err.
func codeSet(err error) []string {
	cs := codesOf(err)
	slices.Sort(cs)
	return slices.Compact(cs)
}

func keys[V any](m map[string]V) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

// On the normal path, each lineage binds its history alone: a lineage without
// a tip binds, and one that fails is reported on its own, by name, without
// keeping the others from binding. Nothing that is not a lineage directly
// beneath the root is read.
func TestAuthoringCollection(t *testing.T) {
	c, err := AuthoringCollection(fleet(), "fleet", thema.NewRuntime(cuecontext.New()))
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if got, want := keys(c.Lineages), []string{"boat", "dinghy", "ship"}; !slices.Equal(got, want) {
		t.Errorf("bound %v, want %v", got, want)
	}
	if got, want := c.Names(), []string{"barge", "boat", "dinghy", "raft", "ship", "tug-boat"}; !slices.Equal(got, want) {
		t.Errorf("names %v, want %v", got, want)
	}
	for name, lin := range c.Lineages {
		if lin.Name() != name {
			t.Errorf("lineage %s is named %s", name, lin.Name())
		}
	}
	if got := versionsOf(c.Lineages["boat"]); !slices.Equal(got, []string{"0.0"}) {
		t.Errorf("boat's versions are %v: the tip was read", got)
	}
	failed := map[string][]string{
		"barge":    {"E0215"},
		"raft":     {"E0148"},
		"tug-boat": {"E0137"},
	}
	if got := keys(c.Failed); !slices.Equal(got, keys(failed)) {
		t.Fatalf("failed %v, want %v", got, keys(failed))
	}
	for name, want := range failed {
		if got := codeSet(c.Failed[name]); !slices.Equal(got, want) {
			t.Errorf("%s failed with %v, want %v: %v", name, got, want, diag.Diagnostics(c.Failed[name]))
		}
	}
}

// On the development path, each lineage binds with its tip, provisional; a
// lineage without a tip fails, as it does alone.
func TestDevelopmentCollection(t *testing.T) {
	c, err := DevelopmentCollection(fleet(), "fleet", thema.NewRuntime(cuecontext.New()))
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if got, want := keys(c.Lineages), []string{"boat", "ship"}; !slices.Equal(got, want) {
		t.Errorf("bound %v, want %v", got, want)
	}
	boat := c.Lineages["boat"]
	if got := versionsOf(boat.Lineage); !slices.Equal(got, []string{"0.0", "0.1"}) {
		t.Errorf("boat's versions are %v, want its tip as 0.1", got)
	}
	if !boat.Provisional(thema.SV(0, 1)) || boat.Tip.String() != "tip (cuts as 0.1)" {
		t.Errorf("boat's tip is %v, want provisional at 0.1", boat.Tip)
	}
	if ship := c.Lineages["ship"]; !ship.Tip.Clean {
		t.Errorf("ship's tip is %v, want clean", ship.Tip)
	}
	failed := map[string][]string{
		"barge":    {"E0215"},
		"dinghy":   {"E0149"},
		"raft":     {"E0148"},
		"tug-boat": {"E0137"},
	}
	if got := keys(c.Failed); !slices.Equal(got, keys(failed)) {
		t.Fatalf("failed %v, want %v", got, keys(failed))
	}
	for name, want := range failed {
		if got := codeSet(c.Failed[name]); !slices.Equal(got, want) {
			t.Errorf("%s failed with %v, want %v: %v", name, got, want, diag.Diagnostics(c.Failed[name]))
		}
	}
}

// A lineage's failure is positioned in its own files, by their paths
// relative to its directory.
func TestCollectionFailurePosition(t *testing.T) {
	c, err := AuthoringCollection(fleet(), "fleet", thema.NewRuntime(cuecontext.New()))
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	ds := diag.Diagnostics(c.Failed["barge"])
	if len(ds) == 0 || ds[0].Primary == nil || ds[0].Primary.File != "history/0.1.thema" {
		t.Errorf("barge's failure is %+v, want it at history/0.1.thema", ds)
	}
}

// What keeps a directory from being read as a collection is the call's
// error, and there is no collection.
func TestCollectionRoot(t *testing.T) {
	tests := map[string]struct {
		root string
		want string
	}{
		"missing":    {root: "nope", want: "E0601"},
		"a file":     {root: "fleet/README.md", want: "E0623"},
		"a lineage":  {root: "fleet/ship", want: "E0624"},
		"no history": {root: "fleet/raft", want: "E0624"},
	}
	rt := thema.NewRuntime(cuecontext.New())
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c, err := AuthoringCollection(fleet(), tt.root, rt)
			if c != nil || !slices.Equal(codesOf(err), []string{tt.want}) {
				t.Errorf("got %v, %v; want no collection and %s", c, codesOf(err), tt.want)
			}
			d, err := DevelopmentCollection(fleet(), tt.root, rt)
			if d != nil || !slices.Equal(codesOf(err), []string{tt.want}) {
				t.Errorf("on the development path, got %v, %v; want no collection and %s", d, codesOf(err), tt.want)
			}
		})
	}
}

// A collection with no lineages is empty, not an error.
func TestCollectionEmpty(t *testing.T) {
	c, err := AuthoringCollection(fleet(), "fleet/docs", thema.NewRuntime(cuecontext.New()))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Names()) != 0 {
		t.Errorf("an empty collection has %v", c.Names())
	}
}
