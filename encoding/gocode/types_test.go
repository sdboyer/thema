package gocode

import (
	"bytes"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/encoding/gocode/internal/gentest"
	"github.com/sdboyer/thema/exemplars"
	"github.com/sdboyer/thema/internal/envvars"
	"github.com/sdboyer/thema/internal/txtartest/bindlin"
	"github.com/sdboyer/thema/internal/txtartest/vanilla"
)

// TestGenerateTypes records the types generated for every schema of each
// lineage in the shared corpus, and checks that they type-check.
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
		src, err := GenerateLineageTypes(lin, nil)
		if err != nil {
			tc.Fatal(err)
		}
		typecheck(tc.T, src)
		tc.Write(src) //nolint:errcheck
	})
}

// TestGenerateTypesSingle checks the configuration GenerateTypes takes.
func TestGenerateTypesSingle(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	lin, err := exemplars.ExpandLineage(rt)
	if err != nil {
		t.Fatal(err)
	}
	sch, err := lin.Schema(thema.SV(0, 2))
	if err != nil {
		t.Fatal(err)
	}

	src, err := GenerateTypes(sch, nil)
	if err != nil {
		t.Fatal(err)
	}
	typecheck(t, src)
	for _, want := range []string{"package expand\n", "type ExpandV0_2 struct", "type ExpandV0_2WithDefault string"} {
		if !bytes.Contains(src, []byte(want)) {
			t.Errorf("default config: output lacks %q:\n%s", want, src)
		}
	}
	if bytes.Contains(src, []byte("ExpandV0_3")) {
		t.Errorf("GenerateTypes emitted a type for another version:\n%s", src)
	}

	src, err = GenerateTypes(sch, &TypeConfig{PackageName: "things", RootName: "Thing"})
	if err != nil {
		t.Fatal(err)
	}
	typecheck(t, src)
	for _, want := range []string{"package things\n", "type Thing struct", "type ThingWithDefault string", "ThingWithDefaultFoo ThingWithDefault = \"foo\""} {
		if !bytes.Contains(src, []byte(want)) {
			t.Errorf("configured: output lacks %q:\n%s", want, src)
		}
	}
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

	names := make([]string, 0, len(lins))
	for name := range lins {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			src, err := GenerateLineageTypes(lins[name], &TypeConfig{PackageName: "gentest"})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("internal", "gentest", name+"_gen.go")
			if envvars.UpdateGoldenFiles {
				if err := os.WriteFile(path, src, 0644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s; set %s=1 to generate it", err, envvars.VarUpdateGolden)
			}
			if !bytes.Equal(want, src) {
				t.Errorf("%s is stale; set %s=1 to regenerate it. Generated:\n%s", path, envvars.VarUpdateGolden, src)
			}
		})
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
