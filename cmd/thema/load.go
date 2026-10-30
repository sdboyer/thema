package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/load"
	"github.com/spf13/cobra"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
	terrors "github.com/sdboyer/thema/errors"
	"github.com/sdboyer/thema/internal/util"
	tload "github.com/sdboyer/thema/load"
	"github.com/yalue/merged_fs"
)

// TODO OMG WOULD BE SO AMAZING TO MAKE THIS ALL JUST USE github.com/sdboyer/thema/load

type lineageLoadArgs struct {
	// fs path to the lineage to load. may be absolute or relative, and point to a
	// file or dir. Also may be "-" for stdin
	inputLinFilePath string

	// Absolute path version of inputLinFilePath
	absInput string

	// indicates whether inputLinFilePath points to a directory. Populated in
	// dynLoad
	pathIsDir bool

	// cue path to the lineage to work on (default root)
	lincuepath string

	// String argument of a version
	verstr string

	// tip binds a lineage in the authoring form with its tip, on the
	// development path
	tip bool

	// skip binding the lineage - useful when operating on values that are known to be
	// invalid lineages
	skipBindLineage bool

	// only do all the loading once
	once sync.Once

	// Stores all results of running loaders/validators
	dl *dynamicLoader

	dlerr error
}

type dynamicLoader struct {
	// build.Instance from which the lineage came
	binst *build.Instance
	// loaded lineage
	lin thema.Lineage

	// dev is the lineage bound with its tip, with --tip
	dev *tload.Development

	// loaded schema. latest if no version was provided. it's up to the command
	// to decide if it's acceptable to use latest - this gets loaded either way.
	sch thema.Schema
}

// openLineageWorkspace opens the run's workspace for the lineage at
// lla.inputLinFilePath and the other paths the command reads, such as its
// data. A lineage in a CUE module is loaded with the module, so the
// workspace holds the directory of the nearest cue.mod above the lineage
// too; that one is found on the host by its existence, before the workspace
// is opened, and read through it.
func (lla *lineageLoadArgs) openLineageWorkspace(paths ...string) error {
	all := append([]string{lla.inputLinFilePath}, paths...)
	if lla.inputLinFilePath != "" {
		if abs, err := filepath.Abs(lla.inputLinFilePath); err == nil {
			if mod, ok := hostCueModDir(abs); ok {
				// The workspace holds the directory of each path it is given:
				// cue.mod's is the module's root.
				all = append(all, filepath.Join(mod, "cue.mod"))
			}
		}
	}
	return openWorkspace(all...)
}

// hostCueModDir returns the nearest directory at or above path that holds a
// cue.mod/module.cue, by its existence on the host. Its contents are read
// through the workspace.
func hostCueModDir(path string) (string, bool) {
	for p := path; ; p = filepath.Dir(p) {
		if info, err := os.Stat(filepath.Join(p, "cue.mod", "module.cue")); err == nil && !info.IsDir() {
			return p, true
		}
		if p == filepath.Dir(p) {
			return "", false
		}
	}
}

