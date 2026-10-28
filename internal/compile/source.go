package compile

import (
	"errors"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue/ast"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compat"
)

// Ext is the extension of authoring files.
const Ext = ".thema"

// The fields of a version file.
const (
	fieldSchema   = "Schema"
	fieldForward  = "ForwardLens"
	fieldBackward = "BackwardLens"
)

// File is one authoring file: its path relative to the lineage's directory,
// and its text.
type File struct {
	Path string
	Text []byte
}

// Load reads the authoring files of the lineage in dir: every file with [Ext]
// in dir and in dir/history. The lineage's name is dir's base name.
func Load(fsys fs.FS, dir string) (name string, files []File, err error) {
	for _, d := range []string{dir, path.Join(dir, "history")} {
		ents, err := fs.ReadDir(fsys, d)
		if err != nil {
			if d != dir && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", nil, diag.New(diag.E0601).Headlinef("%s cannot be read", d).Detail(diag.OSReason(err)).Err()
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), Ext) {
				continue
			}
			p := path.Join(d, e.Name())
			b, err := fs.ReadFile(fsys, p)
			if err != nil {
				return "", nil, diag.New(diag.E0601).Headlinef("%s cannot be read", p).Detail(diag.OSReason(err)).Err()
			}
			files = append(files, File{Path: strings.TrimPrefix(p, dir+"/"), Text: b})
		}
	}
	return path.Base(dir), files, nil
}

