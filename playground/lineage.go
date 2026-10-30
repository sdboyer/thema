package playground

import (
	"fmt"
	"strconv"
	"strings"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/util"
	tload "github.com/sdboyer/thema/load"
)

// playgroundModule is the module a normal-form file that imports thema is
// loaded in. It is never fetched; only thema's own module is.
const playgroundModule = "playground.local/in"

// Bind binds a lineage and describes it.
func (s *Session) Bind(req BindRequest) BindResponse {
	e := s.lineage(req.Lineage)
	if e.err != nil {
		return BindResponse{Result: failed(e.err), Normal: e.normal}
	}
	resp := BindResponse{Result: failed(e.pending), Name: e.lin.Name(), Normal: e.normal}
	for _, sch := range e.lin.All() {
		resp.Versions = append(resp.Versions, sch.Version().String())
	}
	if e.tip != nil {
		resp.Tip = &TipInfo{Version: e.tip.Version.String(), Clean: e.tip.Clean, Provisional: !e.tip.Clean}
	}
	return resp
}

// lineage binds in, or returns what binding it said before.
func (s *Session) lineage(in LineageInput) cached {
	if e, ok := s.cache.get(in); ok {
		return e
	}
	e := s.bind(in)
	s.cache.put(in, e)
	return e
}

func (s *Session) bind(in LineageInput) cached {
	switch {
	case in.Normal != nil && len(in.Files) > 0:
		return cached{err: diag.New(diag.E0802).Headline("the lineage is given in both forms").
			Help("send either the authoring form's files or one normal-form file").Err()}
	case in.Normal != nil:
		return s.bindNormal(*in.Normal, in.Path)
	case len(in.Files) > 0:
		return s.bindAuthoring(in.Name, in.Files)
	}
	return cached{err: diag.New(diag.E0802).Headline("no lineage was given").
		Help("send the authoring form's files, or one normal-form file").Err()}
}

// bindAuthoring compiles and binds a lineage in the authoring form with its
// tip, on the development path: the tip is what the author edits. Its normal
// form is reported even when binding fails, since that is often what the
// author needs to see.
func (s *Session) bindAuthoring(name string, files []File) cached {
	if name == "" {
		return cached{err: diag.New(diag.E0802).Headline("the authoring form's lineage has no name").
			Help("a lineage in the authoring form is named by its directory; send its name").Err()}
	}
	ctx := s.rt.Context()
	c, err := authoring.CompileDevelopment(ctx, name, authoringFiles(files))
	if err != nil {
		return cached{err: err}
	}
	normal, err := formatNode(c.File)
	if err != nil {
		return cached{err: diag.InternalErr(err)}
	}
	lin, err := authoring.BindDevelopment(s.rt, c)
	return cached{lin: lin, raw: ctx.BuildFile(c.File), normal: normal, err: err, tip: c.Tip, pending: c.Pending}
}

// authoringFiles converts a request's authoring files. The request is the
// playground's FS: each file is named by its path in it.
func authoringFiles(files []File) []authoring.File {
	out := make([]authoring.File, len(files))
	for i, f := range files {
		out[i] = authoring.File{Path: f.Path, Name: f.Path, Text: []byte(f.Text)}
	}
	return out
}

// bindNormal binds a lineage in one normal-form file. A file that imports
// nothing is compiled as it is. A file that imports thema is loaded as a
// package of a module that depends on thema's embedded module; any other
// import is refused before loading, so that nothing is ever fetched.
func (s *Session) bindNormal(f File, path string) cached {
	if f.Path == "" {
		f.Path = "lineage.cue"
	}
	af, err := parser.ParseFile(f.Path, f.Text, parser.ImportsOnly)
	if err != nil {
		return cached{err: diag.Convert(err, diag.Context{Op: diag.OpLoad, Generic: diag.E0609})}
	}
	importsThema := false
	themaPath, _, _ := strings.Cut(util.ThemaModulePath, "@")
	for spec := range af.ImportSpecs() {
		p, _ := strconv.Unquote(spec.Path.Value)
		switch {
		case p == themaPath:
			importsThema = true
		case compat.IsBuiltinPackage(p):
		default:
			return cached{err: diag.New(diag.E0608).At(spec.Path.Pos()).
				Headlinef("the lineage imports %s, which is not available here", p).
				Help("the playground provides github.com/sdboyer/thema and CUE's builtin packages, and no other module").
				Err()}
		}
	}
	var v cue.Value
	if importsThema {
		v, err = s.loadWithThema(f, af.PackageName())
		if err != nil {
			return cached{err: err}
		}
	} else {
		v = s.rt.Context().CompileString(f.Text, cue.Filename(f.Path))
	}
	if path != "" {
		p := cue.ParsePath(path)
		if p.Err() != nil {
			return cached{err: diag.New(diag.E0604).Headlinef("%q is not a CUE path", path).Detail(p.Err().Error()).
				Help("the lineage's path is a path into the file, such as lin or rename.l").Err()}
		}
		v = v.LookupPath(p)
		if !v.Exists() {
			return cached{err: diag.New(diag.E0605).Headlinef("there is no value at %s in %s", path, f.Path).
				Help("the lineage's path names the field of the file that holds the lineage").Err()}
		}
	}
	lin, err := thema.BindLineage(v, s.rt)
	return cached{lin: lin, raw: v, normal: f.Text, err: err}
}

// loadWithThema loads f, which imports thema, as the package pkg of a module
// that depends on thema's embedded module.
func (s *Session) loadWithThema(f File, pkg string) (cue.Value, error) {
	// A file without a package clause is CUE's anonymous package.
	if pkg == "" {
		pkg = "_"
	}
	fsys := fstest.MapFS{f.Path: &fstest.MapFile{Data: []byte(f.Text)}}
	binst, err := tload.InstanceWithThema(tload.AsModFS(fsys, playgroundModule), ".", tload.Package(pkg))
	if err != nil {
		return cue.Value{}, diag.Convert(err, diag.Context{Op: diag.OpLoad, Generic: diag.E0609})
	}
	return s.rt.Context().BuildInstance(binst), nil
}

func formatNode(n ast.Node) (string, error) {
	b, err := format.Node(n)
	if err != nil {
		return "", fmt.Errorf("formatting the normal form: %w", err)
	}
	return string(b), nil
}

// schema returns the lineage's schema at version, the latest if version is
// empty.
func schema(lin thema.Lineage, version string) (thema.Schema, error) {
	if version == "" {
		return lin.Latest(), nil
	}
	sv, err := parseVersion(version)
	if err != nil {
		return nil, err
	}
	return lin.Schema(sv)
}

func parseVersion(version string) (thema.SyntacticVersion, error) {
	sv, err := thema.ParseSyntacticVersion(version)
	if err != nil {
		return sv, diag.New(diag.E0803).Headlinef("%q is not a version", version).
			Help("a version is <major>.<minor>, such as 1.0").Err()
	}
	return sv, nil
}