func (lla *lineageLoadArgs) dynLoad() (*dynamicLoader, error) {
	if lla.inputLinFilePath == "" {
		return nil, diag.New(diag.E0620).Headline("no lineage path was given").Help("pass the path to a .cue file or a directory with -l").Err()
	}
	if err := lla.openLineageWorkspace(); err != nil {
		return nil, err
	}

	dl := &dynamicLoader{}
	var err error
	// subgen writes beside the lineage, at its host path.
	lla.absInput, err = filepath.Abs(lla.inputLinFilePath)
	if err != nil {
		return nil, diag.New(diag.E0601).Headlinef("%s cannot be resolved to a path", lla.inputLinFilePath).Detail(diag.OSReason(err)).Err()
	}
	name, err := ws.name(lla.inputLinFilePath)
	if err != nil {
		return nil, err
	}

	info, err := fs.Stat(ws.fsys, name)
	if err != nil {
		b := diag.New(diag.E0601).Headlinef("%s does not exist", lla.inputLinFilePath)
		if !errors.Is(err, fs.ErrNotExist) {
			b.Headlinef("%s cannot be read", lla.inputLinFilePath).Detail(diag.OSReason(err))
		}
		return nil, b.Help("pass the path to a .cue file or a directory with -l").Err()
	}
	lla.pathIsDir = info.IsDir()

	if lla.pathIsDir && isAuthoringDir(name) {
		// A lineage in the authoring form: compiled to the normal form and
		// bound, with errors positioned in its files.
		if lla.skipBindLineage {
			return nil, diag.InternalErr(fmt.Errorf("%s is a lineage in the authoring form, which a command that skips binding does not take", lla.inputLinFilePath))
		}
		if lla.tip {
			dl.dev, err = tload.DevelopmentLineage(ws.fsys, name, rt)
			if err != nil {
				return nil, err
			}
			warn(dl.dev.Pending)
			dl.lin = dl.dev.Lineage
			return lla.attachSchema(dl)
		}
		dl.lin, err = tload.AuthoringLineage(ws.fsys, name, rt)
		if err != nil {
			return nil, err
		}
		return lla.attachSchema(dl)
	}
	if lla.tip {
		return nil, diag.New(diag.E0620).Headlinef("--tip takes a lineage in the authoring form, and %s is not one", lla.inputLinFilePath).
			Helpf("pass the directory of a lineage with %s and %s/", authoring.TipFile, authoring.HistoryDir).Err()
	}

	// The CUE loader reads the workspace, and nothing else: its positions
	// name files as the workspace does (load.Configure).
	dir := name
	var args []string
	if !lla.pathIsDir {
		dir = path.Dir(name)
		args = append(args, path.Base(name))
	}
	var cfg *load.Config
	modroot, modname, err := findCueMod(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// No module: the lineage's directory is made one, with a module
		// file of its own beside the workspace's files.
		fsys := merged_fs.NewMergedFS(fstest.MapFS{
			path.Join(dir, "cue.mod", "module.cue"): &fstest.MapFile{Data: []byte(util.ModuleFile("thema.cli/input"))},
		}, ws.fsys)
		cfg = &load.Config{FS: fsys, ModuleRoot: fsAbs(dir), Dir: fsAbs(dir), Package: "*"}
	case err != nil:
		return nil, err
	default:
		cfg = &load.Config{FS: ws.fsys, ModuleRoot: fsAbs(modroot), Module: modname, Dir: fsAbs(dir)}
	}
	if err = tload.Configure(cfg); err != nil {
		return nil, err
	}
	binsts := load.Instances(args, cfg)

	if lla.skipBindLineage {
		// just take the first one, ooof
		dl.binst = binsts[0]
		return dl, nil
	}

	dl.lin, dl.binst, err = buildInsts(rt, binsts, func(binst *build.Instance) string {
		if lla.pathIsDir {
			return fmt.Sprintf("%s:%s", lla.inputLinFilePath, binst.PkgName)
		}
		return lla.inputLinFilePath
	}, lla.lincuepath)
	if err != nil {
		return nil, err
	}

	return lla.attachSchema(dl)
}

// isAuthoringDir reports whether dir, a directory in the workspace, holds a
// lineage in the authoring form: a .thema file at its top level, such as its
// tip, or under its history/.
func isAuthoringDir(dir string) bool {
	for _, d := range []string{dir, path.Join(dir, authoring.HistoryDir)} {
		ents, err := fs.ReadDir(ws.fsys, d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), authoring.Ext) {
				return true
			}
		}
	}
	return false
}

// versionLabel names version v of the loaded lineage as the CLI reports it:
// with --tip, the tip's version is the tip, "tip (cuts as 2.1)".
func (dl *dynamicLoader) versionLabel(v thema.SyntacticVersion) string {
	if dl.dev != nil && dl.dev.Provisional(v) {
		return dl.dev.Tip.String()
	}
	return v.String()
}

// versionName names version v of the loaded lineage in machine-readable
// output: with --tip, the tip's version is "tip", and cutsAs the version it
// cuts as; otherwise v, and cutsAs is empty.
func (dl *dynamicLoader) versionName(v thema.SyntacticVersion) (name, cutsAs string) {
	if dl.dev != nil && dl.dev.Provisional(v) {
		return authoring.TipParam, v.String()
	}
	return v.String(), ""
}

