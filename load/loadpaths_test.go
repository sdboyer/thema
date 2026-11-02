package load

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
	"github.com/google/go-cmp/cmp"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/contenthash"
	"github.com/sdboyer/thema/internal/diagtest"
	"github.com/sdboyer/thema/internal/lensir"
	"github.com/sdboyer/thema/internal/util"
)

// What a lineage reports must not depend on how it reached BindLineage (#217).
// A reference the lineage makes, a lens reading v0_0.name or a schema using
// strings.MaxRunes, is part of what it is; the package it is declared in, the
// import or re-export it was reached through, and the files and filesystem it
// was loaded from are not. TestLoadPaths loads each lineage in
// testdata/loadpaths along every path in loadPaths and requires the same
// result from each:
//
//   - the same bind verdict, and the same diagnostics: code, headline, path,
//     address, notes and help;
//   - the same ID for every version (internal/contenthash, which Schema.ID
//     will return once it exists);
//   - the same program for every lens (thema.LensPrograms);
//   - for the paths that load the same normal-form text, the same positions
//     for every diagnostic and note, up to the file's name, and the same
//     origin (the lineage's file, or thema's own CUE).
//
// A note that says only where CUE's error is ("here", "also here") is a
// position: it is compared with the positions, not the meaning. One CUE
// reports in a file only some paths load, the line of package ship that
// embeds thema.Lineage, is not compared at all; see #217.
//
// Each archive holds a lineage in the authoring form, as ship/<version>.thema
// files, or in the normal form, as normal.cue; the authoring form is compiled,
// and the normal form's text is what the other paths load. Any other file is
// another package of the lineage's module (common/common.cue), for a lineage
// that imports one; only the paths that load a module can load it. out/verdict
// records the result every path gave; regenerate it with THEMA_UPDATE_GOLDEN=1.
//
// A path is added as one more row in loadPaths. Two are pending #212: the
// normal form generated beside the authoring files (normalized.gen.cue) loaded
// as a package of a collection by the collection loader, and a module read
// through load.Configure's FS mode (load.Config.FS, thema's module mounted
// inside the caller's fs.FS), which #212 adds.
func TestLoadPaths(t *testing.T) {
	for _, a := range diagtest.Archives(t, "testdata/loadpaths") {
		t.Run(a.Name(), func(t *testing.T) {
			fx := readLPFixture(t, a)
			var first, firstText *lpResult
			for _, p := range loadPaths {
				if !p.loads(fx) {
					continue
				}
				res := p.run(t, fx)
				if first == nil {
					first = res
				} else if diff := cmp.Diff(first.meaning, res.meaning); diff != "" {
					t.Errorf("%s reports otherwise than %s (-%s +%s):\n%s", p.name, first.path, first.path, p.name, diff)
				}
				if !p.text {
					continue
				}
				if firstText == nil {
					firstText = res
				} else if diff := cmp.Diff(firstText.positions, res.positions); diff != "" {
					t.Errorf("%s positions its diagnostics otherwise than %s (-%s +%s):\n%s", p.name, firstText.path, firstText.path, p.name, diff)
				}
			}
			if first == nil {
				t.Fatal("no loading path can load the archive")
			}
			a.Compare(t, map[string]string{"out/verdict": first.verdict()})
		})
	}
}

// The module and package the lineage is declared in, as an author's module
// would hold them, and the file that holds it.
const (
	lpModule  = "example.com/fleet"
	lpPackage = "ship"
	lpFile    = lpPackage + "/" + lpPackage + ".cue"
	// fleetFile is the file of package fleet, which reaches the lineage.
	fleetFile = "fleet/fleet.cue"
)

// lpFixture is one archive's lineage, in the forms a loading path starts from.
type lpFixture struct {
	// compiled is the authoring form compiled to the normal form, if the
	// archive holds the authoring form.
	compiled *authoring.Compiled
	// text is the normal form's text, as the file of package ship that holds
	// it: what an author's module holds when the lineage is written, or
	// generated, in the normal form.
	text string
	// module holds the module's other files, by path from its root.
	module map[string]string
}

