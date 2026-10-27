package authoring_test

import (
	"bytes"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema/authoring"
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

// The pieces, used as a tool would: load, compile, build, decompile, and
// format, with the decompiled files equal to the formatted input.
func TestPieces(t *testing.T) {
	fsys := fstest.MapFS{
		"ship/history/0.0.thema": {Data: []byte(v00)},
		"ship/1.0.thema":         {Data: []byte(v10)},
	}
	name, files, err := authoring.Load(fsys, "ship")
	if err != nil {
		t.Fatal(err)
	}
	if name != "ship" || len(files) != 2 {
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
	want := map[string]string{"history/0.0.thema": v00, "1.0.thema": v10}
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
