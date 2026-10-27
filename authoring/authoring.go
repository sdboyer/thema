// Package authoring compiles thema's authoring form, the .thema files a
// lineage author writes, to the normal form that every thema tool consumes,
// and decompiles the normal form back. The form is described in
// docs/authoring-form.md.
//
// The common case, loading a lineage's directory and binding it, is one call:
// [github.com/sdboyer/thema/load.AuthoringLineage]. This package is for tools
// that need the pieces: the files as loaded, the compiled normal form as an
// AST positioned in the authoring files (to build, inspect or position errors
// of their own), the versions in order, the decompiler, or the formatter.
package authoring

import (
	"io/fs"

	"cuelang.org/go/cue"

	"github.com/sdboyer/thema/internal/compile"
)

// Ext is the extension of a version file.
const Ext = compile.Ext

// A File is one authoring file: its path relative to the lineage's directory
// (1.0.thema, history/0.0.thema) and its text.
type File = compile.File

// Compiled is a lineage in normal form, compiled from authoring files: its
// name, the normal form as an AST whose every node is positioned in the
// authoring file it came from, and its versions in order. Build the AST with
// [cue.Context.BuildFile] and bind it with [github.com/sdboyer/thema.BindLineage].
type Compiled = compile.Compiled

// Load reads the authoring files of the lineage in dir, a directory of fsys:
// every version file at the top of dir and under dir/history. The lineage's
// name is dir's base name.
func Load(fsys fs.FS, dir string) (name string, files []File, err error) {
	return compile.Load(fsys, dir)
}

// Compile compiles the authoring files of the lineage name to its normal
// form. Errors are positioned in the authoring files, and worded in the
// authoring form's terms.
func Compile(ctx *cue.Context, name string, files []File) (*Compiled, error) {
	return compile.Compile(ctx, name, files)
}

// Decompile recovers the authoring files of a lineage in normal form: the
// lineage value, as compiled or as loaded, which need not be bound. It is the
// inverse of Compile on the lineages Compile produces; a lens outside that
// form is an error naming its edge, never an approximation.
func Decompile(lin cue.Value) (name string, files []File, err error) {
	return compile.Decompile(lin)
}

// Format lays out the text of a version file in the authoring form's one
// layout: every block keeps its braces and every field has its own line.
// Decompiling a formatted file gives back its text exactly.
func Format(src []byte) ([]byte, error) {
	return compile.Format(src)
}
