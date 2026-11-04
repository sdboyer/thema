package gocode

import (
	"bytes"
	"context"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	iofs "io/fs"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/sdboyer/jenny"
	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/encoding/gocode/internal/gentest"
	"github.com/sdboyer/thema/exemplars"
	"github.com/sdboyer/thema/internal/envvars"
	"github.com/sdboyer/thema/internal/txtartest/bindlin"
	"github.com/sdboyer/thema/internal/txtartest/vanilla"
)

// TestGenerateTypes records the files LineageTypes generates for each lineage
// in the shared corpus, one section per file, and checks that each
// type-checks.
func TestGenerateTypes(t *testing.T) {
	test := vanilla.TxTarTest{
		Root:    "../../testdata/lineage",
		Name:    "encoding/gocode/TestGenerateTypes",
		ThemaFS: thema.CueJointFS,
	}

	rt := thema.NewRuntime(cuecontext.New())
	test.Run(t, func(tc *vanilla.Test) {
		if testing.Short() && tc.HasTag("slow") {
			t.Skip("case is tagged #slow, skipping for -short")
		}
		lin, err := bindlin.BindTxtarLineage(tc, rt)
		if err != nil {
			tc.Fatal(err)
		}
		fl, err := LineageTypes(LineageTypeConfig{}).Generate(lin)
		if err != nil {
			tc.Fatal(err)
		}
		for _, f := range fl {
			src, err := f.Render()
			if err != nil {
				tc.Fatal(err)
			}
			typecheck(tc.T, src)
			if _, err := tc.Writer(f.Path()).Write(src); err != nil {
				tc.Fatal(err)
			}
		}
	})
}

// SchemaTypes writes types.go, as a tree of Syntax, in the package and with
// the root name its config gives, defaulting to the lineage's.
func TestSchemaTypes(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	lin, err := exemplars.ExpandLineage(rt)
	if err != nil {
		t.Fatal(err)
	}
	sch, err := lin.Schema(thema.SV(0, 2))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		cfg  TypeConfig
		want []string
	}{
		{"default", TypeConfig{}, []string{"package expand\n", "type Expand struct", "type ExpandWithDefault string"}},
		{"configured", TypeConfig{PackageName: "things", RootName: "Thing"}, []string{"package things\n", "type Thing struct", "type ThingWithDefault string", "ThingWithDefaultFoo ThingWithDefault = \"foo\""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fl, err := SchemaTypes(tc.cfg).Generate(sch)
			if err != nil {
				t.Fatal(err)
			}
			if len(fl) != 1 || fl[0].Path() != TypesFile {
				t.Fatalf("generated %v, want one %s", fl, TypesFile)
			}
			if _, ok := jenny.TreeOf[Syntax](fl[0]); !ok {
				t.Errorf("%s is not a tree of Syntax", TypesFile)
			}
			src, err := fl[0].Render()
			if err != nil {
				t.Fatal(err)
			}
			typecheck(t, src)
			for _, want := range tc.want {
				if !bytes.Contains(src, []byte(want)) {
					t.Errorf("output lacks %q:\n%s", want, src)
				}
			}
		})
	}
}

// LineageTypes places each version in a directory and package of its own,
// with the same root name in each, and a pipeline can place the lineage's
// files further: PackageFromDir has already named each package by then.
func TestLineageTypes(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	lin, err := exemplars.ExpandLineage(rt)
	if err != nil {
		t.Fatal(err)
	}
	gen := LineageTypes(LineageTypeConfig{RootName: "Thing"}).Prefix("gen/expand")
	fl, err := gen.Generate(lin)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fl {
		got = append(got, f.Path())
		src, err := f.Render()
		if err != nil {
			t.Fatal(err)
		}
		pkg := path.Base(path.Dir(f.Path()))
		for _, want := range []string{"package " + pkg + "\n", "type Thing struct"} {
			if !bytes.Contains(src, []byte(want)) {
				t.Errorf("%s lacks %q:\n%s", f.Path(), want, src)
			}
		}
	}
	want := []string{"gen/expand/v0_0/types.go", "gen/expand/v0_1/types.go", "gen/expand/v0_2/types.go", "gen/expand/v0_3/types.go"}
	if !slices.Equal(got, want) {
		t.Errorf("generated %v, want %v", got, want)
	}
}

// PackageFromDir refuses a file it cannot name a package for.
func TestPackageFromDir(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	lin, err := exemplars.ExpandLineage(rt)
	if err != nil {
		t.Fatal(err)
	}
	sch := lin.All()[0]
	for _, tc := range []struct {
		name string
		gen  jenny.Jenny[thema.Schema]
		want string
	}{
		{"no directory", SchemaTypes(TypeConfig{}).Map(PackageFromDir), `types.go: directory "." is not a Go package name`},
		{"not an identifier", SchemaTypes(TypeConfig{}).Prefix("v1.0").Map(PackageFromDir), `v1.0/types.go: directory "v1.0" is not a Go package name`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.gen.Generate(sch); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// gentestJenny generates the files in internal/gentest: each lineage's types
// in a directory named for it.
func gentestJenny() jenny.Jenny[thema.Lineage] {
	return jenny.For[thema.Lineage](jenny.InputName(thema.Lineage.Name)).
		PrefixBy(thema.Lineage.Name, LineageTypes(LineageTypeConfig{}))
}

// TestGentestGoldens checks that the generated files in internal/gentest,
// whose own tests check compilation and assignability, are what the generator
// produces for the exemplars and the shapes lineage. Set THEMA_UPDATE_GOLDEN
// to rewrite them.
func TestGentestGoldens(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	lins := exemplars.All(rt)
	shapes, err := gentest.ShapesLineage(rt)
	if err != nil {
		t.Fatal(err)
	}
	lins["shapes"] = shapes

	names := slices.Sorted(maps.Keys(lins))
	in := make([]thema.Lineage, 0, len(names))
	for _, name := range names {
		in = append(in, lins[name])
	}
	fs, err := gentestJenny().GenerateFS(in...)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("internal", "gentest")
	if envvars.UpdateGoldenFiles {
		if err = fs.Write(context.Background(), dir); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err = fs.Verify(context.Background(), dir); err != nil {
		t.Errorf("%s; set %s=1 to regenerate", err, envvars.VarUpdateGolden)
	}

	// Verify ignores files it does not generate, so a package left from a
	// version or lineage that is gone would still compile here unnoticed.
	generated := make(map[string]bool)
	for _, f := range fs.AsFiles() {
		generated[f.Path()] = true
	}
	err = filepath.WalkDir(dir, func(p string, d iofs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != TypesFile {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if !generated[filepath.ToSlash(rel)] {
			t.Errorf("%s is not generated by anything; remove it", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// typecheck fails t if src is not a Go file that type-checks on its own.
func typecheck(t *testing.T, src []byte) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "gen.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("generated Go does not parse: %s\n%s", err, src)
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	if _, err := conf.Check(f.Name.Name, fset, []*ast.File{f}, nil); err != nil {
		t.Fatalf("generated Go does not type-check: %s\n%s", err, src)
	}
}