func readLPFixture(t *testing.T, a *diagtest.Archive) lpFixture {
	t.Helper()
	fx := lpFixture{module: make(map[string]string)}
	var files []authoring.File
	for _, f := range a.Inputs() {
		switch name, ok := strings.CutPrefix(f.Name, lpPackage+"/"); {
		case ok && strings.HasSuffix(name, authoring.Ext):
			files = append(files, authoring.File{Path: name, Text: f.Data})
		case f.Name == "normal.cue":
			fx.text = "package " + lpPackage + "\n\n" + string(f.Data)
		default:
			fx.module[f.Name] = string(f.Data)
		}
	}
	if len(files) > 0 {
		c, err := authoring.Compile(cuecontext.New(), lpPackage, files)
		if err != nil {
			t.Fatalf("compiling the authoring form: %v", err)
		}
		text, err := c.Text()
		if err != nil {
			t.Fatal(err)
		}
		fx.compiled, fx.text = c, "package "+lpPackage+"\n\n"+text
	}
	if fx.text == "" {
		t.Fatal("the archive holds no lineage: neither ship/*.thema nor normal.cue")
	}
	return fx
}

// A loadPath is one way a lineage reaches BindLineage.
type loadPath struct {
	name string
	// compiled says the path binds the compiled authoring form.
	compiled bool
	// text says the path loads the normal form's text, so that its positions
	// are comparable with every other such path's.
	text bool
	// module says the path loads the lineage from a module, which can hold
	// the other packages a lineage imports.
	module bool
	// load returns the lineage value to bind.
	load func(t *testing.T, ctx *cue.Context, fx lpFixture) cue.Value
}

// fleet is the file of package fleet that reaches the lineage in package ship
// through decl, importing what imports lists besides ship.
func fleet(decl string, imports ...string) string {
	imports = append(imports, lpModule+"/"+lpPackage)
	var b strings.Builder
	b.WriteString("package fleet\n\nimport (\n")
	for _, imp := range imports {
		fmt.Fprintf(&b, "\t%q\n", imp)
	}
	b.WriteString(")\n\n" + decl + "\n")
	return b.String()
}

// embedLineage is a file of package ship that embeds thema.Lineage in the
// package, as the normal form thema generates beside the authoring files does.
const embedLineage = "package ship\n\nimport \"github.com/sdboyer/thema\"\n\nthema.Lineage\n"

var loadPaths = []loadPath{
	{
		// The authoring form, compiled, as load.AuthoringLineage binds it.
		name: "compiled", compiled: true,
		load: func(_ *testing.T, ctx *cue.Context, fx lpFixture) cue.Value {
			return ctx.BuildFile(fx.compiled.File)
		},
	},
	{
		// The normal form's text compiled on its own, with no loader.
		name: "file", text: true,
		load: func(_ *testing.T, ctx *cue.Context, fx lpFixture) cue.Value {
			return ctx.CompileString(fx.text, cue.Filename(lpFile))
		},
	},
	{
		// Package ship, loaded through a load.Config overlay.
		name: "overlay", text: true, module: true,
		load: func(t *testing.T, ctx *cue.Context, fx lpFixture) cue.Value {
			return overlayLoad(t, ctx, fx, nil, lpPackage, "")
		},
	},
	{
		// Package ship, loaded from an fstest.MapFS by InstanceWithThema.
		name: "mapfs", text: true, module: true,
		load: func(t *testing.T, ctx *cue.Context, fx lpFixture) cue.Value {
			fsys := fstest.MapFS{
				"cue.mod/module.cue": {Data: []byte(util.ModuleFile(lpModule))},
				lpFile:               {Data: []byte(fx.text)},
			}
			for name, text := range fx.module {
				fsys[name] = &fstest.MapFile{Data: []byte(text)}
			}
			inst, err := InstanceWithThema(fsys, lpPackage)
			if err != nil {
				t.Fatal(err)
			}
			return ctx.BuildInstance(inst)
		},
	},
	{
		// Package ship of a module whose path extends thema's, loaded from an
		// fstest.MapFS by InstanceWithThema. The loader puts the module's
		// files beneath thema's module path, where thema's own CUE is too. A
		// lineage importing another package of its module would name it by
		// this module's path, so the path loads only lineages that import
		// none.
		name: "mapfs-under-thema", text: true,
		load: func(t *testing.T, ctx *cue.Context, fx lpFixture) cue.Value {
			fsys := fstest.MapFS{
				"cue.mod/module.cue": {Data: []byte(util.ModuleFile("github.com/sdboyer/thema/fleet"))},
				lpFile:               {Data: []byte(fx.text)},
			}
			inst, err := InstanceWithThema(fsys, lpPackage)
			if err != nil {
				t.Fatal(err)
			}
			return ctx.BuildInstance(inst)
		},
	},
	{
		// Package ship, with thema.Lineage embedded in it by another of its
		// files.
		name: "embedded", text: true, module: true,
		load: func(t *testing.T, ctx *cue.Context, fx lpFixture) cue.Value {
			return overlayLoad(t, ctx, fx, map[string]string{lpPackage + "/lineage.cue": embedLineage}, lpPackage, "")
		},
	},
	{
		// Imported into package fleet, which is the lineage: it embeds the
		// import.
		name: "imported", text: true, module: true,
		load: func(t *testing.T, ctx *cue.Context, fx lpFixture) cue.Value {
			return overlayLoad(t, ctx, fx, map[string]string{fleetFile: fleet(lpPackage)}, "fleet", "")
		},
	},
	{
		// Re-exported by package fleet under another name. The evaluator
		// shares package ship's value, and the lineage's references resolve
		// in package ship.
		name: "reexported", text: true, module: true,
		load: func(t *testing.T, ctx *cue.Context, fx lpFixture) cue.Value {
			return overlayLoad(t, ctx, fx, map[string]string{fleetFile: fleet("flagship: ship")}, "fleet", "flagship")
		},
	},
	{
		// Re-exported through a hidden field of package fleet.
		name: "reexported-chain", text: true, module: true,
		load: func(t *testing.T, ctx *cue.Context, fx lpFixture) cue.Value {
			return overlayLoad(t, ctx, fx, map[string]string{fleetFile: fleet("_ship:    ship\nflagship: _ship")}, "fleet", "flagship")
		},
	},
	{
		// Re-exported by package fleet, unified with thema.Lineage there. The
		// lineage is evaluated afresh in package fleet, and its references
		// resolve there.
		name: "reexported-unified", text: true, module: true,
		load: func(t *testing.T, ctx *cue.Context, fx lpFixture) cue.Value {
			return overlayLoad(t, ctx, fx, map[string]string{fleetFile: fleet("flagship: thema.Lineage & ship", "github.com/sdboyer/thema")}, "fleet", "flagship")
		},
	},
	{
		// Re-exported by package fleet, from a package ship that embeds
		// thema.Lineage.
		name: "reexported-embedded", text: true, module: true,
		load: func(t *testing.T, ctx *cue.Context, fx lpFixture) cue.Value {
			return overlayLoad(t, ctx, fx, map[string]string{
				lpPackage + "/lineage.cue": embedLineage,
				fleetFile:                  fleet("flagship: ship"),
			}, "fleet", "flagship")
		},
	},
}

