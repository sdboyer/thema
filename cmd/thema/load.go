package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

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
	// if a real cue.mod existed on disk, this is non-nil
	cm *parentCueMod
	// relative path from the cue mod to the dir containing the loaded lineage, if there was a cue.mod
	relToLinDir string
	// filename of the lineage, if a single specific filename was provided
	linFilename string

	// build.Instance from which the lineage came
	binst *build.Instance
	// loaded lineage
	lin thema.Lineage

	// loaded schema. latest if no version was provided. it's up to the command
	// to decide if it's acceptable to use latest - this gets loaded either way.
	sch thema.Schema
}

// lineageFromPaths takes a filepath and an optional CUE path expression
// and loads the result up and bind it to a Lineage.
type parentCueMod struct {
	// the absolute path to the dir containing cue.mod directory
	cuemodparentdir string
	// name of module according to cue.mod/module.cue file
	modname string
}

func (lla *lineageLoadArgs) dynLoad() (*dynamicLoader, error) {
	if lla.inputLinFilePath == "" {
		return nil, diag.New(diag.E0620).Headline("no lineage path was given").Help("pass the path to a .cue file or a directory with -l").Err()
	}

	dl := &dynamicLoader{}
	// scenarios
	// - lineage load path has no cue.mod parent (dynamically create cueFS in load dir)
	// - lineage load path has a cue.mod in some parent (dynamically make a cueFS that includes it; record rel)
	// - cwd is in the same cue.mod as target lineage
	// - cwd is different
	//
	// loadpath is absolute vs. relative (CUE load.Instances() seems to choke on absolute paths)

	var err error
	lla.absInput, err = filepath.Abs(lla.inputLinFilePath)
	if err != nil {
		return nil, diag.New(diag.E0601).Headlinef("%s cannot be resolved to a path", lla.inputLinFilePath).Detail(diag.OSReason(err)).Err()
	}

	var binsts []*build.Instance
	var cfg *load.Config

	// GOAL:
	// cfg.ModuleRoot is the ABSOLUTE path up to the module root, real or virtual
	// cfg.Module is the name of the module, real or virtual
	// cfg.Dir is the RELATIVE path, from module root to what we actually want to load, less filename

	var info os.FileInfo
	info, err = os.Stat(lla.absInput)
	if err != nil {
		b := diag.New(diag.E0601).Headlinef("%s does not exist", lla.inputLinFilePath)
		if !errors.Is(err, os.ErrNotExist) {
			b.Headlinef("%s cannot be read", lla.inputLinFilePath).Detail(diag.OSReason(err))
		}
		return nil, b.Help("pass the path to a .cue file or a directory with -l").Err()
	}
	lla.pathIsDir = info.IsDir()

	if lla.pathIsDir && isAuthoringDir(lla.absInput) {
		// A lineage in the authoring form: compiled to the normal form and
		// bound, with errors positioned in its files.
		if lla.skipBindLineage {
			return nil, diag.InternalErr(fmt.Errorf("%s is a lineage in the authoring form, which a command that skips binding does not take", lla.inputLinFilePath))
		}
		dl.lin, err = tload.AuthoringLineage(os.DirFS(filepath.Dir(lla.absInput)), filepath.Base(lla.absInput), rt)
		if err != nil {
			return nil, err
		}
		return lla.attachSchema(dl)
	}

	dl.cm, err = findCueMod(lla.absInput)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		dyndir := lla.absInput
		var args []string
		if !lla.pathIsDir {
			args = append(args, filepath.Base(dyndir))
			dyndir = filepath.Dir(dyndir)
		}

		overlay := map[string]load.Source{
			filepath.Join(dyndir, "cue.mod", "module.cue"): load.FromString(util.ModuleFile("thema.cli/input")),
		}
		cfg = &load.Config{
			ModuleRoot: dyndir,
			Overlay:    overlay,
			Dir:        dyndir,
			Package:    "*",
		}
		if err := tload.Configure(cfg); err != nil {
			return nil, err
		}

		binsts = load.Instances(args, cfg)
	} else {
		dl.relToLinDir, err = filepath.Rel(dl.cm.cuemodparentdir, lla.absInput)
		if err != nil {
			return nil, diag.InternalErr(fmt.Errorf("cue.mod parent %s is not relative to the lineage path %s", dl.cm.cuemodparentdir, lla.inputLinFilePath))
		}
		var args []string
		if !lla.pathIsDir {
			dl.linFilename = filepath.Base(dl.relToLinDir)
			dl.relToLinDir = filepath.Dir(dl.relToLinDir)
			args = append(args, dl.linFilename)
		}

		cfg = &load.Config{
			ModuleRoot: dl.cm.cuemodparentdir,
			Module:     dl.cm.modname,
			Dir:        filepath.Join(dl.cm.cuemodparentdir, dl.relToLinDir),
			// Package:    "*",
		}
		if err := tload.Configure(cfg); err != nil {
			return nil, err
		}
		binsts = load.Instances(args, cfg)
	}

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

// isAuthoringDir reports whether dir holds a lineage in the authoring form:
// a version file at its top level.
func isAuthoringDir(dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), authoring.Ext) {
			return true
		}
	}
	return false
}

// attachSchema attaches the requested schema, or the latest, to the loaded
// lineage. Other validators decide whether what was loaded is acceptable
// (i.e. if the command required explicit input).
func (lla *lineageLoadArgs) attachSchema(dl *dynamicLoader) (*dynamicLoader, error) {
	if lla.verstr == "" {
		dl.sch = dl.lin.Latest()
	} else {
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

// findCueMod recursively searches the given path and its parent directories
// until one is found containing a cue.mod/module.cue. An error is returned on
// file read errors, or if no cue.mod was found.
func findCueMod(path string) (*parentCueMod, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, diag.New(diag.E0601).Headlinef("%s cannot be read", path).Detail(diag.OSReason(err)).Err()
	}
	if !stat.IsDir() {
		path = filepath.Dir(path)
	}
	p := path

	for ; p != filepath.Dir(p); p = filepath.Dir(p) {
		mpath := filepath.Join(p, "cue.mod", "module.cue")
		byt, err := os.ReadFile(mpath)
		if err == nil {
			modname, err := cuecontext.New().CompileBytes(byt, cue.Filename(mpath)).LookupPath(cue.MakePath(cue.Str("module"))).String()
			if err != nil {
				return nil, diag.New(diag.E0606).Headlinef("%s does not declare module", displayPath(mpath)).
					Cause(err).
					Help(`a module file declares module: "<path>" and language: version: "<version>"`).
					Err()
			}
			return &parentCueMod{
				cuemodparentdir: p,
				modname:         modname,
			}, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, diag.New(diag.E0606).Headlinef("%s cannot be read", displayPath(mpath)).Detail(diag.OSReason(err)).Err()
		}
	}
	return nil, os.ErrNotExist
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
		return nil, diag.New(diag.E0602).Headlinef("there is no CUE package at %s", displayPath(binst.Dir)).
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

	var opts []thema.BindOption
	if _, set := os.LookupEnv("THEMA_SKIP_BUGGY"); set {
		opts = append(opts, thema.SkipBuggyChecks())
	}
	return thema.BindLineage(v, rt, opts...)
}
