// Package embedreg serves thema's CUE module to the CUE loader from the copy
// embedded in the thema Go module.
//
// Thema's Go logic depends on the exact shape of thema's CUE, so the two must
// always be evaluated in lockstep. Serving the CUE module from the Go binary,
// rather than fetching it from a remote registry, guarantees that, and also
// means loading lineages never requires network access for thema itself.
//
// The CUE loader reads dependency modules from absolute paths through its
// filesystem, so the embedded module is placed in the loader's overlay at
// [Root], a virtual path naming the module and version. That path appears in
// error positions for thema's own CUE.
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
// the root of thema's CUE module (containing cue.mod/module.cue). It adds the
// files of themaFS to cfg.Overlay at [Root], and sets cfg.Registry to a registry
// that serves thema's module from there at [util.ThemaModuleVersion]. A request
// for any other version of thema's module fails with a [*VersionSkewError].
//
// Requests for all other modules go to the registry already set in cfg, if any,
// or else to a registry configured from cfg.Env, as [load.Config] would do by
// default. That registry is only constructed if such a request is made.
//
// Configure cannot be used with a cfg that sets FS, as the CUE loader does not
// permit FS to be combined with Overlay.
func Configure(cfg *load.Config, themaFS fs.FS) error {
	if cfg.FS != nil {
		return errors.New("cannot load thema into a load.Config that sets FS; use Overlay instead")
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

	if cfg.Overlay == nil {
		cfg.Overlay = make(map[string]load.Source)
	}
	if err := util.ToOverlay(Root(), themaFS, cfg.Overlay); err != nil {
		return err
	}

	next := cfg.Registry
	if next == nil {
		env := cfg.Env
		next = &modconfig.LazyRegistry{New: func() (modconfig.CachedRegistry, error) {
			return modconfig.NewRegistry(&modconfig.Config{Env: env})
		}}
	}
	cfg.Registry = &registry{
		loc:  module.SourceLoc{FS: rootFS{FS: themaFS}, Dir: "."},
		modf: modf,
		next: next,
	}
	return nil
}

// rootFS reports [Root] as its OS root, which is where the CUE loader then
// reads the module's files from, via the overlay.
type rootFS struct {
	fs.FS
}

func (rootFS) OSRoot() string { return Root() }

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