// loads reports whether p can load fx.
func (p loadPath) loads(fx lpFixture) bool {
	switch {
	case p.compiled:
		return fx.compiled != nil
	case len(fx.module) > 0:
		return p.module
	}
	return true
}

// overlayLoad loads the package in dir from the fixture's module, with extra
// files, through a load.Config overlay, and returns the value at path in it.
func overlayLoad(t *testing.T, ctx *cue.Context, fx lpFixture, extra map[string]string, dir, path string) cue.Value {
	t.Helper()
	root := filepath.Join(util.Prefix, "loadpaths")
	overlay := map[string]load.Source{
		filepath.Join(root, "cue.mod", "module.cue"):    load.FromString(util.ModuleFile(lpModule)),
		filepath.Join(root, filepath.FromSlash(lpFile)): load.FromString(fx.text),
	}
	for _, files := range []map[string]string{fx.module, extra} {
		for name, text := range files {
			overlay[filepath.Join(root, filepath.FromSlash(name))] = load.FromString(text)
		}
	}
	cfg := &load.Config{Overlay: overlay, ModuleRoot: root, Module: lpModule, Dir: filepath.Join(root, dir)}
	if err := Configure(cfg); err != nil {
		t.Fatal(err)
	}
	inst := load.Instances(nil, cfg)[0]
	if inst.Err != nil {
		t.Fatal(inst.Err)
	}
	return ctx.BuildInstance(inst).LookupPath(cue.ParsePath(path))
}

// lpResult is what one path reported, split into what every path must agree
// on and the positions only paths loading the same text can agree on.
type lpResult struct {
	path      string
	meaning   lpMeaning
	positions []lpPositions
}

type lpMeaning struct {
	Diagnostics []lpDiagnostic
	IDs         []string
	Programs    []string
}

// lpDiagnostic is a diagnostic without its positions.
type lpDiagnostic struct {
	Code     diag.Code
	Headline string
	Path     []string
	Address  *diag.Address
	Notes    []string
	Help     []string
	Detail   string
}

// lpPositions are one diagnostic's positions, without the file's name: its
// primary position, then each note's.
type lpPositions []string

// lpPos renders a position without its file's name, but with where it is: in
// the lineage's file, in thema's own CUE, or elsewhere, in a file only some
// paths load (package fleet's, or the file embedding thema.Lineage).
func lpPos(p *diag.Pos) string {
	switch {
	case p == nil:
		return "-"
	case p.Origin != diag.OriginUser:
		return fmt.Sprintf("%d:%d (%s)", p.Line, p.Col, p.Origin)
	case filepath.Base(p.File) == filepath.Base(lpFile):
		return fmt.Sprintf("%d:%d (lineage)", p.Line, p.Col)
	}
	return fmt.Sprintf("%d:%d (elsewhere)", p.Line, p.Col)
}

