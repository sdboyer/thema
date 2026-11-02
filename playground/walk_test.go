package playground

import (
	"slices"
	"testing"
	"testing/fstest"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/diagtest"
)

// walkRoot is the root of the tree lineages returns.
const walkRoot = "lineages"

// lineages is a tree of lineages in the authoring form: three examples, one
// of which does not bind; a lineage that is not an example, with no tip; and
// a directory that holds no lineage.
func lineages() fstest.MapFS {
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	return fstest.MapFS{
		"lineages/pets/dog/history/0.0.thema":    file("Schema: {\n\tname: string\n}\n"),
		"lineages/pets/dog/history/1.0.thema":    file("Schema: {\n\tname:  string\n\tbreed: string\n}\n\nForwardLens: {\n\tv0_0: _\n\tv1_0: {\n\t\tbreed: \"unknown\"\n\t}\n}\n"),
		"lineages/pets/dog/tip.thema":            file("Schema: {\n\tname:  string\n\tbreed: string\n}\n"),
		"lineages/pets/dog/example.cue":          file("// A dog.\ncategory: \"Pets\"\ntitle:    \"Dog\"\nsummary:  \"A dog, and a breed added at 1.0.\"\n"),
		"lineages/pets/broken/history/0.0.thema": file("Schema: {\n\tname: string\n}\n"),
		"lineages/pets/broken/history/0.1.thema": file("Schema: {\n\tname: int\n}\n"),
		"lineages/pets/broken/tip.thema":         file("Schema: {\n\tname: int\n}\n"),
		"lineages/pets/broken/example.cue":       file("category: \"Pet errors\"\n"),
		"lineages/pets/cat/history/0.0.thema":    file("Schema: {\n\tlives: int\n}\n"),
		"lineages/pets/cat/tip.thema":            file("Schema: {\n\tlives: int\n}\n"),
		"lineages/pets/cat/example.cue":          file("category: \"Pets\"\n"),
		"lineages/wip/draft/history/0.0.thema":   file("Schema: {\n\tx: int\n}\n"),
		"lineages/notes/readme.md":               file("Not a lineage.\n"),
	}
}

// Every lineage with an example.cue is one example, described by it, with
// whether it binds found by binding it; the rest of the tree is not. What the
// walk finds registers without a diagnostic.
func TestWalkExamples(t *testing.T) {
	req, err := WalkExamples(lineages(), walkRoot)
	if err != nil {
		t.Fatal(diag.Diagnostics(err))
	}
	want := []ExampleInfo{
		{ID: "pets/broken", Title: "broken", Category: "Pet errors", Kind: kindAuthoring, Binds: false},
		{ID: "pets/cat", Title: "cat", Category: "Pets", Kind: kindAuthoring, Binds: true},
		{ID: "pets/dog", Title: "Dog", Category: "Pets", Summary: "A dog, and a breed added at 1.0.", Kind: kindAuthoring, Binds: true},
	}
	var got []ExampleInfo
	for _, h := range req.Examples {
		got = append(got, h.Example)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("walked\n%+v\nwant\n%+v", got, want)
	}
	dog := req.Examples[2].Lineage
	if dog.Name != "dog" || len(dog.Files) != 3 || dog.Normal != nil {
		t.Errorf("pets/dog's lineage is %+v, want its tip and two frozen versions under its name", dog)
	}

	s := newSession(t)
	reg := s.RegisterExamples(req)
	if !reg.OK || len(reg.Registered) != len(want) {
		t.Fatalf("registering the walked examples: %+v", reg)
	}
}

// A root that is itself a lineage is one example, named for the root.
func TestWalkExamplesRootLineage(t *testing.T) {
	req, err := WalkExamples(lineages(), "lineages/pets/dog")
	if err != nil {
		t.Fatal(diag.Diagnostics(err))
	}
	if len(req.Examples) != 1 || req.Examples[0].Example.ID != "dog" {
		t.Fatalf("walked %+v, want one example, dog", req.Examples)
	}
}

// Each problem with a walk is a diagnostic, and returns no examples.
func TestWalkExamplesFails(t *testing.T) {
	tests := map[string]struct {
		root    string
		cat     string // pets/cat's example.cue, when set
		want    []diag.Code
		noteFor string // a lineage named in a note
	}{
		"missing root":    {root: "nope", want: []diag.Code{diag.E0601}},
		"root is a file":  {root: "lineages/notes/readme.md", want: []diag.Code{diag.E0622}},
		"no example":      {root: "lineages/wip", want: []diag.Code{diag.E0622}, noteFor: "lineages/wip/draft"},
		"not CUE":         {root: walkRoot, cat: "category: \"Pets\n", want: []diag.Code{diag.E0621}},
		"no category":     {root: walkRoot, cat: "title: \"cat\"\n", want: []diag.Code{diag.E0621}},
		"other fields":    {root: walkRoot, cat: "category: \"Pets\"\ncolor: \"grey\"\nlives: 9\n", want: []diag.Code{diag.E0621, diag.E0621}},
		"not a string":    {root: walkRoot, cat: "category: 3\n", want: []diag.Code{diag.E0621}},
		"not a constant":  {root: walkRoot, cat: "category: string\n", want: []diag.Code{diag.E0621}},
		"all are counted": {root: walkRoot, cat: "category: 3\ntitle: 4\n", want: []diag.Code{diag.E0621, diag.E0621}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fsys := lineages()
			if tt.cat != "" {
				fsys["lineages/pets/cat/example.cue"] = &fstest.MapFile{Data: []byte(tt.cat)}
			}
			req, err := WalkExamples(fsys, tt.root)
			if err == nil {
				t.Fatalf("the walk succeeded: %+v", req)
			}
			if len(req.Examples) != 0 {
				t.Errorf("a failed walk returned %d examples", len(req.Examples))
			}
			ds := diag.Diagnostics(err)
			diagtest.Lint(t, ds, diagtest.Normal)
			var codes []diag.Code
			for _, d := range ds {
				codes = append(codes, d.Code)
			}
			if !slices.Equal(codes, tt.want) {
				t.Fatalf("codes %v, want %v: %+v", codes, tt.want, ds)
			}
			if tt.noteFor != "" && (len(ds[0].Notes) != 1 || ds[0].Notes[0].Label != tt.noteFor+" holds a lineage but no example.cue") {
				t.Errorf("notes %+v, want one naming %s", ds[0].Notes, tt.noteFor)
			}
		})
	}
}
