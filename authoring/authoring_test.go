package authoring_test

import (
	"bytes"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
)

const v00 = "Schema: {\n\tbefore: string\n}\n"

const v10 = `Schema: {
	after: string
}

ForwardLens: {
	v0_0: _
	v1_0: {
		after: v0_0.before
	}
}
`

// tip is the tip just after 1.0 was cut: 1.0's schema, with no lenses.
const tip = "Schema: {\n\tafter: string\n}\n"

// The pieces, used as a tool would: load, compile, build, decompile, and
// format, with the decompiled files equal to the formatted input.
func TestPieces(t *testing.T) {
	fsys := fstest.MapFS{
		"ship/history/0.0.thema": {Data: []byte(v00)},
		"ship/history/1.0.thema": {Data: []byte(v10)},
		"ship/tip.thema":         {Data: []byte(tip)},
	}
	name, files, err := authoring.Load(fsys, "ship")
	if err != nil {
		t.Fatal(err)
	}
	if name != "ship" || len(files) != 3 {
		t.Fatalf("Load: name %q, %d files", name, len(files))
	}
	ctx := cuecontext.New()
	c, err := authoring.Compile(ctx, name, files)
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if len(c.Versions) != 2 {
		t.Fatalf("versions: %v", c.Versions)
	}
	v := ctx.BuildFile(c.File)
	if verr := v.Err(); verr != nil {
		t.Fatal(verr)
	}
	dname, dfiles, err := authoring.Decompile(v)
	if err != nil {
		t.Fatal(err)
	}
	if dname != name {
		t.Errorf("Decompile: name %q", dname)
	}
	want := map[string]string{"history/0.0.thema": v00, "history/1.0.thema": v10, "tip.thema": tip}
	if len(dfiles) != len(want) {
		t.Errorf("Decompile: %d files, want %d", len(dfiles), len(want))
	}
	for _, f := range dfiles {
		formatted, err := authoring.Format([]byte(want[f.Path]))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(f.Text, formatted) {
			t.Errorf("%s decompiled as\n%s\nwant\n%s", f.Path, f.Text, formatted)
		}
	}
}

// Two lineages loaded from different directories of one fs.FS, compiled with
// their files positioned under their directories, and composed into one
// value, name their own files: in the value, and in bind's diagnostics, though
// each lineage's files have the same paths within its directory (#254).
func TestAtComposed(t *testing.T) {
	// The forward lens computes a value its target may reject, which bind
	// reports in history/1.0.thema.
	const partial = `Schema: {
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
`
	fsys := fstest.MapFS{}
	dirs := map[string]string{"get": "operations/get/query", "list": "operations/list/query"}
	for _, dir := range dirs {
		fsys[dir+"/history/0.0.thema"] = &fstest.MapFile{Data: []byte("Schema: {\n\ta: int32\n}\n")}
		fsys[dir+"/history/1.0.thema"] = &fstest.MapFile{Data: []byte(partial)}
	}

	ctx := cuecontext.New()
	rt := thema.NewRuntime(ctx)
	whole := ctx.CompileString("get: _, list: _")
	compiled := map[string]*authoring.Compiled{}
	for op, dir := range dirs {
		name, files, err := authoring.Load(fsys, dir)
		if err != nil {
			t.Fatal(err)
		}
		c, err := authoring.Compile(ctx, name, files, authoring.At(dir))
		if err != nil {
			t.Fatal(cerrors.Details(err, nil))
		}
		compiled[op] = c
		whole = whole.FillPath(cue.MakePath(cue.Str(op)), ctx.BuildFile(c.File))
	}
	if err := whole.Err(); err != nil {
		t.Fatal(err)
	}

	for op, dir := range dirs {
		lin := whole.LookupPath(cue.MakePath(cue.Str(op)))
		a := lin.LookupPath(cue.ParsePath(`schemas."0"."0".schema.a`))
		if got, want := a.Pos().Filename(), dir+"/history/0.0.thema"; got != want {
			t.Errorf("%s: 0.0's a is positioned in %s, want %s", op, got, want)
		}

		_, err := thema.BindLineage(lin, rt, thema.WithVocabulary(authoring.Vocabulary(compiled[op])))
		ds := diag.Diagnostics(err)
		if len(ds) == 0 {
			t.Fatalf("%s: want bind to refuse the forward lens", op)
		}
		for _, d := range ds {
			if d.Primary == nil || d.Primary.File != dir+"/history/1.0.thema" {
				t.Errorf("%s: %s %q is positioned at %v, want %s/history/1.0.thema", op, d.Code, d.Headline, d.Primary, dir)
			}
		}
	}
}
