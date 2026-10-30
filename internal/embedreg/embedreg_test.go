package embedreg_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

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
// inFile is the file TestFS loads, by its path within the FS.
const inFile = "m/in.cue"

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

func TestVersionSkew(t *testing.T) {
	modfile := fmt.Sprintf("module: %q\nlanguage: version: %q\ndeps: %q: v: %q\n",
		"example.com/m", util.LanguageVersion, util.ThemaModulePath, "v0.0.1")
	cfg := &load.Config{
		Dir: "/m",
		Env: []string{"CUE_REGISTRY=none"},
		Overlay: map[string]load.Source{
			"/m/cue.mod/module.cue": load.FromString(modfile),
			"/m/in.cue":             load.FromString("package m\n\nimport \"github.com/sdboyer/thema\"\n\nlin: thema.Lineage\n"),
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

func TestRejectsFromFSPath(t *testing.T) {
	cfg := &load.Config{FS: fstest.MapFS{}, FromFSPath: func(p string) string { return p }}
	if err := embedreg.Configure(cfg, thema.CueJointFS); err == nil {
		t.Fatal("expected an error when cfg.FromFSPath is set")
	}
}

// With cfg.FS, the loader reads only that FS: thema's module is served beside
// it, a file of the caller's is named by its path within the FS, relative to
// its root, and thema's own files are named as they are in the overlay.
func TestFS(t *testing.T) {
	fsys := fstest.MapFS{
		"m/cue.mod/module.cue": {Data: []byte(util.ModuleFile("example.com/m"))},
		inFile:                 {Data: []byte("package m\n\nimport \"github.com/sdboyer/thema\"\n\nlin: thema.Lineage & {name: 42}\n")},
	}
	cfg := &load.Config{FS: fsys, Dir: "/m", ModuleRoot: "/m", Env: []string{"CUE_REGISTRY=none"}}
	if err := embedreg.Configure(cfg, thema.CueJointFS); err != nil {
		t.Fatal(err)
	}
	bi := load.Instances([]string{"."}, cfg)[0]
	if bi.Err != nil {
		t.Fatal(cerrors.Details(bi.Err, nil))
	}
	if got := bi.Files[0].Filename; got != inFile {
		t.Errorf("the caller's file is named %q, want m/in.cue", got)
	}
	err := cuecontext.New().BuildInstance(bi).Validate()
	if err == nil {
		t.Fatal("expected a conflict with thema.Lineage")
	}
	var files []string
	for _, e := range cerrors.Errors(err) {
		for _, p := range e.InputPositions() {
			files = append(files, p.Filename())
		}
	}
	if !slices.Contains(files, embedreg.Root()+"/lineage.cue") || !slices.Contains(files, inFile) {
		t.Errorf("positions name %v, want %s/lineage.cue and m/in.cue", files, embedreg.Root())
	}
	for _, f := range files {
		if _, kernel := embedreg.KernelPath(f); !kernel && f != inFile {
			t.Errorf("position in %s, which is neither thema's nor the caller's file", f)
		}
	}
}
