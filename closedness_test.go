package thema

import (
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"

	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/util"
)

// bindString loads src as a single-file lineage module importing thema, and binds
// the lineage at path lin.
func bindString(t *testing.T, rt *Runtime, src string) (Lineage, error) {
	t.Helper()
	cfg := &load.Config{
		Dir: "/m",
		Overlay: map[string]load.Source{
			"/m/cue.mod/module.cue": load.FromString(util.ModuleFile("example.com/m")),
			"/m/in.cue":             load.FromString("package m\n\nimport \"github.com/sdboyer/thema\"\n\n" + src),
		},
	}
	if err := embedreg.Configure(cfg, CueJointFS); err != nil {
		t.Fatal(err)
	}
	bi := load.Instances([]string{"."}, cfg)[0]
	if bi.Err != nil {
		t.Fatal(bi.Err)
	}
	return BindLineage(rt.Context().BuildInstance(bi).LookupPath(cue.ParsePath("lin")), rt)
}

func TestLensResultsMayNotInventFields(t *testing.T) {
	ctx := cuecontext.New()
	rt := NewRuntime(ctx)

	for name, result := range map[string]string{
		"valid":  `{b: input.a, n: {x: 1}}`,
		"top":    `{b: input.a, n: {x: 1}, invented: 1}`,
		"nested": `{b: input.a, n: {x: 1, invented: 1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			lin, err := bindString(t, rt, `lin: thema.Lineage & {
	name: "invent"
	schemas: [{
		version: [0, 0]
		schema: a: string
	}, {
		version: [1, 0]
		// Written open, deliberately: lens results must still match it exactly.
		schema: {b: string, n: {x: int}, ...}
	}]
	lenses: [{
		to: [0, 0]
		from: [1, 0]
		input: _
		result: a: input.b
	}, {
		to: [1, 0]
		from: [0, 0]
		input: _
		result: `+result+`
	}]
}`)
			if err != nil {
				t.Fatalf("binding failed: %v", err)
			}
			inst, err := lin.First().Validate(ctx.CompileString(`{a: "foo"}`))
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = inst.Translate(SV(1, 0))
			if name == "valid" && err != nil {
				t.Fatalf("valid lens result rejected: %v", err)
			}
			if name != "valid" && err == nil {
				t.Fatal("lens result with an invented field was accepted")
			}
		})
	}
}

func TestValidateHonorsAuthorOpenness(t *testing.T) {
	ctx := cuecontext.New()
	rt := NewRuntime(ctx)

	lin, err := bindString(t, rt, `lin: thema.Lineage & {
	name: "openness"
	schemas: [{
		version: [0, 0]
		schema: {
			closedish: {a: int}
			open: {a: int, ...}
		}
	}]
}`)
	if err != nil {
		t.Fatal(err)
	}
	sch := lin.First()

	if _, err := sch.Validate(ctx.CompileString(`{closedish: {a: 1}, open: {a: 1, extra: true}}`)); err != nil {
		t.Errorf("extra field in a struct the author wrote as open was rejected: %v", err)
	}
	if _, err := sch.Validate(ctx.CompileString(`{closedish: {a: 1, extra: true}, open: {a: 1}}`)); err == nil {
		t.Error("extra field in a struct without ... was accepted by default validation")
	}
	if _, err := sch.Validate(ctx.CompileString(`{closedish: {a: 1}, open: {a: 1}, extra: true}`)); err == nil {
		t.Error("extra top-level field was accepted by default validation")
	}
}

// thema's CUE must evaluate cleanly in the abstract, before any lineage is
// unified with it: only incompleteness, never conflicts.
func TestAbstractLineageEvaluates(t *testing.T) {
	rt := NewRuntime(cuecontext.New())
	for _, name := range []string{"Lineage", "SchemaDef", "Lens", "Lacuna"} {
		v := rt.Underlying().LookupPath(cue.ParsePath(name))
		if !v.Exists() {
			t.Fatalf("%s does not exist in thema's CUE", name)
		}
		if err := v.Validate(); err != nil {
			t.Errorf("abstract %s does not evaluate cleanly: %v", name, err)
		}
	}
}
