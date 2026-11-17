package load

import (
	"io/fs"

	"cuelang.org/go/cue"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
)

// A LineageOption configures how [AuthoringLineage] and [DevelopmentLineage]
// load and bind a lineage.
type LineageOption func(*lineageConfig)

type lineageConfig struct {
	// history is where the lineage's history is read from and positioned:
	// HistoryAt, when given.
	history []authoring.Option
	bind    []thema.BindOption
}

// HistoryAt reads the lineage's history from dir/history, in place of the
// lineage's own directory's, and positions its versions there: dir is a
// path of the same fs.FS, as the lineage's directory is. It is for a lineage
// whose history is kept in a tree of its own. See [authoring.HistoryAt].
func HistoryAt(dir string) LineageOption {
	return func(c *lineageConfig) { c.history = []authoring.Option{authoring.HistoryAt(dir)} }
}

// BindOptions passes opts to binding the lineage.
func BindOptions(opts ...thema.BindOption) LineageOption {
	return func(c *lineageConfig) { c.bind = append(c.bind, opts...) }
}

func configure(opts []LineageOption) lineageConfig {
	var c lineageConfig
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// compiling is what the lineage in dir is compiled with: its files
// positioned under dir, and its history where HistoryAt says.
func (c lineageConfig) compiling(dir string) []authoring.Option {
	return append([]authoring.Option{authoring.At(dir)}, c.history...)
}

// AuthoringLineage loads the authoring files of the lineage in dir, a
// directory of fsys, compiles its frozen versions, those under history/ (or
// under the directory [HistoryAt] gives), to the normal form and binds the
// result. The tip is not read. Errors are positioned in the authoring files,
// by their paths in fsys (dir/history/0.0.thema; see [authoring.At]), and
// worded in the authoring form's terms. For the pieces of that sequence, see
// [github.com/sdboyer/thema/authoring].
func AuthoringLineage(fsys fs.FS, dir string, rt *thema.Runtime, opts ...LineageOption) (thema.Lineage, error) {
	cfg := configure(opts)
	name, files, err := authoring.Load(fsys, dir, cfg.history...)
	if err != nil {
		return nil, err
	}
	c, err := authoring.Compile(rt.Context(), name, files, cfg.compiling(dir)...)
	if err != nil {
		return nil, err
	}
	v := rt.Context().BuildFile(c.File)
	if err := v.Err(); err != nil {
		return nil, diag.Convert(err, diag.Context{Op: diag.OpCompile, Vocabulary: authoring.Vocabulary(c), Generic: diag.E0112})
	}
	return thema.BindLineage(v, cue.Path{}, rt, append([]thema.BindOption{thema.WithVocabulary(authoring.Vocabulary(c))}, cfg.bind...)...)
}

// Development is a lineage bound with its tip, on the development path.
type Development struct {
	// Lineage is the history with the tip as its newest version, at
	// Tip.Version, provisional; when the tip is clean, it is the history
	// alone. See [authoring.BindDevelopment].
	Lineage thema.Lineage
	// Tip describes the tip: the version it cuts as, and whether it is clean.
	Tip authoring.Tip
	// Pending is nil when the edge into the tip has every lens it needs.
	// Otherwise it holds an E1001 warning for each lens the tip does not
	// write and derivation cannot produce; translating across one fails with
	// E1002. Each unwraps to thema/errors.ErrProvisional.
	Pending error
}

// Provisional reports whether v is the tip's version in d.Lineage: an
// instance of it is an instance of the tip, which may still change, and of no
// version that was cut.
func (d *Development) Provisional(v thema.SyntacticVersion) bool {
	return !d.Tip.Clean && v == d.Tip.Version
}

// DevelopmentLineage loads the lineage in dir, a directory of fsys, with its
// tip, compiles it and binds it on the development path: the tip is the
// newest version, provisional, at the version it cuts as. It is for
// developing and testing a lineage, never for serving it: what is published
// is what [AuthoringLineage] binds, the versions under history/ (or under
// the directory [HistoryAt] gives). A lineage with no tip.thema is an error
// (E0149). Errors are positioned as [AuthoringLineage] positions them. For
// the pieces, see [authoring.CompileDevelopment] and
// [authoring.BindDevelopment].
func DevelopmentLineage(fsys fs.FS, dir string, rt *thema.Runtime, opts ...LineageOption) (*Development, error) {
	cfg := configure(opts)
	name, files, err := authoring.Load(fsys, dir, cfg.history...)
	if err != nil {
		return nil, err
	}
	c, err := authoring.CompileDevelopment(rt.Context(), name, files, cfg.compiling(dir)...)
	if err != nil {
		return nil, err
	}
	lin, err := authoring.BindDevelopment(rt, c, cfg.bind...)
	if err != nil {
		return nil, err
	}
	return &Development{Lineage: lin, Tip: *c.Tip, Pending: c.Pending}, nil
}
