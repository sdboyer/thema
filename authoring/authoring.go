// Package authoring compiles thema's authoring form, the .thema files a
// lineage author writes, to the normal form that every thema tool consumes,
// and decompiles the normal form back. The form is described in
// docs/authoring-form.md.
//
// A lineage's directory holds its tip, tip.thema, the version being worked
// on, and its frozen versions under history/. [Compile] reads the history
// alone: what binds is what was cut. The development path, [CompileDevelopment]
// and [BindDevelopment] (or [BindDevelopmentValue], for the lineage as part of
// a larger value), adds the tip as a provisional newest version, at the
// version it would be cut as, and tolerates the lenses its edge does not have
// yet. [ReadTip] derives that version, [CutTip] cuts the tip to history/ at
// it, and [CheckClean] checks that the tip was cut.
//
// The common cases, loading a lineage's directory and binding it, are one
// call each: [github.com/sdboyer/thema/load.AuthoringLineage] and
// [github.com/sdboyer/thema/load.DevelopmentLineage]. This package is for
// tools that need the pieces: the files as loaded, the compiled normal form as
// an AST positioned in the authoring files (to build, inspect or position
// errors of their own), the versions in order, the decompiler, or the
// formatter.
package authoring

import (
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compile"
	"github.com/sdboyer/thema/internal/provisional"
)

// Ext is the extension of a version file.
const Ext = compile.Ext

// The layout of a lineage's directory.
const (
	// TipFile is the tip, at the top of the lineage's directory: the version
	// being worked on. Its version is never written, but derived; see [Tip].
	TipFile = compile.TipFile
	// HistoryDir holds the frozen versions, each <major>.<minor>.thema.
	HistoryDir = compile.HistoryDir
	// TipParam is the name a lens on the edge into the tip gives the tip's
	// instance, as in ForwardLens: {v2_0: _, tip: {...}}. It is reserved: no
	// other lens may use it.
	TipParam = compile.TipParam
)

// A File is one authoring file: its path relative to the lineage's directory
// (tip.thema, history/0.0.thema) and its text.
type File = compile.File

// Tip is a lineage's tip as it stands against the lineage's history: the
// version it cuts as, and whether it is clean.
type Tip = compile.Tip

// Compiled is a lineage in normal form, compiled from authoring files: its
// name, the normal form as an AST whose every node is positioned in the
// authoring file it came from, and its versions in order. Build the AST with
// [cue.Context.BuildFile] and bind it with [github.com/sdboyer/thema.BindLineage].
type Compiled = compile.Compiled

// Load reads the authoring files of the lineage in dir, a directory of fsys:
// every .thema file at the top of dir and under dir/history. The lineage's
// name is dir's base name.
func Load(fsys fs.FS, dir string) (name string, files []File, err error) {
	return compile.Load(fsys, dir)
}

// Compile compiles the frozen versions of the lineage name, those under
// history/, to its normal form. The tip is not read, and need not exist.
// Errors are positioned in the authoring files, and worded in the authoring
// form's terms.
func Compile(ctx *cue.Context, name string, files []File) (*Compiled, error) {
	return compile.Compile(ctx, name, files)
}

// CompileDevelopment compiles the lineage name with its tip, for the
// development path: the tip is the newest version, at the version it cuts as,
// unless it is clean, when the result is the history's. Compiled.Tip
// describes the tip. Each lens on the edge into the tip that tip.thema does
// not write and derivation cannot produce is left out of the normal form and
// reported in Compiled.Pending, as an E1001 warning; a lens the tip writes is
// compiled like any other. A lineage with no tip.thema is an error (E0149).
// Bind the result with [BindDevelopment].
func CompileDevelopment(ctx *cue.Context, name string, files []File) (*Compiled, error) {
	return compile.Development(ctx, name, files)
}

// BindDevelopment binds a lineage compiled by [CompileDevelopment], with the
// authoring form's vocabulary. Its newest version is the tip's, when the tip
// is not clean, and is provisional: bind tolerates the lenses missing from
// its edge (Compiled.Pending), and translating across one of them returns an
// E1002 error, which unwraps to thema/errors.ErrProvisional. Everything else
// is checked as [thema.BindLineage] checks it. A lineage compiled by
// [Compile] has no provisional version, and binds as BindLineage binds it.
//
// An instance of the tip's version is an instance of the tip, not of any
// version that was cut: until it is, the tip may change under it.
func BindDevelopment(rt *thema.Runtime, c *Compiled, opts ...thema.BindOption) (thema.Lineage, error) {
	return BindDevelopmentValue(rt, c, rt.Context().BuildFile(c.File), opts...)
}

// BindDevelopmentValue binds v, the lineage c compiled to as it appears in a
// larger value, on the development path, as [BindDevelopment] binds c alone. v
// is c.File built and unified with more: a joinSchema, or other constraints
// of a tool that composes lineages into one value.
//
// The provisional version's tolerance is c's to grant, so v must be c's
// lineage: v is named as c is, declares exactly c's versions, and declares
// every lens c compiled. Anything else is an error (E0626), and nothing is
// bound. The lenses bind tolerates missing are then those c reports in
// Compiled.Pending, on the edge into c's provisional version, which is v's
// newest.
func BindDevelopmentValue(rt *thema.Runtime, c *Compiled, v cue.Value, opts ...thema.BindOption) (thema.Lineage, error) {
	if err := v.Err(); err != nil {
		return nil, diag.Convert(err, diag.Context{Op: diag.OpCompile, Vocabulary: Vocabulary(c), Generic: diag.E0112})
	}
	if err := compiledFrom(rt.Context(), c, v); err != nil {
		return nil, err
	}
	all := append([]thema.BindOption{thema.WithVocabulary(Vocabulary(c))}, opts...)
	if p, ok := c.Provisional(); ok {
		all = append(all, provisional.Option(p).(thema.BindOption))
	}
	return thema.BindLineage(v, rt, all...)
}

