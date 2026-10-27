package thema

import (
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema/internal/util"
)

// TestKeyedNormalFormCUE pins how thema's CUE evaluates keyed schemas and lenses
// on its own, without BindLineage.
func TestKeyedNormalFormCUE(t *testing.T) {
	ctx := cuecontext.New()
	ordered := cue.MakePath(cue.Hid("_ordered", util.ThemaPackageID))
	counts := cue.MakePath(cue.Hid("_counts", util.ThemaPackageID))
	latest := cue.MakePath(cue.Def("#LatestVersion"))

	t.Run("contiguous versions are counted in order", func(t *testing.T) {
		// Keys are declared out of order; _ordered is built by counting, not from
		// declaration order.
		lin := loadLineageValue(t, ctx, `lin: thema.Lineage & {
	name: "x"
	schemas: {
		"1": "0": schema: {b: int}
		"0": {"1": schema: {a: int, b?: int}, "0": schema: {a: int}}
	}
}`)
		for path, want := range map[*cue.Path]string{&ordered: "[[0,0],[0,1],[1,0]]", &counts: "[2,1]", &latest: "[1,0]"} {
			got, err := lin.LookupPath(*path).MarshalJSON()
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if string(got) != want {
				t.Errorf("%s: got %s, want %s", path, got, want)
			}
		}
	})

	// A skipped version is a missing key: counting reaches a key that isn't
	// there. CUE treats a reference to a missing field as incomplete, not as an
	// error, since unification could still supply the key. So Validate reports
	// nothing, and only using _ordered fails; BindLineage reports the skip itself
	// (checkSchemaVersions).
	t.Run("skipped version is incomplete, not invalid", func(t *testing.T) {
		lin := loadLineageValue(t, ctx, `lin: thema.Lineage & {
	name: "x"
	schemas: "0": {"0": schema: {a: int}, "2": schema: {a: int, b?: int}}
}`)
		if err := lin.Validate(); err != nil {
			t.Errorf("Validate reported the skip: %v", err)
		}
		if err := lin.LookupPath(latest).Err(); err == nil || !strings.Contains(err.Error(), `undefined field: "1"`) {
			t.Errorf("#LatestVersion: want undefined field \"1\", got %v", err)
		}
	})

	// struct.MinFields is only checked once a value is concrete. An empty major
	// passes non-concrete validation, which is all a lineage (whose schemas are
	// abstract) can pass; BindLineage reports it itself (checkGoValidity).
	t.Run("empty major is only caught by concrete validation", func(t *testing.T) {
		lin := loadLineageValue(t, ctx, `lin: thema.Lineage & {
	name: "x"
	schemas: {"0": "0": schema: {a: int}, "1": {}}
}`)
		if err := lin.Validate(); err != nil {
			t.Errorf("non-concrete Validate reported the empty major: %v", err)
		}
		if err := lin.Validate(cue.Concrete(true)); err == nil || !strings.Contains(cerrors.Details(err, nil), "MinFields") {
			t.Errorf("concrete Validate: want MinFields error, got %v", err)
		}
	})

	for name, c := range map[string]struct{ src, err string }{
		"major key": {`schemas: v0: "0": schema: {a: int}`, "v0 is not a major version"},
		"minor key": {`schemas: "0": "01": schema: {a: int}`, "01 is not a minor version"},
		"lens edge": {`schemas: "0": {"0": schema: {a: int}, "1": schema: {a: int, b?: int}}
	lenses: "0.1 -> 0.0": {input: _, result: {a: input.a}}`, "0.1 -> 0.0 is not a lens edge"},
		"schema version field": {`schemas: "0": "0": {version: [0, 0], schema: {a: int}}`, "version is not a field of a schema"},
		"lens from field": {`schemas: "0": {"0": schema: {a: int}, "1": schema: {a: int, b?: int}}
	lenses: "0.1->0.0": {from: [0, 1], v0_1: _, v0_0: {a: v0_1.a}}`, "from is not a field of a lens"},
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			lin := loadLineageValue(t, ctx, "lin: thema.Lineage & {\n\tname: \"x\"\n\t"+c.src+"\n}")
			if err := lin.Validate(); err == nil || !strings.Contains(cerrors.Details(err, nil), c.err) {
				t.Errorf("want error containing %q, got %v", c.err, err)
			}
		})
	}
}

// The Go side names a lens's fields with cuetil.LensParam; thema's CUE names
// them in Lens.takes and Lens.returns. The two must agree.
func TestLensParamMatchesCUE(t *testing.T) {
	rt := NewRuntime(cuecontext.New())
	def := rt.Underlying().LookupPath(cue.MakePath(cue.Str("Lens"))).
		FillPath(cue.MakePath(cue.Str("from")), SV(0, 1)).
		FillPath(cue.MakePath(cue.Str("to")), SV(12, 3))
	for name, want := range map[string]string{"takes": lensParam(SV(0, 1)), "returns": lensParam(SV(12, 3))} {
		got, err := def.LookupPath(cue.MakePath(cue.Str(name))).String()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("Lens.%s = %q, Go names it %q", name, got, want)
		}
	}
}
