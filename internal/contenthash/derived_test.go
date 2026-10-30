package contenthash_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/internal/contenthash"
)

// compileHistory compiles the lineage ship from its version files, keyed by
// their paths beneath its directory, as the authoring form's compiler writes
// it: with its derived lenses and lacunas written out.
func compileHistory(t *testing.T, files map[string]string) cue.Value {
	t.Helper()
	fsys := fstest.MapFS{}
	for p, text := range files {
		fsys["ship/"+p] = &fstest.MapFile{Data: []byte(text)}
	}
	name, fs, err := authoring.Load(fsys, "ship")
	if err != nil {
		t.Fatal(err)
	}
	c, err := authoring.Compile(cuecontext.New(), name, fs)
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	v := cuecontext.New().BuildFile(c.File)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	return v
}

// A version's derived backward lens drops what the version added, with a
// DroppedField lacuna whose message names the predecessor's version. When
// versions are inserted before the predecessor, renumbering it, the message
// names the new number; the lens does what it did, and its digest, which
// leaves messages out, is unchanged.
func TestLensDigestDerivedDropRenumbered(t *testing.T) {
	const (
		name = "Schema: {\n\tname: string\n}\n"
		crew = "Schema: {\n\tname:  string\n\tcrew?: int\n}\n"
	)
	published := compileHistory(t, map[string]string{
		"history/0.0.thema": name,
		"history/0.1.thema": crew,
	})
	// A major version is inserted before them: the published 0.0 and 0.1
	// are 1.0 and 1.1, with the same schemas, and 1.1's edge is to 1.0.
	renumbered := compileHistory(t, map[string]string{
		"history/0.0.thema": "Schema: {\n\ttitle: string\n}\n",
		"history/1.0.thema": "Schema: {\n\tname: string\n}\n\nForwardLens: {\n\tv0_0: _\n\tv1_0: {\n\t\tname: v0_0.title\n\t}\n}\n",
		"history/1.1.thema": crew,
	})

	_, pubBack, err := contenthash.LensForms(published, contenthash.Version{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	_, renBack, err := contenthash.LensForms(renumbered, contenthash.Version{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		lin  cue.Value
		edge string
		from string
	}{{published, "0.1->0.0", "0.0"}, {renumbered, "1.1->1.0", "1.0"}} {
		msg, err := c.lin.LookupPath(cue.MakePath(cue.Str("lenses"), cue.Str(c.edge), cue.Str("lacunas"), cue.Index(0), cue.Str("message"))).String()
		if err != nil {
			t.Fatalf("%s: %v", c.edge, err)
		}
		if !strings.Contains(msg, c.from) {
			t.Fatalf("%s's derived lacuna says %q, which does not name %s: the test no longer shows what it means to", c.edge, msg, c.from)
		}
	}
	if pubBack != renBack {
		t.Errorf("the derived backward lens changed with its predecessor's number:\n%s\n---\n%s", pubBack, renBack)
	}
	pub, err := contenthash.VersionID(published, contenthash.Version{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	ren, err := contenthash.VersionID(renumbered, contenthash.Version{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if pub != ren {
		t.Errorf("published 0.1 is %s, and renumbered as 1.1 it is %s", pub, ren)
	}
}
