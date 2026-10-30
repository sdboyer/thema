package contenthash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/contenthash"
	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/envvars"
	"github.com/sdboyer/thema/internal/util"
)

// versions lists the versions of lin's schemas, in key order: by major, then
// minor.
func versions(t *testing.T, lin cue.Value) []contenthash.Version {
	t.Helper()
	var out []contenthash.Version
	majors, err := lin.LookupPath(cue.ParsePath("schemas")).Fields()
	if err != nil {
		t.Fatal(err)
	}
	for majors.Next() {
		minors, err := majors.Value().Fields()
		if err != nil {
			t.Fatal(err)
		}
		for minors.Next() {
			var v contenthash.Version
			for i, s := range []string{majors.Selector().Unquoted(), minors.Selector().Unquoted()} {
				n, err := strconv.ParseUint(s, 10, 64)
				if err != nil {
					t.Fatalf("schema key %q: %v", s, err)
				}
				v[i] = uint(n)
			}
			out = append(out, v)
		}
	}
	return out
}

// loadModule loads the package at the root of the CUE module example.com/p in
// fsys, serving thema's CUE module from the copy embedded in thema. It is
// what thema's load package does, done here because load imports the
// authoring form's compiler, which imports this package. These tests are
// outside the package because thema, whose embedded CUE module they serve,
// imports it too.
func loadModule(t *testing.T, fsys fstest.MapFS) *build.Instance {
	t.Helper()
	root := filepath.Join(util.Prefix, "example.com", "p")
	overlay := make(map[string]load.Source)
	if err := util.ToOverlay(root, fsys, overlay); err != nil {
		t.Fatal(err)
	}
	cfg := &load.Config{Overlay: overlay, ModuleRoot: root, Module: "example.com/p", Dir: root, Package: "p", DataFiles: true}
	if err := embedreg.Configure(cfg, thema.CueJointFS); err != nil {
		t.Fatal(err)
	}
	inst := load.Instances(nil, cfg)[0]
	if inst.Err != nil {
		t.Fatal(inst.Err)
	}
	return inst
}

// TestGoldens records the canonical forms and IDs of every version of the
// lineages in testdata/*.txtar. Each archive holds a CUE module's files (its
// cue.mod/module.cue is supplied), whose package p declares the lineage as lin;
// each out/<version> section holds that version's ID, schema form and lens
// form.
//
// The digests are a stored contract: an ID computed today must be computed the
// same way for as long as instances recorded against it exist. A diff here,
// including one from a CUE upgrade alone, changes every stored ID it touches;
// see the contenthash seam. Set THEMA_UPDATE_GOLDEN=1 to rewrite the out/
// sections, then review the diff.
func TestGoldens(t *testing.T) {
	files, err := filepath.Glob("testdata/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no golden files")
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".txtar"), func(t *testing.T) {
			ar, err := txtar.ParseFile(file)
			if err != nil {
				t.Fatal(err)
			}
			fs := fstest.MapFS{"cue.mod/module.cue": {Data: []byte(util.ModuleFile("example.com/p"))}}
			for _, f := range ar.Files {
				if !strings.HasPrefix(f.Name, "out/") {
					fs[f.Name] = &fstest.MapFile{Data: f.Data}
				}
			}
			insts := loadModule(t, fs)
			v := cuecontext.New().BuildInstance(insts)
			if verr := v.Err(); verr != nil {
				t.Fatal(verr)
			}
			lin := v.LookupPath(cue.ParsePath("lin"))

			got := map[string][]byte{}
			var order []string
			for _, ver := range versions(t, lin) {
				id, err := contenthash.VersionID(lin, ver)
				if err != nil {
					t.Fatal(err)
				}
				sch, err := contenthash.SchemaForm(lin, ver)
				if err != nil {
					t.Fatal(err)
				}
				name := "out/" + ver.String()
				order = append(order, name)
				lens, err := contenthash.LensForm(lin, ver)
				if err != nil {
					t.Fatal(err)
				}
				got[name] = fmt.Appendf(nil, "# id\n%s\n# schema\n%s\n# lenses\n%s\n", id, sch, lens)
			}

			if envvars.UpdateGoldenFiles {
				var kept []txtar.File
				for _, f := range ar.Files {
					if !strings.HasPrefix(f.Name, "out/") {
						kept = append(kept, f)
					}
				}
				for _, name := range order {
					kept = append(kept, txtar.File{Name: name, Data: got[name]})
				}
				ar.Files = kept
				if err := os.WriteFile(file, txtar.Format(ar), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			for _, f := range ar.Files {
				if !strings.HasPrefix(f.Name, "out/") {
					continue
				}
				want, ok := got[f.Name]
				if !ok {
					t.Errorf("golden section %s has no version", f.Name)
					continue
				}
				if diff := cmp.Diff(string(f.Data), string(want)); diff != "" {
					t.Errorf("%s (-golden +got):\n%s", f.Name, diff)
				}
				delete(got, f.Name)
			}
			for name := range got {
				t.Errorf("missing golden section %s; run with %s=1", name, envvars.VarUpdateGolden)
			}
		})
	}
}