// positional are the labels of notes that say only where CUE's error is, and
// so are positions, not meaning.
var positional = map[string]bool{"here": true, "also here": true, "in thema's own CUE": true}

func (p loadPath) run(t *testing.T, fx lpFixture) *lpResult {
	t.Helper()
	ctx := cuecontext.New()
	v := p.load(t, ctx, fx)
	var opts []thema.BindOption
	if fx.compiled != nil {
		// Every path binds with the authoring form's vocabulary when there is
		// one, so that their headlines can be compared with the compiled
		// path's.
		opts = append(opts, thema.WithVocabulary(authoring.Vocabulary(fx.compiled)))
	}
	lin, err := thema.BindLineage(v, thema.NewRuntime(ctx), opts...)
	res := &lpResult{path: p.name}
	ds := diag.Diagnostics(err)
	if err != nil && len(ds) == 0 {
		t.Fatalf("%s: bind failed without diagnostics: %v", p.name, err)
	}
	for _, d := range ds {
		md := lpDiagnostic{Code: d.Code, Headline: d.Headline, Path: d.Path, Address: d.Address, Help: d.Help, Detail: d.Detail}
		pos := lpPositions{lpPos(d.Primary)}
		for _, n := range d.Notes {
			if !positional[n.Label] {
				md.Notes = append(md.Notes, n.Label)
			}
			at := lpPos(n.Pos)
			if positional[n.Label] && strings.HasSuffix(at, "(elsewhere)") {
				// A position CUE reports in a file only this path loads: where
				// the author embedded thema.Lineage, say. Deferred in #217.
				continue
			}
			pos = append(pos, n.Label+" "+at)
		}
		res.meaning.Diagnostics = append(res.meaning.Diagnostics, md)
		res.positions = append(res.positions, pos)
	}

	for _, ver := range lpVersions(t, v) {
		id, err := contenthash.VersionID(v, contenthash.Version(ver))
		if err != nil {
			t.Fatalf("%s: the ID of %s: %v", p.name, ver, err)
		}
		res.meaning.IDs = append(res.meaning.IDs, fmt.Sprintf("%s %s", ver, id))
	}

	if lin != nil {
		progs, err := thema.LensPrograms(lin)
		if err != nil {
			res.meaning.Programs = append(res.meaning.Programs, "no programs: "+err.Error())
		}
		for _, prog := range progs.Sorted() {
			res.meaning.Programs = append(res.meaning.Programs, fmt.Sprintf("lens %s\n%s", prog.Edge, lensir.Print(prog)))
		}
	}
	return res
}

// lpVersions returns the versions the lineage declares, in order, read from
// the value whether or not it binds.
func lpVersions(t *testing.T, lin cue.Value) []thema.SyntacticVersion {
	t.Helper()
	var out []thema.SyntacticVersion
	majors, err := lin.LookupPath(cue.MakePath(cue.Str("schemas"))).Fields()
	if err != nil {
		t.Fatal(err)
	}
	for majors.Next() {
		minors, err := majors.Value().Fields()
		if err != nil {
			t.Fatal(err)
		}
		for minors.Next() {
			var v thema.SyntacticVersion
			if _, err := fmt.Sscanf(majors.Selector().Unquoted()+"."+minors.Selector().Unquoted(), "%d.%d", &v[0], &v[1]); err != nil {
				t.Fatal(err)
			}
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b thema.SyntacticVersion) int {
		if a[0] != b[0] {
			return int(a[0]) - int(b[0])
		}
		return int(a[1]) - int(b[1])
	})
	return out
}

// verdict renders what every path reported, for out/verdict.
func (r *lpResult) verdict() string {
	var b strings.Builder
	b.WriteString("# bind\n")
	if len(r.meaning.Diagnostics) == 0 {
		b.WriteString("ok\n")
	}
	for _, d := range r.meaning.Diagnostics {
		fmt.Fprintf(&b, "%s %s\n", d.Code, d.Headline)
	}
	b.WriteString("# ids\n")
	for _, id := range r.meaning.IDs {
		b.WriteString(id + "\n")
	}
	if len(r.meaning.Programs) > 0 {
		b.WriteString("# programs\n")
		for _, p := range r.meaning.Programs {
			b.WriteString(strings.TrimSuffix(p, "\n") + "\n")
		}
	}
	return b.String()
}