// compiledFrom checks that v is the lineage c compiled to, as unifying c's
// normal form with more leaves it: the same name, the same versions, and
// every lens c declares. Anything a tool unifies with c may narrow it, but
// cannot rename it, add or remove a version, or remove a lens.
func compiledFrom(ctx *cue.Context, c *Compiled, v cue.Value) error {
	cv := ctx.BuildFile(c.File)
	const help = "bind the lineage that Compiled.File builds, as the larger value holds it"
	fail := func(format string, args ...any) error {
		return diag.New(diag.E0626).Headlinef(format, args...).
			Notef(token.NoPos, "the development path tolerates the lenses missing from the tip's edge because %s was compiled with them missing, so it binds only the lineage %s was compiled to", c.Name, c.Name).
			Help(help).
			Err()
	}

	namePath := cue.MakePath(cue.Str("name"))
	if name, err := v.LookupPath(namePath).String(); err != nil || name != c.Name {
		return fail("the lineage to bind on the development path is not named %s, as the one compiled was", c.Name)
	}

	// The versions a lineage declares, by the keys of its schemas, in order.
	// A key that is not a version is bind's to report.
	versions := func(lin cue.Value) []string {
		var vs []thema.SyntacticVersion
		var bad []string
		majors, _ := lin.LookupPath(cue.MakePath(cue.Str("schemas"))).Fields()
		for majors != nil && majors.Next() {
			minors, _ := majors.Value().Fields()
			for minors != nil && minors.Next() {
				key := majors.Selector().Unquoted() + "." + minors.Selector().Unquoted()
				if sv, err := thema.ParseSyntacticVersion(key); err == nil {
					vs = append(vs, sv)
				} else {
					bad = append(bad, key)
				}
			}
		}
		slices.SortFunc(vs, func(a, b thema.SyntacticVersion) int {
			if a.Less(b) {
				return -1
			}
			if b.Less(a) {
				return 1
			}
			return 0
		})
		out := make([]string, 0, len(vs)+len(bad))
		for _, sv := range vs {
			out = append(out, sv.String())
		}
		return append(out, bad...)
	}
	if got, want := versions(v), versions(cv); !slices.Equal(got, want) {
		return fail("the lineage to bind on the development path has versions %s, where %s was compiled with %s", strings.Join(got, ", "), c.Name, strings.Join(want, ", "))
	}

	lensesPath := cue.MakePath(cue.Str("lenses"))
	compiled := cv.LookupPath(lensesPath)
	if !compiled.Exists() {
		return nil
	}
	lenses, err := compiled.Fields()
	if err != nil {
		return diag.InternalErr(fmt.Errorf("the lenses %s compiled to cannot be read: %w", c.Name, err))
	}
	for lenses.Next() {
		edge := lenses.Selector()
		if !v.LookupPath(lensesPath.Append(edge)).Exists() {
			return fail("the lineage to bind on the development path has no lens %s, which %s was compiled with", edge.Unquoted(), c.Name)
		}
	}
	return nil
}

// ReadTip reads the tip of the lineage in files against its history: the
// version it cuts as, derived by the compatibility check against the newest
// version under history/, and whether it is clean. It reads the tip and the
// newest frozen version, not the lineage as a whole; compile it for that.
func ReadTip(ctx *cue.Context, files []File) (*Tip, error) {
	return compile.ReadTip(ctx, files)
}

// CheckClean checks that the tip of the lineage in files is clean: its schema
// equals that of the newest version under history/, and it declares no
// lenses. It returns nil for a clean tip, and otherwise an E1003 diagnostic
// for each way the tip differs. CI runs it to assert that the tip was cut.
func CheckClean(ctx *cue.Context, files []File) error {
	return compile.CheckClean(ctx, files)
}

// Vocabulary names a compiled lineage's parts in the authoring form's terms:
// a lens is the ForwardLens or BackwardLens in its version file, a schema is
// the version file that declares it. Bind the compiled lineage with
// [github.com/sdboyer/thema.WithVocabulary] so that bind's findings read in
// those terms too.
func Vocabulary(c *Compiled) diag.Vocabulary {
	return compile.Vocabulary(c)
}

// Decompile recovers the authoring files of a lineage in normal form: the
// lineage value, as compiled or as loaded, which need not be bound. It is the
// inverse of Compile on the lineages Compile produces; a lens outside that
// form is an error naming its edge, never an approximation. Every version
// is decompiled under history/, and the tip is clean: the newest version's
// schema, with no lenses.
func Decompile(lin cue.Value) (name string, files []File, err error) {
	return compile.Decompile(lin)
}

// NewTip returns the tip of a new lineage whose one version is schema:
// tip.thema holding schema, in the layout [Format] gives it. A lineage of
// that tip alone has an empty history, and its tip cuts as 0.0.
func NewTip(schema cue.Value) (File, error) {
	return compile.NewTip(schema)
}

// Format lays out the text of a version file in the authoring form's one
// layout: every block keeps its braces and every field has its own line.
// Decompiling a formatted file gives back its text exactly.
func Format(src []byte) ([]byte, error) {
	return compile.Format(src)
}
