// Package authoring compiles thema's authoring form, the .thema files a
// lineage author writes, to the normal form that every thema tool consumes,
// and decompiles the normal form back. The form is described in
// docs/authoring-form.md.
//
// A lineage's directory holds its tip, tip.thema, the version being worked
// on, and its frozen versions under history/. [Compile] reads the history
// alone: what binds is what was cut. The development path, [CompileDevelopment]
// and [BindDevelopment], adds the tip as a provisional newest version, at the
// version it would be cut as, and tolerates the lenses its edge does not have
// yet. [ReadTip] derives that version, and [CheckClean] checks that the tip
// was cut.
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
	"io/fs"

	"cuelang.org/go/cue"

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
// (tip.thema, history/0.0.thema), which places it in the lineage, its Name,
// which its positions carry, and its text. [Load] names each file by its path
// within the fs.FS it read it from (ship/history/0.0.thema), so that a tool
// reads an excerpt from that FS by the name a diagnostic gives, and two
// lineages' files of the same Path are never confused. A file that was not
// read from an FS must be named by whoever makes it; one with no Name is not
// compiled.
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
// name is dir's base name. Each file is named by its path within fsys.
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
	return compile.CompileDevelopment(ctx, name, files)
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
	v := rt.Context().BuildFile(c.File)
	if err := v.Err(); err != nil {
		return nil, diag.Convert(err, diag.Context{Op: diag.OpCompile, Vocabulary: Vocabulary(c), Generic: diag.E0112})
	}
	all := append([]thema.BindOption{thema.WithVocabulary(Vocabulary(c))}, opts...)
	if p, ok := c.Provisional(); ok {
		all = append(all, provisional.Option(p).(thema.BindOption))
	}
	return thema.BindLineage(v, rt, all...)
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
// schema, with no lenses. The files were read from no FS, and are named by
// their Path.
func Decompile(lin cue.Value) (name string, files []File, err error) {
	return compile.Decompile(lin)
}

// Format lays out the text of a version file in the authoring form's one
// layout: every block keeps its braces and every field has its own line.
// Decompiling a formatted file gives back its text exactly.
func Format(src []byte) ([]byte, error) {
	return compile.Format(src)
}
