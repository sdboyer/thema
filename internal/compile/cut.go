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
// included, with the tip's instance in its lenses renamed from TipParam to
// v's parameter, v<major>_<minor>, and laid out as [Format] lays it out. The
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
			name, _, _ := ast.LabelName(d.Label)
			switch name {
			case fieldSchema:
				schema = d
			case fieldForward, fieldBackward:
				renameTipParam(d, paramName(v))
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

// renameTipParam renames, in the lens block f on the edge into the tip, the
// tip's instance from TipParam to to: the block's own field named TipParam,
// and every reference to it. A lens refers to nothing but its instance, the
// operations and the strings functions (E0418), so every identifier tip that
// is neither a label nor a selector is the instance. Labels beneath the
// block's own fields are fields of the instances, and are left alone.
func renameTipParam(f *ast.Field, to string) {
	s, ok := f.Value.(*ast.StructLit)
	if !ok {
		return
	}
	for _, d := range s.Elts {
		if pf, ok := d.(*ast.Field); ok {
			if id, ok := pf.Label.(*ast.Ident); ok && id.Name == TipParam {
				id.Name = to
			}
		}
	}
	skip := make(map[*ast.Ident]bool)
	ast.Walk(s, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := n.Sel.(*ast.Ident); ok {
				skip[id] = true
			}
		case *ast.Field:
			if id, ok := n.Label.(*ast.Ident); ok {
				skip[id] = true
			}
		case *ast.Ident:
			if !skip[n] && n.Name == TipParam {
				n.Name = to
			}
		}
		return true
	}, nil)
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
