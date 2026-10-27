package compile

import (
	"errors"
	"fmt"
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
)

// Ext is the extension of authoring files.
const Ext = ".thema"

// The fields of a version file.
const (
	fieldSchema   = "schema"
	fieldForward  = "forward"
	fieldBackward = "backward"
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

// block returns the struct literal of the forward or backward field, nil if
// the field is absent.
func (v *version) block(f *ast.Field) *ast.StructLit {
	if f == nil {
		return nil
	}
	return f.Value.(*ast.StructLit)
}

// parseVersion parses one version file. Its name gives its version; its
// contents are at most a schema, a forward block and a backward block.
func parseVersion(f File) (*version, error) {
	m := versionFile.FindStringSubmatch(f.Path)
	if m == nil {
		return nil, fmt.Errorf("%s: a version file is named <major>.<minor>%s, in the lineage's directory or its history directory", f.Path, Ext)
	}
	af, err := parser.ParseFile(f.Path, f.Text, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	v := &version{path: f.Path, file: af, tf: af.Pos().File()}
	major, _ := strconv.ParseUint(m[1], 10, 64)
	minor, _ := strconv.ParseUint(m[2], 10, 64)
	v.v = thema.SV(uint(major), uint(minor))

	var errs cerrors.Error
	for _, d := range af.Decls {
		switch d := d.(type) {
		case *ast.Package:
			errs = cerrors.Append(errs, cerrors.Newf(d.Pos(), "a version file has no package clause"))
		case *ast.ImportDecl:
			errs = cerrors.Append(errs, cerrors.Newf(d.Pos(), "imports are not allowed in a version file"))
		case *ast.CommentGroup:
		case *ast.Field:
			name, _, err := ast.LabelName(d.Label)
			if err != nil {
				errs = cerrors.Append(errs, cerrors.Newf(d.Pos(), "a version file has only schema, forward and backward"))
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
				errs = cerrors.Append(errs, cerrors.Newf(d.Pos(), "%s is not a field of a version file; a version file has only schema, forward and backward", name))
				continue
			}
			if *slot != nil {
				errs = cerrors.Append(errs, cerrors.Newf(d.Pos(), "%s is declared twice", name))
				continue
			}
			if name != fieldSchema {
				if _, ok := d.Value.(*ast.StructLit); !ok {
					errs = cerrors.Append(errs, cerrors.Newf(d.Value.Pos(), "%s must be a struct of mappings", name))
					continue
				}
			}
			*slot = d
		default:
			errs = cerrors.Append(errs, cerrors.Newf(d.Pos(), "a version file has only schema, forward and backward"))
		}
	}
	if v.schema == nil {
		errs = cerrors.Append(errs, cerrors.Newf(v.header(), "%s declares no schema", f.Path))
	}
	if errs != nil {
		return nil, errs
	}
	return v, nil
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
		return nil, errors.New("no version files")
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].v.Less(vs[j].v) })
	for i, v := range vs {
		var want thema.SyntacticVersion
		if i > 0 {
			prev := vs[i-1].v
			if v.v == prev {
				return nil, cerrors.Newf(v.header(), "%s and %s both declare version %s", vs[i-1].path, v.path, v.v)
			}
			if v.v[0] == prev[0] {
				want = thema.SV(prev[0], prev[1]+1)
			} else {
				want = thema.SV(prev[0]+1, 0)
			}
		}
		if v.v != want {
			return nil, cerrors.Newf(v.header(), "%s: versions must be contiguous; after %s comes %s", v.path, vs[max(i-1, 0)].v, want)
		}
	}
	return vs, nil
}
