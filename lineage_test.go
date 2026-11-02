package thema

import (
	"bytes"
	"fmt"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/diagtest"
	"github.com/sdboyer/thema/internal/txtartest/vanilla"
)

func TestBindLineage(t *testing.T) {
	test := vanilla.TxTarTest{
		Root:    "./testdata/lineage",
		Name:    "bind",
		ThemaFS: CueJointFS,
		ToDo: map[string]string{
			"lineage/defaultchange": "Thema compat analyzer fails to classify changes to default values as breaking",
			"lineage/optional":      "Optional fields do not satisfy struct.MinFields(), causing Lineage constraints to fail",
		},
	}

	ctx := cuecontext.New()
	rt := NewRuntime(ctx)

	test.Run(t, func(tc *vanilla.Test) {
		lin, err := bindTxtarLineage(tc, rt, "lineagePath")
		if testing.Short() && tc.HasTag("slow") {
			t.Skip("case is tagged #slow, skipping for -short")
		}

		if err != nil {
			tc.Fatalf("error binding lineage: %+v", err)
		}
		slen := len(lin.All())
		fmt.Fprintf(tc, "Schema count: %v\n", slen)
		fmt.Fprintf(tc, "Schema versions: %s\n", lin.allVersions())

		var llen int
		iter, err := lin.Underlying().LookupPath(cue.MakePath(cue.Str("lenses"))).Fields()
		if err != nil {
			tc.Fatal("error getting lenses", err)
		}
		for iter.Next() {
			llen++
		}
		fmt.Fprintf(tc, "Lenses count: %v\n", llen)
	})
}

func TestInvalidLineages(t *testing.T) {
	test := vanilla.TxTarTest{
		Root:    "./testdata/invalidlineage",
		Name:    "bindfail",
		ThemaFS: CueJointFS,
		ToDo: map[string]string{
			"invalidlineage/joindef": "no invariant checker written to disallow definitions from joinSchema",
			"invalidlineage/onlydef": "no invariant checker written to require a schema to declare a regular field",
		},
	}

	ctx := cuecontext.New()
	rt := NewRuntime(ctx)

	test.Run(t, func(tc *vanilla.Test) {
		_, err := bindTxtarLineage(tc, rt, "lineagePath")
		if testing.Short() && tc.HasTag("slow") {
			tc.Skip("case is tagged #slow, skipping for -short")
		}

		if err == nil {
			tc.Fatal("expected error from known-invalid lineage")
		}
		ds := diag.Diagnostics(err)
		if len(ds) == 0 {
			tc.Fatalf("a bind failure is made of diagnostics, got: %v", err)
		}
		diagtest.Lint(tc, ds, diagtest.Normal)
		diag.Render(tc, ds, diag.RenderOptions{Source: archiveSource(tc)})
	})
}

// The append-only corpus: pairs of revisions of a lineage, the published one
// (#firstLin) and a candidate (#secondLin). Those in valid only append, or
// are the same lineage written another way; those in invalid change a
// published schema.
func TestCheckAppendOnly(t *testing.T) {
	test := vanilla.TxTarTest{
		Root:    "./testdata/appendonly/valid",
		Name:    "appendonly",
		ThemaFS: CueJointFS,
	}

	ctx := cuecontext.New()
	rt := NewRuntime(ctx)

	test.Run(t, func(tc *vanilla.Test) {
		if testing.Short() && tc.HasTag("slow") {
			t.Skip("case is tagged #slow, skipping for -short")
		}

		published, err := bindTxtarLineage(tc, rt, "firstLin")
		if err != nil {
			tc.Fatalf("error binding the published lineage: %+v", err)
		}

		candidate, err := bindTxtarLineage(tc, rt, "secondLin")
		if err != nil {
			tc.Fatalf("error binding the candidate lineage: %+v", err)
		}

		if err := CheckAppendOnly(published, candidate); err != nil {
			tc.Fatalf("CheckAppendOnly returned an error: %+v", diag.Diagnostics(err))
		}
	})
}

