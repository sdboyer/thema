package thema

import (
	"fmt"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"

	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/util"
	"github.com/sdboyer/thema/lacuna"
)

// TestGoLensValidation checks that Go lenses can't bypass the validation CUE
// lenses get: their results are validated against the target schema, minor-version
// steps validate rather than assume, and CUE lenses aren't silently ignored.
func TestGoLensValidation(t *testing.T) {
	ctx := cuecontext.New()
	rt := NewRuntime(ctx)
	bindSrc := func(t *testing.T, src string, opts ...BindOption) (lin Lineage, err error) {
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
			t.Fatalf("loading case failed: %v", bi.Err)
		}
		defer func() {
			if r := recover(); r != nil {
				lin, err = nil, fmt.Errorf("panic: %v", r)
			}
		}()
		return BindLineage(ctx.BuildInstance(bi).LookupPath(cue.ParsePath("lin")), rt, opts...)
	}
	mustBind := func(t *testing.T, src string, opts ...BindOption) Lineage {
		t.Helper()
		lin, err := bindSrc(t, src, opts...)
		if err != nil {
			t.Fatalf("binding failed: %v", err)
		}
		return lin
	}
	mustValidate := func(t *testing.T, sch Schema, data string) *Instance {
		t.Helper()
		inst, err := sch.Validate(ctx.CompileString(data))
		if err != nil {
			t.Fatalf("validating %s failed: %v", data, err)
		}
		return inst
	}
	verdict := func(t *testing.T, unmet []string) {
		t.Helper()
		if len(unmet) > 0 {
			t.Errorf("%d expectation(s) not met:\n  %s", len(unmet), strings.Join(unmet, "\n  "))
		}
	}

	// translateGo checks only the version number of what a Go lens returns, so a
	// lens may return an instance of another lineage, invalid for the target.
	t.Run("go-lens-foreign-instance", func(t *testing.T) {
		other := mustBind(t, `lin: thema.Lineage & {name: "other", schemas: {
	"0": "0": {schema: {a: int}}
}}`)
		lin := mustBind(t, `lin: thema.Lineage & {
	name: "main"
	schemas: {
		"0": "0": {schema: {a: string}}
		"0": "1": {schema: {a: string, b?: int}}
	}
}`, ImperativeLenses(ImperativeLens{From: SV(0, 1), To: SV(0, 0), Mapper: func(_ *Instance, _ Schema) (*Instance, []lacuna.Lacuna, error) {
			inst, err := other.First().Validate(ctx.CompileString(`{a: 42}`))
			return inst, nil, err
		}}))
		var unmet []string
		out, _, err := mustValidate(t, lin.Latest(), `{a: "x"}`).Translate(SV(0, 0))
		if err == nil {
			unmet = append(unmet, fmt.Sprintf("translate accepted a lens result from lineage %q", out.Schema().Lineage().Name()))
		}
		verdict(t, unmet)
	})

	// When Go lenses are supplied, CUE lenses are not checked at all.
	t.Run("go-lens-ignores-cue-lenses", func(t *testing.T) {
		_, err := bindSrc(t, `lin: thema.Lineage & {
	name: "main"
	schemas: {
		"0": "0": {schema: {a: string}}
		"0": "1": {schema: {a: string, b?: int}}
	}
	lenses: {
		"9.9->3.0": {input: _, result: {nonsense: true}}
		"0.0->0.0": {input: _, result: {}}
	}
}`, ImperativeLenses(ImperativeLens{From: SV(0, 1), To: SV(0, 0), Mapper: func(_ *Instance, to Schema) (*Instance, []lacuna.Lacuna, error) {
			inst, err := to.Validate(ctx.CompileString(`{a: "x"}`))
			return inst, nil, err
		}}))
		var unmet []string
		if err == nil {
			unmet = append(unmet, "bind succeeded despite invalid CUE lenses")
		}
		verdict(t, unmet)
	})

	// Lacunas whose condition holds are never returned from Translate.

	// Malformed versions make BindLineage panic rather than return an error.
}
