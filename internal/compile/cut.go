package compile

import (
	"fmt"
	"path"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
)

// CutFiles returns the two files that cutting the tip in files as version v
// writes: the frozen version, history/<v>.thema, and the tip as it is after
// the cut. The frozen version is the whole tip file, notes and lenses
// included, laid out as [Format] lays it out: a lens names its instances self
// and prev wherever its file is, so cutting renames nothing. The
// tip after the cut is clean: its Schema, with the doc comment above it and
// the comments within it, and the imports the Schema uses. The lenses, the
// comments attached to them and any detached notes stay in the frozen file
// alone; they describe the edge, or the provenance of the version just cut,
// and would go stale in the tip.
//
// CutFiles transforms the text only. Whether the tip may be cut, and as which
// version, is for the caller to decide, as authoring.CutTip does.
func CutFiles(files []File, v thema.SyntacticVersion) (frozen, tip File, err error) {
	_, tipFile, err := splitLayout(files)
	if err != nil {
		return File{}, File{}, err
	}
	if tipFile == nil {
		return File{}, File{}, noTip()
	}

	af, err := parser.ParseFile(TipFile, tipFile.Text, parser.ParseComments)
	if err != nil {
		return File{}, File{}, diag.Convert(err, diag.Context{Op: diag.OpCompile, Generic: diag.E0102})
	}
	var schema *ast.Field
	var specs []*ast.ImportSpec
	for _, d := range af.Decls {
		switch d := d.(type) {
		case *ast.ImportDecl:
			specs = append(specs, d.Specs...)
		case *ast.Field:
			if name, _, _ := ast.LabelName(d.Label); name == fieldSchema {
				schema = d
			}
		}
	}
	if schema == nil {
		return File{}, File{}, diag.InternalErr(fmt.Errorf("%s has no Schema, which compiling it should have reported", TipFile))
	}

	frozenText, err := printVersion(af)
	if err != nil {
		return File{}, File{}, err
	}

	// The clean tip is built from fresh import specs, so that no comment
	// attached to an import comes with them; every import is one the Schema
	// uses, under its own name, since compiling the tip refuses any other
	// (E0147, E0105), and lenses call the strings functions without one. It
	// takes the Schema field with only its doc comment among the comments
	// attached to the field itself.
	clean := &ast.File{}
	if len(specs) > 0 {
		decl := &ast.ImportDecl{}
		for _, s := range specs {
			decl.Specs = append(decl.Specs, ast.NewImport(nil, s.Path.Value[1:len(s.Path.Value)-1]))
		}
		clean.Decls = append(clean.Decls, decl)
	}
	var docs []*ast.CommentGroup
	for _, cg := range ast.Comments(schema) {
		if cg.Doc {
			docs = append(docs, cg)
		}
	}
	ast.SetComments(schema, docs)
	clean.Decls = append(clean.Decls, schema)
	cleanText, err := printVersion(clean)
	if err != nil {
		return File{}, File{}, err
	}

	return File{Path: path.Join(HistoryDir, v.String()+Ext), Text: frozenText},
		File{Path: TipFile, Text: cleanText}, nil
}

// printVersion prints f as CUE and lays it out as [Format] does.
func printVersion(f *ast.File) ([]byte, error) {
	b, err := format.Node(f)
	if err != nil {
		return nil, diag.InternalErr(fmt.Errorf("printing a version file: %w", err))
	}
	out, err := Format(b)
	if err != nil {
		return nil, diag.InternalErr(fmt.Errorf("formatting a version file: %w", err))
	}
	return out, nil
}