func TestCheckAppendOnlyFail(t *testing.T) {
	test := vanilla.TxTarTest{
		Root:    "./testdata/appendonly/invalid",
		Name:    "appendonly-fail",
		ThemaFS: CueJointFS,
	}

	ctx := cuecontext.New()
	rt := NewRuntime(ctx)

	test.Run(t, func(tc *vanilla.Test) {
		if testing.Short() && tc.HasTag("slow") {
			t.Skip("case is tagged #slow, skipping for -short")
		}

		published, err := bindTxtarLineage(tc, rt, "firstLin")
		if err != nil {
			tc.Fatalf("error binding the published lineage: %+v", err)
		}

		candidate, err := bindTxtarLineage(tc, rt, "secondLin")
		if err != nil {
			tc.Fatalf("error binding the candidate lineage: %+v", err)
		}

		err = CheckAppendOnly(published, candidate)
		if err == nil {
			tc.Fatalf("expected error from known invalid updates")
		}

		ds := diag.Diagnostics(err)
		if len(ds) == 0 {
			tc.Fatalf("an append-only failure is made of diagnostics, got: %v", err)
		}
		diagtest.Lint(tc, ds, diagtest.Normal)
		diag.Render(tc, ds, diag.RenderOptions{Source: archiveSource(tc)})
	})
}

// archiveSource reads a test archive's files by the names their positions
// carry: the lineage is loaded at the virtual root.
func archiveSource(tc *vanilla.Test) func(file string) ([]byte, bool) {
	return func(file string) ([]byte, bool) {
		for _, f := range tc.Archive.Files {
			if f.Name == file || "/"+f.Name == file {
				return f.Data, true
			}
		}
		return nil, false
	}
}

func bindTxtarLineage(t *vanilla.Test, rt *Runtime, path string) (Lineage, error) {
	if rt == nil {
		rt = NewRuntime(cuecontext.New())
	}
	ctx := rt.Context()

	t.Helper()
	inst := t.Instance()
	val := ctx.BuildInstance(inst)
	if p, ok := t.Value(path); ok {
		pp := cue.ParsePath(p)
		if len(pp.Selectors()) == 0 {
			t.Fatalf("%q is not a valid value for the #%s key", p, path)
		}
		val = val.LookupPath(pp)
		if !val.Exists() {
			t.Fatalf("path %q specified in #%s does not exist in input cue instance", p, path)
		}
	}

	return BindLineage(val, rt)
}

var benchBindstr = `
name: "trivialTwo"
schemas: {
    "0": "0": {
        schema: {
            firstfield: string
        }
    }
    "0": "1": {
        schema: {
            firstfield: string
            secondfield?: int32
        }
    }
}

lenses: {
    "0.1->0.0": {
        v0_1: _
        v0_0: {
            firstfield: v0_1.firstfield
        }
    }
}
`

func BenchmarkUnifyLineage(b *testing.B) {
	bi := getCaseWithImport()
	val := cuecontext.New().BuildInstance(getCaseWithImport())
	if val.Err() != nil {
		b.Fatal(val.Err())
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cuecontext.New().BuildInstance(bi)
	}
}

func getCaseWithImport() *build.Instance {
	themaInst := load.Instances(nil, &load.Config{
		Package: "thema",
	})[0]

	buf := new(bytes.Buffer)
	buf.WriteString(`import "github.com/sdboyer/thema"

thema.Lineage
`)
	buf.WriteString(benchBindstr)

	bi := load.Instances([]string{"-"}, &load.Config{
		Context: themaInst.Context(),
		Stdin:   buf,
	})
	return bi[0]
}

// BenchmarkBindLineage benchmarks binding a lineage in Go, with and without
// explicitly unifying the input lineage with thema.Lineage in the CUE source.
//
// Keeping these separate lets us see the difference between the performance cost
// of just the pure, native CUE logic, vs. the cost of the Go code that wraps it.
func BenchmarkBindLineage(b *testing.B) {
	b.Run("PreUnified", func(b *testing.B) {
		ctx := cuecontext.New()
		rt := NewRuntime(ctx)
		linv := ctx.BuildInstance(getCaseWithImport())

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := BindLineage(linv, rt)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("NotUnified", func(b *testing.B) {
		ctx := cuecontext.New()
		rt := NewRuntime(ctx)
		linv := ctx.CompileString(benchBindstr)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := BindLineage(linv, rt)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
