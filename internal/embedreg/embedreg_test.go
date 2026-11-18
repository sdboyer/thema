package embedreg_test

import (
	"fmt"
	"path/filepath"
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
	v := loadWithThema(t, util.ModuleFile("example.com/m"), "lin: thema.Lineage & {name: \"x\"}\n")
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	if !v.LookupPath(cue.ParsePath("lin.name")).Exists() {
		t.Fatal("expected thema.Lineage to resolve")
	}
}

// requiring is a module file for example.com/m that requires thema's CUE at
// version v.
func requiring(v string) string {
	return fmt.Sprintf("module: %q\nlanguage: version: %q\ndeps: %q: v: %q\n",
		"example.com/m", util.LanguageVersion, util.ThemaModulePath, v)
}

// patch is the embedded version with its patch number moved by d.
func patch(t *testing.T, d int) string {
	t.Helper()
	var maj, minor, p int
	if _, err := fmt.Sscanf(util.ThemaModuleVersion, "v%d.%d.%d", &maj, &minor, &p); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("v%d.%d.%d", maj, minor, p+d)
}

// A module that requires an older version of thema's CUE than the embedded one
// is served the embedded copy: a requirement is a minimum. That it is the
// embedded copy shows in its errors in thema's CUE, which are at Root, the
// embedded version's path, whatever version was required.
func TestOlderVersion(t *testing.T) {
	older := []string{"v0.0.1"}
	if prev := patch(t, -1); !strings.HasSuffix(prev, ".-1") {
		older = append(older, prev)
	}
	for _, req := range older {
		v := loadWithThema(t, requiring(req), "lin: thema.Lineage & {name: 42}\n")
		if !positionAt(v.Validate(), embedreg.Root()+"/lineage.cue") {
			t.Errorf("%s: expected an error position in %s, got: %v", req, embedreg.Root()+"/lineage.cue", cerrors.Details(v.Validate(), nil))
		}
	}
}

func positionAt(err error, file string) bool {
	for _, e := range cerrors.Errors(err) {
		for _, p := range e.InputPositions() {
			if p.Filename() == file {
				return true
			}
		}
	}
	return false
}

// A module that requires a newer version of thema's CUE than the embedded one
// is refused: the embedded copy may lack what it relies on.
func TestVersionSkew(t *testing.T) {
	for _, newer := range []string{patch(t, 1), "v0.999.0"} {
		if err := loadRequiring(newer); err == nil || !strings.Contains(cerrors.Details(err, nil), (&embedreg.VersionSkewError{Required: newer, Embedded: util.ThemaModuleVersion}).Error()) {
			t.Errorf("%s: expected a version skew error, got: %v", newer, cerrors.Details(err, nil))
		}
	}
}

// loadRequiring loads a module that requires thema's CUE at version req, and
// returns the loader's error.
func loadRequiring(req string) error {
	cfg := &load.Config{
		Dir: "/m",
		Env: []string{"CUE_REGISTRY=none"},
		Overlay: map[string]load.Source{
			"/m/cue.mod/module.cue": load.FromString(requiring(req)),
			"/m/in.cue":             load.FromString("package m\n\nimport \"github.com/sdboyer/thema\"\n\nlin: thema.Lineage\n"),
		},
	}
	if err := embedreg.Configure(cfg, thema.CueJointFS); err != nil {
		return err
	}
	// The CUE loader flattens the registry's error into its own, so callers
	// match on the message rather than the type.
	return load.Instances([]string{"."}, cfg)[0].Err
}

func TestErrorPositionsUseRoot(t *testing.T) {
	// name must be a string, per thema.Lineage, so this conflicts in thema's CUE.
	v := loadWithThema(t, util.ModuleFile("example.com/m"), "lin: thema.Lineage & {name: 42}\n")
	err := v.Validate()
	if err == nil {
		t.Fatal("expected a conflict with thema.Lineage")
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

// thema's module is its package's files and its cue.mod, at Root or at
// RuntimeRoot. A module whose path extends thema's is loaded beneath
// RuntimeRoot, and its files are the author's.
func TestKernelPath(t *testing.T) {
	for _, c := range []struct {
		file string
		want bool
	}{
		{filepath.Join(embedreg.Root(), "lineage.cue"), true},
		{filepath.Join(embedreg.RuntimeRoot(), "lineage.cue"), true},
		{filepath.Join(embedreg.Root(), "cue.mod", "module.cue"), true},
		{filepath.Join(embedreg.RuntimeRoot(), "exemplars", "rename.cue"), false},
		{filepath.Join(embedreg.RuntimeRoot(), "fleet", "ship", "ship.cue"), false},
		{filepath.Join(embedreg.RuntimeRoot()+"x", "lineage.cue"), false},
	} {
		if _, got := embedreg.KernelPath(c.file); got != c.want {
			t.Errorf("KernelPath(%q): %v, want %v", c.file, got, c.want)
		}
	}
}
