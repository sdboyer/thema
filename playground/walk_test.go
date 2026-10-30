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

// lineages is a collection of lineages in the authoring form: three
// examples, one of which does not bind; a lineage that is not an example,
// with no tip; a directory that holds no lineage, and one that holds a
// lineage only beneath it, which a flat collection does not read; and a file
// beside the lineages. wip is a collection with no example.
func lineages() fstest.MapFS {
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	return fstest.MapFS{
		"lineages/dog/history/0.0.thema":    file("Schema: {\n\tname: string\n}\n"),
		"lineages/dog/history/1.0.thema":    file("Schema: {\n\tname:  string\n\tbreed: string\n}\n\nForwardLens: {\n\tv0_0: _\n\tv1_0: {\n\t\tbreed: \"unknown\"\n\t}\n}\n"),
		"lineages/dog/tip.thema":            file("Schema: {\n\tname:  string\n\tbreed: string\n}\n"),
		"lineages/dog/example.cue":          file("// A dog.\ncategory: \"Pets\"\ntitle:    \"Dog\"\nsummary:  \"A dog, and a breed added at 1.0.\"\n"),
		"lineages/broken/history/0.0.thema": file("Schema: {\n\tname: string\n}\n"),
		"lineages/broken/history/0.1.thema": file("Schema: {\n\tname: int\n}\n"),
		"lineages/broken/tip.thema":         file("Schema: {\n\tname: int\n}\n"),
		"lineages/broken/example.cue":       file("category: \"Pet errors\"\n"),
		"lineages/cat/history/0.0.thema":    file("Schema: {\n\tlives: int\n}\n"),
		"lineages/cat/tip.thema":            file("Schema: {\n\tlives: int\n}\n"),
		"lineages/cat/example.cue":          file("category: \"Pets\"\n"),
		"lineages/draft/history/0.0.thema":  file("Schema: {\n\tx: int\n}\n"),
		"lineages/notes/readme.md":          file("Not a lineage.\n"),
		"lineages/pets/hamster/tip.thema":   file("Schema: {\n\tname: string\n}\n"),
		"lineages/pets/hamster/example.cue": file("category: \"Pets\"\n"),
		"lineages/README.md":                file("The examples.\n"),
		"wip/draft/history/0.0.thema":       file("Schema: {\n\tx: int\n}\n"),
	}
}

// Every lineage with an example.cue is one example, described by it, with
// whether it binds found by binding it; the rest of the collection is not,
// and a lineage beneath one of its subdirectories is not in it. What the
// walk finds registers without a diagnostic.
func TestWalkExamples(t *testing.T) {
	req, err := WalkExamples(lineages(), walkRoot)
	if err != nil {
		t.Fatal(diag.Diagnostics(err))
	}
	want := []ExampleInfo{
		{ID: "broken", Title: "broken", Category: "Pet errors", Kind: kindAuthoring, Binds: false},
		{ID: "cat", Title: "cat", Category: "Pets", Kind: kindAuthoring, Binds: true},
		{ID: "dog", Title: "Dog", Category: "Pets", Summary: "A dog, and a breed added at 1.0.", Kind: kindAuthoring, Binds: true},
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
		t.Errorf("dog's lineage is %+v, want its tip and two frozen versions under its name", dog)
	}

	s := newSession(t)
	reg := s.RegisterExamples(req)
	if !reg.OK || len(reg.Registered) != len(want) {
		t.Fatalf("registering the walked examples: %+v", reg)
	}
}

// Each problem with a walk is a diagnostic, and returns no examples.
func TestWalkExamplesFails(t *testing.T) {
	tests := map[string]struct {
		root    string
		cat     string // cat's example.cue, when set
		want    []diag.Code
		noteFor string // a lineage named in a note
	}{
		"missing root":      {root: "nope", want: []diag.Code{diag.E0601}},
		"root is a file":    {root: "lineages/notes/readme.md", want: []diag.Code{diag.E0623}},
		"root is a lineage": {root: "lineages/dog", want: []diag.Code{diag.E0624}},
		"no example":        {root: "wip", want: []diag.Code{diag.E0622}, noteFor: "wip/draft"},
		"not CUE":           {root: walkRoot, cat: "category: \"Pets\n", want: []diag.Code{diag.E0621}},
		"no category":       {root: walkRoot, cat: "title: \"cat\"\n", want: []diag.Code{diag.E0621}},
		"other fields":      {root: walkRoot, cat: "category: \"Pets\"\ncolor: \"grey\"\nlives: 9\n", want: []diag.Code{diag.E0621, diag.E0621}},
		"not a string":      {root: walkRoot, cat: "category: 3\n", want: []diag.Code{diag.E0621}},
		"not a constant":    {root: walkRoot, cat: "category: string\n", want: []diag.Code{diag.E0621}},
		"all are counted":   {root: walkRoot, cat: "category: 3\ntitle: 4\n", want: []diag.Code{diag.E0621, diag.E0621}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fsys := lineages()
			if tt.cat != "" {
				fsys["lineages/cat/example.cue"] = &fstest.MapFile{Data: []byte(tt.cat)}
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
