// Package embedreg serves thema's CUE module to the CUE loader from the copy
// embedded in the thema Go module.
//
// Thema's Go logic depends on the exact shape of thema's CUE, so the two must
// always be evaluated in lockstep. Serving the CUE module from the Go binary,
// rather than fetching it from a remote registry, guarantees that, and also
// means loading lineages never requires network access for thema itself.
//
// The CUE loader reads dependency modules through its filesystem, so the
// embedded module is placed there: in the loader's overlay at [Root], a
// virtual path naming the module and version, or, for a loader reading a
// caller's fs.FS, beside the caller's files at the same path. Root appears in
// error positions for thema's own CUE either way.
package embedreg

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue/load"
	"cuelang.org/go/mod/modconfig"
	"cuelang.org/go/mod/modfile"
	"cuelang.org/go/mod/module"

	"github.com/sdboyer/thema/internal/util"
)

// VersionSkewError is returned when a CUE module requires a version of thema's
// CUE module other than the one embedded in the running thema Go module.
type VersionSkewError struct {
	Required string
	Embedded string
}

func (e *VersionSkewError) Error() string {
	return fmt.Sprintf("CUE module requires %s version %s, but this program embeds version %s; set deps[%q].v to %q in cue.mod/module.cue",
		util.ThemaModulePath, e.Required, e.Embedded, util.ThemaModulePath, e.Embedded)
}

// Root is the virtual absolute path at which the embedded thema CUE module is
// presented to the CUE loader, e.g. /github.com/sdboyer/thema@v0.1.0. It has the
// same shape as a module's directory in CUE's module cache.
func Root() string {
	base, _, _ := strings.Cut(util.ThemaModulePath, "@")
	return filepath.Join(util.Prefix, filepath.FromSlash(base)+"@"+util.ThemaModuleVersion)
}

// Configure prepares cfg for loading CUE that imports thema, where themaFS is
// the root of thema's CUE module (containing cue.mod/module.cue). It sets
// cfg.Registry to a registry that serves thema's module from themaFS at
// [util.ThemaModuleVersion]. A request for any other version of thema's
// module fails with a [*VersionSkewError].
//
// The CUE loader reads a dependency's files through its own filesystem, so
// themaFS is also placed there, in one of the loader's two modes:
//
//   - When cfg.FS is nil, the loader reads the host's filesystem, and themaFS
//     is added to cfg.Overlay at [Root].
//   - When cfg.FS is set, the loader reads only cfg.FS. It is replaced with an
//     FS that also serves themaFS at Root's path, and cfg.FromFSPath is set to
//     name every file the loader reads: a file of thema's at its path under
//     Root, as the overlay names it, so that positions in thema's CUE are
//     recognized the same in both modes ([KernelPath]), and every other file
//     by its path within the caller's cfg.FS, relative to its root, with no
//     leading slash (ship/lineage.cue). A tool then reads an excerpt from the
//     same FS by the name a position gives. cfg.FromFSPath must be unset.
//
// Requests for all other modules go to the registry already set in cfg, if any,
// or else to a registry configured from cfg.Env, as [load.Config] would do by
// default. That registry is only constructed if such a request is made.
func Configure(cfg *load.Config, themaFS fs.FS) error {
	if cfg.FS != nil && cfg.FromFSPath != nil {
		return errors.New("cannot load thema into a load.Config that sets FromFSPath; Configure names the files")
	}

	b, err := fs.ReadFile(themaFS, "cue.mod/module.cue")
	if err != nil {
		return fmt.Errorf("reading thema module file: %w", err)
	}
	modf, err := modfile.Parse(b, "cue.mod/module.cue")
	if err != nil {
		return fmt.Errorf("parsing thema module file: %w", err)
	}
	if modf.Module != util.ThemaModulePath {
		return fmt.Errorf("thema module file declares module %q, expected %q", modf.Module, util.ThemaModulePath)
	}

	osRoot := Root()
	if cfg.FS != nil {
		cfg.FS = mountFS{base: cfg.FS, thema: themaFS}
		cfg.FromFSPath = fromFSPath
		osRoot = "/" + fsRoot()
	} else {
		if cfg.Overlay == nil {
			cfg.Overlay = make(map[string]load.Source)
		}
		if err := util.ToOverlay(Root(), themaFS, cfg.Overlay); err != nil {
			return err
		}
	}

	next := cfg.Registry
	if next == nil {
		env := cfg.Env
		next = &modconfig.LazyRegistry{New: func() (modconfig.CachedRegistry, error) {
			return modconfig.NewRegistry(&modconfig.Config{Env: env})
		}}
	}
	cfg.Registry = &registry{
		loc:  module.SourceLoc{FS: rootFS{FS: themaFS, root: osRoot}, Dir: "."},
		modf: modf,
		next: next,
	}
	return nil
}

