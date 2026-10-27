package compile

import (
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"

	"github.com/google/go-cmp/cmp"

	"github.com/sdboyer/thema"
)

// TestRoundTrip compiles each archive in testdata/roundtrip, binds the result
// and decompiles it again. The archive holds a lineage's authoring files under
// its name (ship/1.0.thema, ship/history/0.0.thema) and, as goldens:
//
//   - out/normal.cue: the compiled normal form;
//   - out/bind: "ok", or BindLineage's error;
//   - out/decompiled/<path>: the authoring files decompiled from the normal
//     form, which must equal the input up to formatting;
//   - out/error: the compile error, for an archive that must not compile; the
//     other sections are then absent.
func TestRoundTrip(t *testing.T) {
	files, err := filepath.Glob("testdata/roundtrip/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".txtar"), func(t *testing.T) {
			a := readArchive(t, file)
			name, inputs := a.lineage(t)
			got := make(map[string]string)

			ctx := cuecontext.New()
			c, err := Compile(ctx, name, inputs)
			if err != nil {
				got["out/error"] = cerrors.Details(err, nil)
				a.compare(t, got)
				return
			}
			text, terr := c.Text()
			if terr != nil {
				t.Fatal(terr)
			}
			got["out/normal.cue"] = text

			v := ctx.BuildFile(c.File)
			if v.Err() != nil {
				t.Fatalf("building the normal form: %v", cerrors.Details(v.Err(), nil))
			}
			if _, berr := thema.BindLineage(v, thema.NewRuntime(ctx)); berr != nil {
				got["out/bind"] = cerrors.Details(berr, nil)
			} else {
				got["out/bind"] = "ok\n"
			}

			dname, dfiles, err := Decompile(v)
			if err != nil {
				t.Fatalf("decompiling: %v", err)
			}
			if dname != name {
				t.Errorf("decompiled name %q, want %q", dname, name)
			}
			want := make(map[string]string)
			for _, f := range inputs {
				want[f.Path] = canonical(t, f.Path, f.Text)
			}
			for _, f := range dfiles {
				got["out/decompiled/"+f.Path] = string(f.Text)
				if w, ok := want[f.Path]; !ok {
					t.Errorf("decompiled %s, which was not an input", f.Path)
				} else if diff := cmp.Diff(w, canonical(t, f.Path, f.Text)); diff != "" {
					t.Errorf("decompiled %s differs from the input (-input +decompiled):\n%s", f.Path, diff)
				}
				delete(want, f.Path)
			}
			for p := range want {
				t.Errorf("input %s was not decompiled", p)
			}

			// Compiling the decompiled form gives the same normal form.
			c2, err := Compile(ctx, dname, dfiles)
			if err != nil {
				t.Fatalf("recompiling the decompiled form: %v", cerrors.Details(err, nil))
			}
			text2, err := c2.Text()
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(canonical(t, "normal.cue", []byte(text)), canonical(t, "normal.cue", []byte(text2))); diff != "" {
				t.Errorf("recompiled normal form differs (-first +recompiled):\n%s", diff)
			}
			a.compare(t, got)
		})
	}
}

// canonical formats CUE text with its layout forgotten, so that two texts
// compare equal when they differ only in formatting: braces around a single
// field, blank lines, alignment.
func canonical(t *testing.T, name string, text []byte) string {
	t.Helper()
	f, err := parser.ParseFile(name, text, parser.ParseComments)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	ast.Walk(f, func(n ast.Node) bool {
		ast.SetPos(n, token.NoPos)
		return true
	}, nil)
	b, err := format.Node(f)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return string(b)
}

// lineage returns the archive's authoring files: those under its first
// directory, which names the lineage.
func (a *archive) lineage(t *testing.T) (string, []File) {
	t.Helper()
	var name string
	var files []File
	for _, f := range a.ar.Files {
		if strings.HasPrefix(f.Name, "out/") {
			continue
		}
		dir, rest, ok := strings.Cut(f.Name, "/")
		if !ok {
			t.Fatalf("%s: file %s is not under the lineage's directory", a.path, f.Name)
		}
		if name == "" {
			name = dir
		} else if dir != name {
			t.Fatalf("%s: files under %s and %s; an archive holds one lineage", a.path, name, dir)
		}
		files = append(files, File{Path: rest, Text: f.Data})
	}
	return name, files
}
