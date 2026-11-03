package thema

import (
	"fmt"
	"slices"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/diag"
)

// A Placement is a file of CUE and the path at which [Compose] declares it.
type Placement struct {
	// Path is where the file's declarations go: a path of regular field and
	// definition labels, such as operations.get.query. An empty path declares
	// them at the top of the package. A definition closes what it declares,
	// so a lineage under one binds only where the package also declares it a
	// thema.Lineage, as #Op: h: thema.Lineage does: a lineage compiled from
	// the authoring form does not say so itself.
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
// The files are one package, and read as one, as they would had their
// author written them there:
//   - Two placements at the same path are unified.
//   - The path's labels are written as identifiers wherever CUE can read
//     one back, so other placements and the caller's files can refer to
//     them. Like any field of a package, a label at the top of the path
//     binds its name in every file: a placement at int is what int means
//     in all of them. A label that is the name of one of its own file's
//     imports would hide the import, and is refused.
//   - A name a placement's file refers to without declaring it resolves in
//     the composed package, to what the other placements or the caller's
//     files declare at its top.
//   - A file attribute applies to the composed file: an @if that does not
//     hold leaves the placement out of the package.
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
		labels, err := placementLabels(i, p.Path, p.File)
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

// placementLabels returns the labels of the fields that nest f's
// declarations at p.
func placementLabels(i int, p cue.Path, f *ast.File) ([]ast.Label, error) {
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
			name := sel.Unquoted()
			// import and package are identifiers, but read as clauses
			// where the nested field begins.
			if ast.StringLabelNeedsQuoting(name) || name == "import" || name == "package" {
				labels = append(labels, ast.NewString(name))
				continue
			}
			if imported(f, name) {
				return nil, diag.New(diag.E0627).
					Headlinef("a placement's path does not name its file's imports, and placement %d's path %s names %s", i, p, name).
					Notef(token.NoPos, "the file imports a package as %s, and a field %s around its declarations would hide the import from them", name, name).
					Help("place the file at another path, or import the package under another name").
					Err()
			}
			labels = append(labels, ast.NewIdent(name))
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

// imported reports whether f imports a package under name.
func imported(f *ast.File, name string) bool {
	for spec := range f.ImportSpecs() {
		if spec.Name != nil {
			if spec.Name.Name == name {
				return true
			}
			continue
		}
		path, err := literal.Unquote(spec.Path.Value)
		if err == nil && ast.ParseImportPath(path).Qualifier == name {
			return true
		}
	}
	return false
}
