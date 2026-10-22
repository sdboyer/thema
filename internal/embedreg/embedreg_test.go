package embedreg_test

import (
	"fmt"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/load"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/util"
)

// loadWithThema loads a single-file module at /m that imports thema, with the
// given module.cue contents and lineage body.
func loadWithThema(t *testing.T, modfile, body string) cue.Value {
	t.Helper()
	cfg := &load.Config{
		Dir: "/m",
		// Never fall back to a real registry: thema must resolve from the embed.
		Env: []string{"CUE_REGISTRY=none"},
		Overlay: map[string]load.Source{
			"/m/cue.mod/module.cue": load.FromString(modfile),
			"/m/in.cue":             load.FromString("package m\n\nimport \"github.com/sdboyer/thema\"\n\n" + body),
		},
	}
	if err := embedreg.Configure(cfg, thema.CueJointFS); err != nil {
		t.Fatal(err)
	}
	bi := load.Instances([]string{"."}, cfg)[0]
	if bi.Err != nil {
		t.Fatalf("load failed: %v", bi.Err)
	}
	return cuecontext.New().BuildInstance(bi)
}

func TestResolvesWithoutRegistry(t *testing.T) {
	v := loadWithThema(t, util.ModuleFile("example.com/m"), "lin: thema.#Lineage & {name: \"x\"}\n")
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	if !v.LookupPath(cue.ParsePath("lin.name")).Exists() {
		t.Fatal("expected thema.#Lineage to resolve")
	}
}

func TestVersionSkew(t *testing.T) {
	modfile := fmt.Sprintf("module: %q\nlanguage: version: %q\ndeps: %q: v: %q\n",
		"example.com/m", util.LanguageVersion, util.ThemaModulePath, "v0.0.1")
	cfg := &load.Config{
		Dir: "/m",
		Env: []string{"CUE_REGISTRY=none"},
		Overlay: map[string]load.Source{
			"/m/cue.mod/module.cue": load.FromString(modfile),
			"/m/in.cue":             load.FromString("package m\n\nimport \"github.com/sdboyer/thema\"\n\nlin: thema.#Lineage\n"),
		},
	}
	if err := embedreg.Configure(cfg, thema.CueJointFS); err != nil {
		t.Fatal(err)
	}
	err := load.Instances([]string{"."}, cfg)[0].Err
	if err == nil {
		t.Fatal("expected loading to fail on a thema version other than the embedded one")
	}
	// The CUE loader flattens the registry's error into its own, so match on the
	// message rather than the type.
	if !strings.Contains(cerrors.Details(err, nil), (&embedreg.VersionSkewError{Required: "v0.0.1", Embedded: util.ThemaModuleVersion}).Error()) {
		t.Fatalf("expected a version skew error, got: %v", cerrors.Details(err, nil))
	}
}

func TestErrorPositionsUseRoot(t *testing.T) {
	// name must be a string, per thema.#Lineage, so this conflicts in thema's CUE.
	v := loadWithThema(t, util.ModuleFile("example.com/m"), "lin: thema.#Lineage & {name: 42}\n")
	err := v.Validate()
	if err == nil {
		t.Fatal("expected a conflict with thema.#Lineage")
	}
	want := embedreg.Root() + "/lineage.cue"
	for _, e := range cerrors.Errors(err) {
		for _, p := range e.InputPositions() {
			if p.Filename() == want {
				return
			}
		}
	}
	t.Fatalf("expected an error position in %s, got: %v", want, cerrors.Details(err, nil))
}

func TestRejectsFS(t *testing.T) {
	cfg := &load.Config{FS: thema.CueJointFS}
	if err := embedreg.Configure(cfg, thema.CueJointFS); err == nil {
		t.Fatal("expected an error when cfg.FS is set")
	}
}
