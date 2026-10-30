package load

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	terrors "github.com/sdboyer/thema/errors"
)

// devDir is shipDir with tip.thema replaced.
func devDir(tip string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for k, v := range shipDir {
		fsys[k] = v
	}
	fsys["lineages/ship/tip.thema"] = &fstest.MapFile{Data: []byte(tip)}
	return fsys
}

func versionsOf(lin thema.Lineage) []string {
	var vs []string
	for _, sch := range lin.All() {
		vs = append(vs, sch.Version().String())
	}
	return vs
}

func codesOf(err error) []string {
	var cs []string
	for _, d := range diag.From(err, diag.Context{}) {
		cs = append(cs, string(d.Code))
	}
	return cs
}

func TestDevelopmentLineageClean(t *testing.T) {
	d, err := DevelopmentLineage(shipDir, "lineages/ship", thema.NewRuntime(cuecontext.New()))
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if got := strings.Join(versionsOf(d.Lineage), ","); got != "0.0,1.0" {
		t.Errorf("versions = %s, want 0.0,1.0", got)
	}
	if !d.Tip.Clean || d.Tip.String() != "tip (clean at 1.0)" || d.Pending != nil {
		t.Errorf("tip = %v, pending %v; want clean at 1.0, none pending", d.Tip, d.Pending)
	}
	if d.Provisional(thema.SV(1, 0)) {
		t.Error("1.0 is provisional, but the tip is clean")
	}
}

