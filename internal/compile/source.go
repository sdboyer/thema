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
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
)

// Ext is the extension of authoring files.
const Ext = ".thema"

// The fields of a version file.
const (
	fieldSchema   = "schema"
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
			return "", nil, err
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), Ext) {
				continue
			}
			p := path.Join(d, e.Name())
			b, err := fs.ReadFile(fsys, p)
			if err != nil {
				return "", nil, err
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
}

// header is the position of the start of the file, where constructs the file
// implies but does not spell out are positioned.
func (v *version) header() token.Pos {
	return v.tf.Pos(0, token.NoRelPos)
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

	const only = "a version file has only schema, ForwardLens and BackwardLens"
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
			report(diag.E0104, d.Pos(), "%s imports a package", f.Path).Help("a version file imports nothing; a schema uses only CUE's builtins")
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
			*slot = d
		default:
			report(diag.E0105, d.Pos(), "%s has a declaration that is not a field", f.Path).Help(only)
		}
	}
	if v.schema == nil {
		report(diag.E0108, v.header(), "%s declares no schema", f.Path).Help("every version file declares schema: {...}")
	}
	errs = cerrors.Append(errs, noShorthand(af))
	if errs != nil {
		return nil, errs
	}
	return v, nil
}

// noShorthand rejects CUE's struct shorthand, a: b: c, anywhere in a version
// file: a version file writes every struct with its braces, so that a lens
// body, a lacunas key and a schema read the same way. The parser leaves the
// shorthand's struct without a brace position.
func noShorthand(af *ast.File) cerrors.Error {
	var errs cerrors.Error
	ast.Walk(af, func(n ast.Node) bool {
		f, ok := n.(*ast.Field)
		if !ok {
			return true
		}
		s, ok := f.Value.(*ast.StructLit)
		if !ok || s.Lbrace.IsValid() || len(s.Elts) != 1 {
			return true
		}
		inner, ok := s.Elts[0].(*ast.Field)
		if !ok {
			return true
		}
		outer, _, _ := ast.LabelName(f.Label)
		label, _, _ := ast.LabelName(inner.Label)
		errs = cerrors.Append(errs, diag.New(diag.E0109).At(inner.Pos()).
			Headlinef("%s: %s: ... is CUE's struct shorthand, which a version file does not use", outer, label).
			Note(token.NoPos, "every struct in a version file keeps its braces, so that a lens body, a lacunas key and a schema read the same way").
			Helpf("write %s: {%s: ...}", outer, label).
			Err())
		return true
	}, nil)
	return errs
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
