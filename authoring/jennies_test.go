package authoring_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/sdboyer/jenny"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
)

// rendered returns the path and contents of each file jenny generated.
func rendered(t *testing.T, fl jenny.Files) map[string]string {
	t.Helper()
	out := make(map[string]string, len(fl))
	for _, f := range fl {
		b, err := f.Render()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Path()] = string(b)
	}
	return out
}

// The cut jennies write the files the cut wrote, one each, and nothing for a
// clean cut.
func TestCutJennies(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	cut, err := authoring.CutTip(rt, "ship", cutFiles("history/0.0.thema", v00, authoring.TipFile, renameTip))
	if err != nil {
		t.Fatal(err)
	}
	const frozenPath = "history/1.0.thema"
	frozen, _ := fileText(cut.Written, frozenPath)
	clean, _ := fileText(cut.Written, authoring.TipFile)
	for _, tc := range []struct {
		name string
		gen  jenny.Jenny[*authoring.Cut]
		want map[string]string
	}{
		{"FrozenVersion", authoring.FrozenVersion, map[string]string{frozenPath: frozen}},
		{"CleanTip", authoring.CleanTip, map[string]string{authoring.TipFile: clean}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fl, gerr := tc.gen.Generate(cut)
			if gerr != nil {
				t.Fatal(gerr)
			}
			if got := rendered(t, fl); !maps.Equal(got, tc.want) {
				t.Errorf("generated %v, want %v", got, tc.want)
			}
		})
	}

	again, err := authoring.CutTip(rt, "ship", cut.Files)
	if err != nil {
		t.Fatal(err)
	}
	fl, err := jenny.Pipeline(authoring.FrozenVersion, authoring.CleanTip).Generate(again)
	if err != nil {
		t.Fatal(err)
	}
	if len(fl) != 0 {
		t.Errorf("a clean cut generated %v", rendered(t, fl))
	}
}

// DecompiledFiles writes what Decompile recovers, relative to the lineage's
// directory.
func TestDecompiledFiles(t *testing.T) {
	ctx := cuecontext.New()
	c, err := authoring.Compile(ctx, "ship", cutFiles("history/0.0.thema", v00, "history/1.0.thema", v10, authoring.TipFile, tip))
	if err != nil {
		t.Fatal(err)
	}
	v := ctx.BuildFile(c.File)
	_, files, err := authoring.Decompile(v)
	if err != nil {
		t.Fatal(err)
	}
	fl, err := authoring.DecompiledFiles.Generate(v)
	if err != nil {
		t.Fatal(err)
	}
	got := rendered(t, fl)
	if len(got) != len(files) {
		t.Errorf("generated %d files, want %d", len(got), len(files))
	}
	for _, f := range files {
		if got[f.Path] != string(f.Text) {
			t.Errorf("%s generated as\n%s\nwant\n%s", f.Path, got[f.Path], f.Text)
		}
	}

	if _, err := authoring.DecompiledFiles.Generate(ctx.CompileString(`schemas: {}`)); err == nil {
		t.Error("a value that is not a lineage decompiled")
	}
}

// InitialTip writes the tip NewTip makes.
func TestInitialTip(t *testing.T) {
	sch := cuecontext.New().CompileString("{name: string}")
	want, err := authoring.NewTip(sch)
	if err != nil {
		t.Fatal(err)
	}
	fl, err := authoring.InitialTip.Generate(sch)
	if err != nil {
		t.Fatal(err)
	}
	got := rendered(t, fl)
	if len(got) != 1 || got[authoring.TipFile] != string(want.Text) {
		t.Errorf("generated %v, want %s holding\n%s", got, authoring.TipFile, want.Text)
	}
}

// Formatted lays out version files and leaves every other file as it is.
func TestFormatted(t *testing.T) {
	const loose = "Schema: {name: string}\n"
	gen := jenny.For[string]().New("files", func(string) (jenny.Files, error) {
		return jenny.Files{
			jenny.NewFile("history/0.0.thema", []byte(loose)),
			jenny.NewFile("README.md", []byte(loose)),
		}, nil
	}).Map(authoring.Formatted)
	fl, err := gen.Generate("ship")
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := authoring.Format([]byte(loose))
	if err != nil {
		t.Fatal(err)
	}
	got := rendered(t, fl)
	if got["history/0.0.thema"] != string(formatted) || got["history/0.0.thema"] == loose {
		t.Errorf("version file generated as\n%s\nwant\n%s", got["history/0.0.thema"], formatted)
	}
	if got["README.md"] != loose {
		t.Errorf("README.md changed to\n%s", got["README.md"])
	}

	bad := jenny.For[string]().New("bad", func(string) (jenny.File, error) {
		return jenny.NewFile("history/0.0.thema", []byte("Schema: {")), nil
	}).Map(authoring.Formatted)
	if _, err := bad.Generate("ship"); err == nil {
		t.Error("a version file that does not parse was formatted")
	}
}

// VersionIndex writes each cut version's ID, as thema computes it, to
// versions.json in the lineage's directory, in this shape.
func TestVersionIndex(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	c, err := authoring.Compile(rt.Context(), "ship", cutFiles("history/0.0.thema", v00, "history/1.0.thema", v10))
	if err != nil {
		t.Fatal(err)
	}
	lin, err := thema.BindLineage(rt.Context().BuildFile(c.File), cue.Path{}, rt)
	if err != nil {
		t.Fatal(err)
	}
	var ids []thema.VersionID
	for sch := lin.First(); sch != nil; sch = sch.Successor() {
		id, ierr := sch.ID()
		if ierr != nil {
			t.Fatal(ierr)
		}
		ids = append(ids, id)
	}
	entry := func(v string, id thema.VersionID) string {
		return fmt.Sprintf("\t\t{\n\t\t\t\"version\": %q,\n\t\t\t\"id\": %q,\n\t\t\t\"schema\": %q,\n\t\t\t\"lens\": %q\n\t\t}", v, id.String(), id.Schema.String(), id.Lens.String())
	}
	want := "{\n\t\"lineage\": \"ship\",\n\t\"versions\": [\n" + entry("0.0", ids[0]) + ",\n" + entry("1.0", ids[1]) + "\n\t]\n}\n"

	fl, err := authoring.VersionIndex.Generate(lin)
	if err != nil {
		t.Fatal(err)
	}
	if got := rendered(t, fl); len(got) != 1 || got[authoring.IndexFile] != want {
		t.Errorf("generated %v, want %s holding\n%s", got, authoring.IndexFile, want)
	}
}

// A provisional version wasn't cut, so VersionIndex leaves it out, and
// writes nothing for a lineage that has no other version.
func TestVersionIndexProvisional(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	for _, tc := range []struct {
		name  string
		files []authoring.File
		want  []string
	}{
		{"tip after history", cutFiles("history/0.0.thema", v00, authoring.TipFile, renameTip), []string{"0.0"}},
		{"tip alone", cutFiles(authoring.TipFile, tip), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := authoring.CompileDevelopment(rt.Context(), "ship", tc.files)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := c.Provisional(); !ok {
				t.Fatal("the tip isn't provisional: the test no longer shows what it means to")
			}
			lin, err := authoring.BindDevelopment(rt, c)
			if err != nil {
				t.Fatal(err)
			}
			fl, err := authoring.VersionIndex.Generate(lin)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, text := range rendered(t, fl) {
				var idx authoring.Index
				if err := json.Unmarshal([]byte(text), &idx); err != nil {
					t.Fatal(err)
				}
				for _, v := range idx.Versions {
					got = append(got, v.Version)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("indexed %v, want %v", got, tc.want)
			}
		})
	}
}