// fsRoot is [Root] as a path within a load.Config's FS: slash-separated, with
// no leading slash.
func fsRoot() string {
	base, _, _ := strings.Cut(util.ThemaModulePath, "@")
	return base + "@" + util.ThemaModuleVersion
}

// mountFS is a caller's FS for the CUE loader with thema's module beside its
// files, at [fsRoot].
type mountFS struct {
	base, thema fs.FS
}

func (m mountFS) Open(name string) (fs.File, error) {
	if rel, ok := underRoot(name); ok {
		return m.thema.Open(rel)
	}
	return m.base.Open(name)
}

// underRoot returns name relative to [fsRoot], if it is beneath it.
func underRoot(name string) (string, bool) {
	if name == fsRoot() {
		return ".", true
	}
	return strings.CutPrefix(name, fsRoot()+"/")
}

// fromFSPath names a file the CUE loader read from a mountFS (see
// [Configure]).
func fromFSPath(p string) string {
	name := strings.TrimPrefix(p, "/")
	if rel, ok := underRoot(name); ok {
		return filepath.Join(Root(), filepath.FromSlash(rel))
	}
	return name
}

// IsKernelFile reports whether filename is a file of thema's own CUE module, as
// opposed to a file an author wrote or data being validated. Error positions in
// thema's CUE are never the author's line; callers choosing a position to show
// treat them as supporting notes.
func IsKernelFile(filename string) bool {
	_, ok := KernelPath(filename)
	return ok
}

// KernelPath returns filename relative to the root of thema's CUE module, if
// filename is a file of that module, at [Root] or at [RuntimeRoot]:
// lineage.cue for /github.com/sdboyer/thema@v0.1.0/lineage.cue.
func KernelPath(filename string) (string, bool) {
	sep := string(filepath.Separator)
	for _, root := range []string{Root(), RuntimeRoot()} {
		if rel, ok := strings.CutPrefix(filename, root+sep); ok {
			return filepath.ToSlash(rel), true
		}
	}
	return "", false
}

// rootFS reports where the CUE loader reads thema's module from, through its
// own filesystem: [Root] in the overlay, or [fsRoot] in a mountFS.
type rootFS struct {
	fs.FS
	root string
}

func (r rootFS) OSRoot() string { return r.root }

type registry struct {
	loc  module.SourceLoc
	modf *modfile.File
	next modconfig.Registry
}

func (r *registry) isThema(m module.Version) (bool, error) {
	if m.Path() != util.ThemaModulePath {
		return false, nil
	}
	if m.Version() != util.ThemaModuleVersion {
		return true, &VersionSkewError{Required: m.Version(), Embedded: util.ThemaModuleVersion}
	}
	return true, nil
}

func (r *registry) ModFile(ctx context.Context, m module.Version) (*modfile.File, error) {
	if ok, err := r.isThema(m); ok {
		return r.modf, err
	}
	return r.next.ModFile(ctx, m)
}

func (r *registry) Fetch(ctx context.Context, m module.Version) (module.SourceLoc, error) {
	if ok, err := r.isThema(m); ok {
		return r.loc, err
	}
	return r.next.Fetch(ctx, m)
}

func (r *registry) ModuleVersions(ctx context.Context, mpath string) ([]string, error) {
	if mpath == util.ThemaModulePath {
		return []string{util.ThemaModuleVersion}, nil
	}
	return r.next.ModuleVersions(ctx, mpath)
}