func TestDevelopmentLineageMinorTip(t *testing.T) {
	ctx := cuecontext.New()
	rt := thema.NewRuntime(ctx)
	fsys := devDir("Schema: {\n\tfirstfield:  string\n\tsecondfield: int\n\tsize:        *\"medium\" | string\n}\n")

	d, err := DevelopmentLineage(fsys, "lineages/ship", rt)
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if got := strings.Join(versionsOf(d.Lineage), ","); got != "0.0,1.0,1.1" {
		t.Errorf("versions = %s, want 0.0,1.0,1.1", got)
	}
	if d.Tip.String() != "tip (cuts as 1.1)" || !d.Provisional(thema.SV(1, 1)) || d.Provisional(thema.SV(1, 0)) {
		t.Errorf("tip = %v; want 1.1, provisional", d.Tip)
	}
	if d.Pending != nil {
		t.Errorf("pending: %v", d.Pending)
	}
	inst, err := d.Lineage.Latest().Predecessor().Validate(ctx.CompileString(`{firstfield: "x", secondfield: 1}`))
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := inst.Translate(thema.SV(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := out.Underlying().LookupPath(cue.ParsePath("size")).String(); s != "medium" {
		t.Errorf("size = %q, want medium", s)
	}

	// The normal path reads the history only.
	lin, err := AuthoringLineage(fsys, "lineages/ship", rt)
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if got := strings.Join(versionsOf(lin), ","); got != "0.0,1.0" {
		t.Errorf("the normal path binds %s, want 0.0,1.0", got)
	}
}

// A major tip that writes no ForwardLens binds, with the missing lens
// pending; translating across it fails with E1002, and translating within the
// history does not.
func TestDevelopmentLineageMissingLens(t *testing.T) {
	ctx := cuecontext.New()
	fsys := devDir("Schema: {\n\tfirstfield:  string\n\tsecondfield: int\n\tsize:        string\n}\n")
	d, err := DevelopmentLineage(fsys, "lineages/ship", thema.NewRuntime(ctx))
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if d.Tip.String() != "tip (cuts as 2.0)" {
		t.Errorf("tip = %v, want cuts as 2.0", d.Tip)
	}
	if got := strings.Join(codesOf(d.Pending), ","); got != "E1001" {
		t.Errorf("pending = %s, want E1001", got)
	}
	if !errors.Is(d.Pending, terrors.ErrProvisional) {
		t.Error("pending is not ErrProvisional")
	}

	inst, err := d.Lineage.First().Validate(ctx.CompileString(`{firstfield: "x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := inst.Translate(thema.SV(1, 0)); err != nil {
		t.Errorf("translating within the history: %v", err)
	}
	_, _, err = inst.Translate(thema.SV(2, 0))
	if got := strings.Join(codesOf(err), ","); got != "E1002" {
		t.Errorf("translating into the tip: codes %s, want E1002; err: %v", got, err)
	}
	if !errors.Is(err, terrors.ErrProvisional) {
		t.Errorf("translating into the tip: not ErrProvisional: %v", err)
	}

	// The tip's backward lens is derived, so translating out of it works.
	tipInst, err := d.Lineage.Latest().Validate(ctx.CompileString(`{firstfield: "x", secondfield: 1, size: "large"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := tipInst.Translate(thema.SV(1, 0)); err != nil {
		t.Errorf("translating out of the tip: %v", err)
	}
}

// The tip's lenses name its instance tip, and translate both ways.
func TestDevelopmentLineageTipLenses(t *testing.T) {
	ctx := cuecontext.New()
	fsys := devDir(`Schema: {
	firstfield: string
	size:       string
}

ForwardLens: {
	v1_0: _
	tip: {
		size: "medium"
		secondfield: _drop
	}
}

BackwardLens: {
	tip: _
	v1_0: {
		secondfield: len(tip.size)
		size: _drop
	}
}
`)
	d, err := DevelopmentLineage(fsys, "lineages/ship", thema.NewRuntime(ctx))
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if d.Pending != nil {
		t.Fatalf("pending: %v", d.Pending)
	}
	inst, err := d.Lineage.Latest().Validate(ctx.CompileString(`{firstfield: "x", size: "large"}`))
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := inst.Translate(thema.SV(1, 0))
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if n, _ := back.Underlying().LookupPath(cue.ParsePath("secondfield")).Int64(); n != 5 {
		t.Errorf("secondfield = %d, want 5", n)
	}
	fwd, _, err := back.Translate(thema.SV(2, 0))
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if s, _ := fwd.Underlying().LookupPath(cue.ParsePath("size")).String(); s != "medium" {
		t.Errorf("size = %q, want medium", s)
	}
}

func TestDevelopmentLineageLayout(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	t.Run("no tip", func(t *testing.T) {
		fsys := devDir("")
		delete(fsys, "lineages/ship/tip.thema")
		_, err := DevelopmentLineage(fsys, "lineages/ship", rt)
		if got := strings.Join(codesOf(err), ","); got != "E0149" {
			t.Errorf("codes = %s, want E0149", got)
		}
		if _, err := AuthoringLineage(fsys, "lineages/ship", rt); err != nil {
			t.Errorf("the normal path needs no tip: %v", err)
		}
	})
	t.Run("no history", func(t *testing.T) {
		fsys := fstest.MapFS{"lineages/ship/tip.thema": {Data: []byte("Schema: {\n\tname: string\n}\n")}}
		d, err := DevelopmentLineage(fsys, "lineages/ship", rt)
		if err != nil {
			t.Fatal(cerrors.Details(err, nil))
		}
		if got := strings.Join(versionsOf(d.Lineage), ","); got != "0.0" || d.Tip.String() != "tip (cuts as 0.0)" {
			t.Errorf("versions %s, tip %v; want 0.0, cuts as 0.0", got, d.Tip)
		}
		_, err = AuthoringLineage(fsys, "lineages/ship", rt)
		if got := strings.Join(codesOf(err), ","); got != "E0110" {
			t.Errorf("the normal path: codes %s, want E0110", got)
		}
	})
	t.Run("a broken tip", func(t *testing.T) {
		fsys := devDir("Schema: {\n\tname: \n")
		if _, err := DevelopmentLineage(fsys, "lineages/ship", rt); err == nil {
			t.Error("the development path binds a broken tip")
		}
		if _, err := AuthoringLineage(fsys, "lineages/ship", rt); err != nil {
			t.Errorf("the normal path reads the tip: %v", err)
		}
	})
}
