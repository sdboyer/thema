package thema

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/diag"
)

// A Placement is a file of CUE and the path at which [Compose] declares it.
type Placement struct {
	// Path is where the file's declarations go: a path of regular field and
	// definition labels, such as operations.get.query or #Op.h. An empty path
	// declares them at the top of the package.
	Path cue.Path
	// File is the CUE to declare at Path, such as a lineage compiled from the
	// authoring form (authoring.Compiled.File). Its package clause, if any, is
	// replaced; its imports are kept.
	File *ast.File
}

// Compose returns the files of one CUE package, named pkg, that declares
// each placement's file at its path: one file per placement, holding the
// placement's imports and its other declarations nested under the path.
//
// Composing lineages this way, rather than filling separately built values
// into one with FillPath or unifying them in Go, gives a value CUE loaded
// from files: every lineage in it is a field of a loaded package, as it
// would be had its author written it there, and plain cue evaluates it.
//
// The declarations are the placements' own nodes, not copies, so positions
// are those of the files the placements were built from. Each file is named
// thema_compose_<i>.cue, for placement i, and holds no syntax that carries a
// position of its own. The caller loads the files, alongside its package's
// own files if it has any, with its own load.Config; load.ComposedInstance
// loads them on their own. Two placements at the same path are unified.
func Compose(pkg string, ps ...Placement) ([]*ast.File, error) {
	if !ast.IsValidIdent(pkg) || pkg == "_" || pkg[0] == '#' || pkg[0] == '_' {
		return nil, diag.New(diag.E0627).
			Headlinef("%q cannot name a composed package", pkg).
			Note(token.NoPos, "a package name is an identifier that does not start with # or _, such as lineages").
			Help("name the package with an identifier").
			Err()
	}
	files := make([]*ast.File, 0, len(ps))
	for i, p := range ps {
		if p.File == nil {
			panic(fmt.Sprintf("thema.Compose: placement %d has no file", i))
		}
		labels, err := placementLabels(i, p.Path)
		if err != nil {
			return nil, err
		}
		files = append(files, placed(fmt.Sprintf("thema_compose_%d.cue", i), pkg, labels, p.File))
	}
	return files, nil
}

// placementLabels returns the labels of the fields that nest a placement's
// declarations at p.
func placementLabels(i int, p cue.Path) ([]ast.Label, error) {
	if err := p.Err(); err != nil {
		return nil, diag.New(diag.E0627).
			Headlinef("placement %d's path is not a valid path", i).
			Detail(err.Error()).
			Help("build the path from labels, with cue.MakePath or cue.ParsePath").
			Err()
	}
	sels := p.Selectors()
	labels := make([]ast.Label, 0, len(sels))
	for _, sel := range sels {
		var why string
		switch {
		case sel.IsConstraint() && sel.ConstraintType() == cue.PatternConstraint:
			why = "a pattern, which constrains fields rather than naming one"
		case sel.IsConstraint():
			why = "an optional or required constraint, which declares no field"
		case sel.LabelType() == cue.IndexLabel:
			why = "a list index, and a placement goes in a field"
		case sel.LabelType() == cue.HiddenLabel, sel.LabelType() == cue.HiddenDefinitionLabel:
			why = "a hidden label, which belongs to the package it is written in, and would name another field in the composed package"
		case sel.IsDefinition():
			labels = append(labels, ast.NewIdent(sel.String()))
			continue
		case sel.IsString():
			labels = append(labels, ast.NewStringLabel(sel.Unquoted()))
			continue
		default:
			why = "a selector a field label cannot write"
		}
		return nil, diag.New(diag.E0627).
			Headlinef("placement %d's path %s cannot be written as fields", i, p).
			Notef(token.NoPos, "%s is %s", sel, why).
			Help("place the file at a path of regular field and definition labels").
			Err()
	}
	return labels, nil
}

// placed returns the file named name, in package pkg, that declares f's
// declarations under labels, keeping f's file attributes and imports at its
// top.
func placed(name, pkg string, labels []ast.Label, f *ast.File) *ast.File {
	var preamble, body []ast.Decl
	pre := f.Preamble()
	for i, d := range f.Decls {
		if i < len(pre) {
			if _, ok := d.(*ast.Package); !ok {
				preamble = append(preamble, d)
			}
			continue
		}
		body = append(body, d)
	}
	for i := len(labels) - 1; i >= 0; i-- {
		body = []ast.Decl{&ast.Field{Label: labels[i], Value: &ast.StructLit{Elts: body}}}
	}

	// File attributes precede the package clause; imports follow it.
	var attrs, imports []ast.Decl
	for _, d := range preamble {
		if _, ok := d.(*ast.Attribute); ok && len(imports) == 0 {
			attrs = append(attrs, d)
			continue
		}
		imports = append(imports, d)
	}
	decls := make([]ast.Decl, 0, len(attrs)+1+len(imports)+len(body))
	decls = append(decls, attrs...)
	decls = append(decls, &ast.Package{Name: ast.NewIdent(pkg)})
	decls = append(decls, imports...)
	return &ast.File{Filename: name, Decls: append(decls, body...)}
}