// attachSchema attaches the requested schema, or the latest, to the loaded
// lineage. Other validators decide whether what was loaded is acceptable
// (i.e. if the command required explicit input).
func (lla *lineageLoadArgs) attachSchema(dl *dynamicLoader) (*dynamicLoader, error) {
	switch {
	case lla.verstr == "":
		dl.sch = dl.lin.Latest()
	case lla.verstr == authoring.TipParam && dl.dev != nil:
		dl.sch = thema.SchemaP(dl.lin, dl.dev.Tip.Version)
	default:
		synv, err := thema.ParseSyntacticVersion(lla.verstr)
		if err != nil {
			return nil, diag.New(diag.E0620).Headlinef("%q is not a version", lla.verstr).Help("a version is <major>.<minor>, such as 1.0").Err()
		}
		dl.sch, err = dl.lin.Schema(synv)
		if err != nil {
			return nil, err
		}
	}

	return dl, nil
}

func (lla *lineageLoadArgs) dynLoadOnce() error {
	lla.once.Do(func() {
		lla.dl, lla.dlerr = lla.dynLoad()
	})
	return lla.dlerr
}

func (lla *lineageLoadArgs) validateLineageInput(cmd *cobra.Command, args []string) error {
	if err := lla.dynLoadOnce(); err != nil {
		// The package root is not a lineage, and no path into it was given:
		// the lineage is probably a field of the package.
		if lla.lincuepath == "" && errors.Is(err, terrors.ErrValueNotALineage) {
			diag.AddHelp(err, "if the lineage is a field of the package, name it with -p")
		}
		return err
	}
	return nil
}

func (lla *lineageLoadArgs) validateVersionInput(cmd *cobra.Command, args []string) error {
	if err := lla.dynLoadOnce(); err != nil {
		return err
	}
	if lla.verstr == "" {
		return diag.New(diag.E0620).Headline("no schema version was given").Help("name the version with -v, as <major>.<minor>").Err()
	}
	return nil
}

func (lla *lineageLoadArgs) validateVersionInputOptional(cmd *cobra.Command, args []string) error {
	return lla.dynLoadOnce()
}

// findCueMod finds the module dir, a directory in the workspace, belongs
// to: the nearest directory at or above it holding cue.mod/module.cue, read
// through the workspace. It returns that directory and the module's name, or
// an error that is fs.ErrNotExist when there is none.
func findCueMod(dir string) (root, modname string, err error) {
	for p := dir; ; p = path.Dir(p) {
		mpath := path.Join(p, "cue.mod", "module.cue")
		byt, err := fs.ReadFile(ws.fsys, mpath)
		if err == nil {
			modname, err := cuecontext.New().CompileBytes(byt, cue.Filename(mpath)).LookupPath(cue.MakePath(cue.Str("module"))).String()
			if err != nil {
				return "", "", diag.New(diag.E0606).Headlinef("%s does not declare module", displayPath(mpath)).
					Cause(err).
					Help(`a module file declares module: "<path>" and language: version: "<version>"`).
					Err()
			}
			return p, modname, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", "", diag.New(diag.E0606).Headlinef("%s cannot be read", displayPath(mpath)).Detail(diag.OSReason(err)).Err()
		}
		if p == "." {
			return "", "", fs.ErrNotExist
		}
	}
}

