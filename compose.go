package thema

import (
	"fmt"
	"slices"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/diag"
)

// A Placement is a file of CUE and the path at which [Compose] declares it.
type Placement struct {
	// Path is where the file's declarations go: a path of regular field and
	// definition labels, such as operations.get.query. An empty path declares
	// them at the top of the package. A definition closes what it declares,
	// and a lineage under one does not bind today (#270).
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
// Each file is named thema_compose_<i>.cue, for placement i, and keeps the
// positions of the file the placement was built from: the syntax Compose
// adds carries none. The file attributes that precede a placement's package
// clause, or that lead a file without one, stay at the top; everything else
// before the declarations follows the new package clause. The caller loads
// the files, alongside its package's own files if it has any, with its own
// load.Config; load.ComposedInstance loads them on their own.
//
// The files are one package, and read as one:
//   - Two placements at the same path are unified.
//   - A name a placement's file refers to without declaring it resolves in
//     the composed package, to what the other placements or the caller's
//     files declare at its top. The path's own labels are written quoted,
//     so they never capture a reference.
//   - Hidden labels belong to the package they are written in, which for
//     every placement is now pkg: two placements at one path share their
//     hidden fields, though they were separate where they were built.
func Compose(pkg string, ps ...Placement) ([]*ast.File, error) {
	if !packageName(pkg) {
		return nil, diag.New(diag.E0627).
			Headlinef("a composed package is named by an identifier, and %q is not one", pkg).
			Note(token.NoPos, "a package name is an identifier that does not start with # or _, and is not a keyword such as if or let").
			Help("name the package with such an identifier, such as lineages").
			Err()
	}
	if len(ps) == 0 {
		return nil, diag.New(diag.E0627).
			Headline("a composed package declares at least one placement, and none was given").
			Help("pass Compose a placement for each file to declare").
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

// packageName reports whether pkg is a name a package clause can declare,
// and that is neither hidden nor a definition.
func packageName(pkg string) bool {
	if !ast.IsValidIdent(pkg) || pkg[0] == '#' || pkg[0] == '_' {
		return false
	}
	f, err := parser.ParseFile("", "package "+pkg)
	return err == nil && f.PackageName() == pkg
}

// placementLabels returns the labels of the fields that nest a placement's
// declarations at p. A regular label is written quoted: a quoted label
// declares the same field, but binds no identifier, so it cannot capture a
// reference the file makes to an import or a builtin, in its own file or in
// another of the package.
func placementLabels(i int, p cue.Path) ([]ast.Label, error) {
	if err := p.Err(); err != nil {
		return nil, diag.New(diag.E0627).
			Headlinef("a placement's path is a valid CUE path, and placement %d's has an error", i).
			Detail(err.Error()).
			Help("build the path from selectors with cue.MakePath, or correct the string given to cue.ParsePath").
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
			labels = append(labels, ast.NewString(sel.Unquoted()))
			continue
		default:
			why = "a selector a field label cannot write"
		}
		return nil, diag.New(diag.E0627).
			Headlinef("a placement's path is made of field and definition labels, and placement %d's path %s is not", i, p).
			Notef(token.NoPos, "%s is %s", sel, why).
			Help("place the file at a path of regular field and definition labels").
			Err()
	}
	return labels, nil
}

// placed returns the file named name, in package pkg, that declares f's
// declarations under labels. What precedes f's package clause, or leads a
// file without one, are file attributes, and precede the new package
// clause; f's imports, and what is between them, follow it.
func placed(name, pkg string, labels []ast.Label, f *ast.File) *ast.File {
	clause := &ast.Package{Name: ast.NewIdent(pkg)}
	head, rest := 0, 0
	for i, d := range f.Decls {
		if old, ok := d.(*ast.Package); ok {
			ast.SetComments(clause, ast.Comments(old))
			head, rest = i, i+1
			break
		}
	}
	if rest == 0 {
		for head < len(f.Decls) && fileLevel(f.Decls[head]) {
			head++
		}
		rest = head
	}
	body := max(rest, len(f.Preamble()))

	nested := slices.Clone(f.Decls[body:])
	for i := len(labels) - 1; i >= 0; i-- {
		nested = []ast.Decl{&ast.Field{Label: labels[i], Value: &ast.StructLit{Elts: nested}}}
	}
	decls := make([]ast.Decl, 0, head+1+body-rest+len(nested))
	decls = append(decls, f.Decls[:head]...)
	decls = append(decls, clause)
	decls = append(decls, f.Decls[rest:body]...)
	return &ast.File{Filename: name, Decls: append(decls, nested...)}
}

// fileLevel reports whether d can lead a file as a file attribute does.
func fileLevel(d ast.Decl) bool {
	switch d.(type) {
	case *ast.Attribute, *ast.CommentGroup:
		return true
	}
	return false
}