// versionFile matches a version file's path: <major>.<minor>.thema, at the top
// of the lineage's directory or under history/.
var versionFile = regexp.MustCompile(`^(?:history/)?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\` + Ext + `$`)

// A version is one parsed version file.
type version struct {
	v    thema.SyntacticVersion
	path string
	file *ast.File
	tf   *token.File
	// The file's three fields. forward and backward may be nil.
	schema, forward, backward *ast.Field
	// imports are the file's imports, each of one of CUE's builtin packages.
	imports []*ast.ImportSpec
}

// header is the position of the start of the file, where constructs the file
// implies but does not spell out are positioned.
func (v *version) header() token.Pos {
	return v.tf.Pos(0, token.NoRelPos)
}

// usedImports returns the import specs that references within f resolve to.
func usedImports(f *ast.Field) map[*ast.ImportSpec]bool {
	used := make(map[*ast.ImportSpec]bool)
	ast.Walk(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			if spec, ok := id.Node.(*ast.ImportSpec); ok {
				used[spec] = true
			}
		}
		return true
	}, nil)
	return used
}

// importDecls is the version file's imports, as a declaration, or none.
func (v *version) importDecls() []ast.Decl {
	if len(v.imports) == 0 {
		return nil
	}
	return []ast.Decl{&ast.ImportDecl{Specs: v.imports}}
}

// schemaField is the version's Schema field under the normal form's name,
// schema, at the authored field's position, with its comments.
func (v *version) schemaField() *ast.Field {
	f := *v.schema
	f.Label = at(v.schema.Label.Pos(), ast.NewIdent(normalSchema))
	return &f
}

// parseVersion parses one version file. Its name gives its version; its
// contents are at most a schema, a forward block and a backward block.
func parseVersion(f File) (*version, error) {
	m := versionFile.FindStringSubmatch(f.Path)
	if m == nil {
		return nil, diag.New(diag.E0101).
			Headlinef("%s is not a version file", f.Path).
			Helpf("a version file is named <major>.<minor>%s, in the lineage's directory or its history directory", Ext).
			Err()
	}
	af, err := parser.ParseFile(f.Path, f.Text, parser.ParseComments)
	if err != nil {
		return nil, diag.Convert(err, diag.Context{Op: diag.OpCompile, Generic: diag.E0102})
	}
	v := &version{path: f.Path, file: af, tf: af.Pos().File()}
	major, _ := strconv.ParseUint(m[1], 10, 64)
	minor, _ := strconv.ParseUint(m[2], 10, 64)
	v.v = thema.SV(uint(major), uint(minor))

	const only = "a version file has only Schema, ForwardLens and BackwardLens"
	fields := []string{fieldSchema, fieldForward, fieldBackward}
	var errs cerrors.Error
	report := func(code diag.Code, p token.Pos, format string, args ...any) *diag.B {
		b := diag.New(code).At(p).Headlinef(format, args...)
		errs = cerrors.Append(errs, b.Err())
		return b
	}
	for _, d := range af.Decls {
		switch d := d.(type) {
		case *ast.Package:
			report(diag.E0103, d.Pos(), "%s has a package clause", f.Path).Help("a version file is not a CUE package; remove the clause")
		case *ast.ImportDecl:
			for _, spec := range d.Specs {
				path, _ := literal.Unquote(spec.Path.Value)
				switch {
				case !compat.IsBuiltinPackage(path):
					report(diag.E0143, spec.Pos(), "%s imports %s, which is not one of CUE's builtin packages", f.Path, path).
						Help("a version file imports only CUE's builtin packages, such as strings, list or time")
				case spec.Name != nil:
					report(diag.E0105, spec.Pos(), "%s imports %s under another name, %s", f.Path, path, spec.Name.Name).
						Help("import a package by its own name")
				default:
					v.imports = append(v.imports, spec)
				}
			}
		case *ast.CommentGroup:
		case *ast.Field:
			name, _, err := ast.LabelName(d.Label)
			if err != nil {
				report(diag.E0105, d.Pos(), "%s has a field whose label is not a name", f.Path).Help(only)
				continue
			}
			var slot **ast.Field
			switch name {
			case fieldSchema:
				slot = &v.schema
			case fieldForward:
				slot = &v.forward
			case fieldBackward:
				slot = &v.backward
			default:
				b := report(diag.E0105, d.Pos(), "%s is not a field of a version file", name)
				if want, ok := diag.Suggest(name, fields); ok {
					b.Helpf("did you mean %s?", want)
				} else {
					b.Help(only)
				}
				continue
			}
			if *slot != nil {
				report(diag.E0106, d.Pos(), "%s declares %s twice", f.Path, name).Note((*slot).Pos(), "first declared here")
				continue
			}
			if name != fieldSchema {
				if _, ok := d.Value.(*ast.StructLit); !ok {
					report(diag.E0107, d.Value.Pos(), "%s is not a struct", name).Helpf("a lens block is {v<from>: _, v<to>: {...}}")
					continue
				}
			}
			errs = cerrors.Append(errs, noShorthand(d))
			*slot = d
		default:
			report(diag.E0105, d.Pos(), "%s has a declaration that is not a field", f.Path).Help(only)
		}
	}
	if v.schema != nil {
		// An import serves the schema: a lens calls the strings functions
		// without one, so an import the schema does not use has no purpose.
		used := usedImports(v.schema)
		var keep []*ast.ImportSpec
		for _, spec := range v.imports {
			if used[spec] {
				keep = append(keep, spec)
				continue
			}
			path, _ := literal.Unquote(spec.Path.Value)
			report(diag.E0147, spec.Pos(), "%s imports %s, which its Schema does not use", f.Path, path).
				Help("remove the import: a lens calls the strings functions without one")
		}
		v.imports = keep
	}
	if v.schema == nil {
		report(diag.E0108, v.header(), "%s declares no Schema", f.Path).Help("every version file declares Schema: {...}")
	}
	if errs != nil {
		return nil, errs
	}
	return v, nil
}

// noShorthand rejects CUE's struct shorthand on a version file's top-level
// field: Schema: a: int collapses the field into one line, and the file's
// three fields are written with their braces so that each reads as the block
// it is. Beneath them the shorthand is accepted; Format expands it. The
// parser leaves a shorthand's struct without a brace position.
func noShorthand(f *ast.Field) cerrors.Error {
	s, ok := f.Value.(*ast.StructLit)
	if !ok || s.Lbrace.IsValid() || len(s.Elts) != 1 {
		return nil
	}
	inner, ok := s.Elts[0].(*ast.Field)
	if !ok {
		return nil
	}
	outer, _, _ := ast.LabelName(f.Label)
	label, _, _ := ast.LabelName(inner.Label)
	return diag.New(diag.E0109).At(inner.Pos()).
		Headlinef("%s: %s: ... is CUE's struct shorthand, which a version file's %s does not use", outer, label, outer).
		Note(token.NoPos, "a version file's three fields keep their braces, so that each reads as the block it is").
		Helpf("write %s: {%s: ...}", outer, label).
		Err()
}

// parseVersions parses every file and orders the versions, which must be
// contiguous: each the next minor version of its predecessor, or the first
// version of the next major.
func parseVersions(files []File) ([]*version, error) {
	var errs cerrors.Error
	var vs []*version
	for _, f := range files {
		v, err := parseVersion(f)
		if err != nil {
			errs = cerrors.Append(errs, cerrors.Promote(err, ""))
			continue
		}
		vs = append(vs, v)
	}
	if errs != nil {
		return nil, errs
	}
	if len(vs) == 0 {
		return nil, diag.New(diag.E0110).
			Headline("the lineage has no version files").
			Helpf("a lineage is a directory of version files, <major>.<minor>%s, starting at 0.0%s", Ext, Ext).
			Err()
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].v.Less(vs[j].v) })
	for i, v := range vs {
		var want thema.SyntacticVersion
		if i > 0 {
			prev := vs[i-1].v
			if v.v == prev {
				return nil, diag.New(diag.E0204).At(v.header()).
					Headlinef("%s and %s both declare version %s", vs[i-1].path, v.path, v.v).
					Note(vs[i-1].header(), "the other is here").
					Help("a version has one file, at the top of the lineage's directory or under history/").
					Err()
			}
			if v.v[0] == prev[0] {
				want = thema.SV(prev[0], prev[1]+1)
			} else {
				want = thema.SV(prev[0]+1, 0)
			}
		}
		if v.v != want {
			b := diag.New(diag.E0205).At(v.header()).
				Headlinef("%s skips a version: after %s comes %s", v.path, vs[max(i-1, 0)].v, want)
			if i == 0 {
				b.Headlinef("%s is not the first version: a lineage starts at 0.0", v.path)
			}
			return nil, b.Helpf("rename the file %s%s, or add the versions between", want, Ext).Err()
		}
	}
	return vs, nil
}