// loadDiag describes a failure of CUE's loader. Three are recognized by the
// message the loader gives them: a path with no CUE package, an import of a
// package that cannot be found, and, inside that, a cue.mod requiring another
// version of thema's CUE than the one this program embeds. The rest are
// reported as the loader states them.
func loadDiag(err error) error {
	for _, e := range cerrors.Errors(err) {
		format, _ := e.Msg()
		if format == `cue: %q matched no packages` {
			return diag.New(diag.E0602).Headline("there is no CUE package at the lineage path").
				Help("a lineage is a .cue file, or a directory of .cue files in one package").Err()
		}
		// The loader wraps its package errors ("import failed: ...") and
		// formats their text before it reaches here, so the registry's
		// *embedreg.VersionSkewError survives only as its message.
		var pe *load.PackageError
		if !errors.As(e, &pe) {
			continue
		}
		format, args := pe.Msg()
		text := fmt.Sprintf(format, args...)
		if m := skewText.FindStringSubmatch(text); m != nil {
			return diag.New(diag.E0607).At(pe.Pos).
				Headlinef("the CUE module requires thema %s, but this program embeds %s", m[1], m[2]).
				Helpf("set deps[%q].v to %q in cue.mod/module.cue", util.ThemaModulePath, m[2]).
				Err()
		}
		if rest, ok := strings.CutPrefix(text, `cannot find package "`); ok {
			pkg, _, _ := strings.Cut(rest, `"`)
			return diag.New(diag.E0608).At(pe.Pos).
				Headlinef("the lineage imports %s, which is not available here", pkg).
				Help("only github.com/sdboyer/thema is built in; another module must be declared in the deps of a cue.mod/module.cue above the lineage").
				Err()
		}
	}
	return diag.Convert(err, diag.Context{Op: diag.OpLoad, Generic: diag.E0609})
}

// skewText recognizes the text of an *embedreg.VersionSkewError inside the
// loader's message, and captures the required and embedded versions.
var skewText = regexp.MustCompile(regexp.QuoteMeta(util.ThemaModulePath) + ` version (\S+), but this program embeds version (\S+);`)

type ppathf func(*build.Instance) string

func buildInsts(rt *thema.Runtime, binsts []*build.Instance, ppath ppathf, cuepath string) (thema.Lineage, *build.Instance, error) {
	rets := make([]struct {
		lin   thema.Lineage
		binst *build.Instance
		err   error
	}, len(binsts))
	for i, binst := range binsts {
		rets[i].binst = binst
		rets[i].lin, rets[i].err = loadone(rt, binst, ppath(binst), cuepath)
	}

	switch len(binsts) {
	case 0:
		return nil, nil, diag.New(diag.E0602).Headline("no CUE package was found at the lineage path").
			Help("a lineage is a .cue file, or a directory of .cue files in one package").Err()
	case 1:
		return rets[0].lin, rets[0].binst, rets[0].err
	default:
		// Try all of them. Error if we end up with more than one.
		var lin thema.Lineage
		var binst *build.Instance
		for _, ret := range rets {
			if ret.lin != nil {
				if lin != nil {
					return nil, nil, diag.New(diag.E0603).
						Headlinef("packages %s and %s at the lineage path both hold a lineage", binst.PkgName, ret.binst.PkgName).
						Help("name one package's file with -l, or move the lineages apart").
						Err()
				}
				lin, binst = ret.lin, ret.binst
			}
		}

		if lin == nil {
			// Sloppy, but it's almost always gonna be the first one
			return nil, nil, rets[0].err
		}
		return lin, binst, nil
	}
}

func loadone(rt *thema.Runtime, binst *build.Instance, pkgpath, cuepath string) (thema.Lineage, error) {
	if binst.Err != nil {
		return nil, loadDiag(binst.Err)
	}

	if len(binst.Files) == 0 {
		// A directory with no .cue files loads as an empty package, which
		// would otherwise be reported as a lineage with no name.
		return nil, diag.New(diag.E0602).Headlinef("there is no CUE package at %s", displayPath(strings.TrimPrefix(filepath.ToSlash(binst.Dir), "/"))).
			Help("a lineage is a .cue file, or a directory of .cue files in one package").Err()
	}

	v := rt.Underlying().Context().BuildInstance(binst)

	if cuepath != "" {
		p := cue.ParsePath(cuepath)
		if p.Err() != nil {
			return nil, diag.New(diag.E0604).Headlinef("%q is not a CUE path", cuepath).Detail(p.Err().Error()).
				Help("-p takes a path into the package, such as lin or lineages.ship").Err()
		}
		v = v.LookupPath(p)
		if !v.Exists() {
			return nil, diag.New(diag.E0605).Headlinef("there is no value at %s in %s", cuepath, pkgpath).
				Help("-p names a field of the package that holds the lineage").Err()
		}
	}

	return thema.BindLineage(v, rt)
}
