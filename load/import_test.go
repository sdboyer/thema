package load

import (
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/load"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/util"
)

// harbor is a CUE module that keeps the fleet, and a package of its own that
// imports two of the fleet's lineages by their directories' paths, as native
// CUE does: it needs no thema tooling, only a dependency on thema's CUE
// module.
const harbor = `package harbor

import (
	"example.com/harbor/fleet/boat"
	"example.com/harbor/fleet/ship"
)

flagship: ship

// A version's schema is a CUE type.
manifest: ship.schemas["1"]["0"].schema & {
	firstfield:  "cargo"
	secondfield: 3
}

names: [ship.name, boat.name]
`

// A lineage's normalized.gen.cue makes its directory a CUE package that native
// CUE imports and uses: its schemas are types. The package's value is the
// lineage, which binds; the authoring files beside it, and example.cue, which
// declares no package, are not part of it. It is all offline: thema's CUE
// module is the embedded copy, and the fleet is in the overlay.
func TestImportNormalizedFile(t *testing.T) {
	const module = "example.com/harbor"
	fsys := fstest.MapFS{
		"cue.mod/module.cue": &fstest.MapFile{Data: []byte(util.ModuleFile(module))},
		"harbor.cue":         &fstest.MapFile{Data: []byte(harbor)},
	}
	err := fs.WalkDir(fleet(), "fleet", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fleet(), p)
		fsys[p] = &fstest.MapFile{Data: b}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(util.Prefix, module)
	cfg := &load.Config{
		Overlay:    map[string]load.Source{},
		ModuleRoot: root,
		Dir:        root,
	}
	if err = util.ToOverlay(root, fsys, cfg.Overlay); err != nil {
		t.Fatal(err)
	}
	if err = Configure(cfg); err != nil {
		t.Fatal(err)
	}
	insts := load.Instances([]string{"."}, cfg)
	if err := insts[0].Err; err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}

	ctx := cuecontext.New()
	v := ctx.BuildInstance(insts[0])
	if err = v.Validate(); err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if err = v.LookupPath(cue.ParsePath("manifest")).Validate(cue.Concrete(true)); err != nil {
		t.Errorf("an instance of ship 1.0: %s", cerrors.Details(err, nil))
	}
	names, err := v.LookupPath(cue.ParsePath("names")).List()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{shipName, boatName} {
		names.Next()
		if got, _ := names.Value().String(); got != want {
			t.Errorf("imported %s, want %s", got, want)
		}
	}

	// The package binds, loaded by its import path. (flagship, the same
	// lineage seen through harbor's import of it, does not: its references
	// resolve in the ship package, which the reference rules read as an
	// import, E0143.)
	shipInsts := load.Instances([]string{module + "/fleet/ship"}, cfg)
	if err := shipInsts[0].Err; err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	lin, err := thema.BindLineage(ctx.BuildInstance(shipInsts[0]), thema.NewRuntime(ctx))
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if got := versionsOf(lin); len(got) != 2 || got[0] != v00 || got[1] != "1.0" {
		t.Errorf("the imported ship has versions %v, want 0.0 and 1.0", got)
	}

	if f := v.LookupPath(cue.ParsePath("flagship.category")); f.Exists() {
		t.Errorf("the imported ship has example.cue's category: %v", f)
	}
}
