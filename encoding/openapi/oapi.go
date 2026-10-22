package openapi

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/encoding/openapi"
	"cuelang.org/go/pkg/strings"
	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/astutil"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/util"
)

// Config controls OpenAPI derivation from a Thema schema.
type Config struct {
	*openapi.Config

	// Group indicates that the [thema.Schema] is from a grouped lineage - the root
	// schema itself does not represent an object that is ever expected to exist
	// independently, but each of its top-level fields do, including definitions and
	// optional fields.
	//
	// NOTE - https://github.com/grafana/thema/issues/62 is the issue for formalizing
	// the group concept. Fixing that issue will obviate this field. Once fixed,
	// this field will be deprecated and ignored.
	Group bool

	// RootName specifies the name to use for the type representing the root of the
	// schema. If empty, this defaults to titlecasing of the lineage name.
	//
	// No-op if [Group] is true.
	RootName string

	// Subpath specifies a path within the provided [thema.Schema] that should be
	// translated, rather than the root schema.
	//
	// No-op if [Group] is true.
	Subpath cue.Path

	// SplitSchema generates the schemas from schema and _join values, and it merges them
	// to generate the file. It's useful when their merged _schema version doesn't show
	// the desired results.
	SplitSchema bool
}

// GenerateSchema creates an OpenAPI document that represents the provided Thema
// Schema as an OpenAPI schema component.
//
// Returns the result as a CUE AST, which is suitable for direct manipulation and
// marshaling to either JSON or YAML.
func GenerateSchema(sch thema.Schema, cfg *Config) (*ast.File, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Config == nil {
		cfg.Config = &openapi.Config{}
	}

	gen := &oapiGen{
		cfg: cfg,
		sch: sch,
		onf: cfg.NameFunc,
		// Underlying() gives us the _schema ref to the definition. We need to
		// eliminate path elements leading into the actual user-specified definition.
		schdef: sch.Underlying().LookupPath(cue.MakePath(cue.Hid("_schema", util.ThemaPackageID))),
		schraw: sch.Underlying().LookupPath(cue.MakePath(cue.Str("schema"))),
		// The lineage's joinSchema, rather than SchemaDef._join: the latter's
		// value is a reference to the former, which the generator cannot
		// translate when it has to inline it.
		join:  sch.Lineage().Underlying().LookupPath(cue.MakePath(cue.Str("joinSchema"))),
		bpath: sch.Underlying().Path(),
	}

	var decls []ast.Decl
	var err error
	if cfg.Group {
		decls, err = genGroup(gen)
	} else if cfg.SplitSchema {
		decls, err = genSingle(gen, gen.schraw, gen.join)
	} else if len(cfg.Subpath.Selectors()) > 0 {
		decls, err = genSingle(gen, gen.schdef)
	} else {
		// _schema is _join & schema, but the generator can't handle values
		// that are thema's references to them when it inlines them. Feed it the
		// two operands directly; joinSchema first, so its fields come first.
		decls, err = genSchema(gen, gen.join, gen.schraw)
	}
	if err != nil {
		return nil, err
	}

	// TODO recursively sort output to improve stability of output
	return &ast.File{
		Decls: []ast.Decl{
			ast.NewStruct(
				"openapi", ast.NewString("3.0.0"),
				"info", ast.NewStruct(
					"title", ast.NewString(gen.name),
					"version", ast.NewString(sch.Version().String()),
				),
				"paths", ast.NewStruct(),
				"components", ast.NewStruct(
					"schemas", &ast.StructLit{Elts: decls},
				),
			),
		},
	}, nil
}

type oapiGen struct {
	cfg *Config
	sch thema.Schema
	// the SchemaDef._schema
	schdef cue.Value
	// the SchemaDef.schema
	schraw cue.Value
	// the SchemaDef._join
	join cue.Value

	// overall name for the generated oapi doc
	name string

	// original NameFunc
	onf func(cue.Value, cue.Path) string

	// full prefix path that leads up to the SchemaDef, e.g. lin._sortedSchemas[0]
	bpath cue.Path
}

func genGroup(gen *oapiGen) ([]ast.Decl, error) {
	ctx := gen.sch.Underlying().Context()
	iter, err := gen.schdef.Fields(cue.Definitions(true), cue.Optional(true))
	if err != nil {
		panic(fmt.Errorf("unreachable - should always be able to get iter for struct kinds: %w", err))
	}

	var decls []ast.Decl
	for iter.Next() {
		val, sel := iter.Value(), iter.Selector()
		name := strings.Trim(sel.String(), "?#")

		v := ctx.CompileString(fmt.Sprintf("#%s: _", name))
		defpath := cue.MakePath(cue.Def(name))
		defsch := v.FillPath(defpath, val)

		cfgi := *gen.cfg.Config
		cfgi.NameFunc = func(val cue.Value, path cue.Path) string {
			return gen.nfSingle(val, path, defpath, name)
		}
		if cfgi.DescriptionFunc == nil {
			cfgi.DescriptionFunc = gen.describer()
		}
		cfgi.Info = newHeader(defpath.String(), gen.sch.Version())

		part, err := openapi.Generate(defsch, &cfgi)
		if err != nil {
			return nil, fmt.Errorf("failed generation for grouped field %s: %w", sel, err)
		}

		decls = append(decls, getSchemas(part)...)
	}

	gen.name = util.SanitizeLabelString(gen.sch.Lineage().Name())
	return decls, nil
}

func genSingle(gen *oapiGen, values ...cue.Value) ([]ast.Decl, error) {
	decls := make([]ast.Decl, 0)
	for _, v := range values {
		decl, err := genSchema(gen, v)
		if err != nil {
			return nil, err
		}
		decls = append(decls, decl...)
	}

	return decls, nil
}

// genSchema generates a schema named for the lineage from the unification of
// vals. When a Subpath is configured, it is applied to each of vals.
func genSchema(gen *oapiGen, vals ...cue.Value) ([]ast.Decl, error) {
	hasSubpath := len(gen.cfg.Subpath.Selectors()) > 0
	name := util.SanitizeLabelString(gen.sch.Lineage().Name())
	if gen.cfg.RootName != "" {
		name = gen.cfg.RootName
	} else if hasSubpath {
		sel := gen.cfg.Subpath.Selectors()
		name = sel[len(sel)-1].String()
	}

	v := gen.sch.Underlying().Context().CompileString(fmt.Sprintf("#%s: _", name))
	defpath := cue.MakePath(cue.Def(name))
	// v, defpath := newEmptyDef(gen.sch.Underlying().Context(), name)
	defsch := v
	for _, val := range vals {
		if hasSubpath {
			for i, sel := range gen.cfg.Subpath.Selectors() {
				if !val.Allows(sel) {
					return nil, errors.Newf(cuetil.FirstNonThemaPos(val), "subpath %q not present in schema", cue.MakePath(gen.cfg.Subpath.Selectors()[:i+1]...))
				}
			}
			if err := pathAllowed(gen.join, gen.cfg.Subpath); err != nil {
				return nil, err
			}
			val = val.LookupPath(gen.cfg.Subpath)
		}
		defsch = defsch.FillPath(defpath, val)
	}

	cfgi := *gen.cfg.Config
	cfgi.NameFunc = func(val cue.Value, path cue.Path) string {
		return gen.nfSingle(val, path, defpath, name)
	}
	if cfgi.DescriptionFunc == nil {
		cfgi.DescriptionFunc = gen.describer()
	}
	cfgi.Info = newHeader(name, gen.sch.Version())

	f, err := openapi.Generate(defsch.Eval(), &cfgi)
	if err != nil {
		return nil, err
	}

	gen.name = name
	return getSchemas(f), nil
}

// For generating a single, our NameFunc must:
// - Eliminate any path prefixes on the element, both internal lineage and wrapping
// - Replace the name "_schema" with the desired name
// - Call the user-provided NameFunc, if any
// - Remove CUE markers like #, !, ?, and the _ of hidden labels
func (gen *oapiGen) nfSingle(val cue.Value, path, defpath cue.Path, name string) string {
	tpath := cuetil.TrimPathPrefix(trimThemaPathPrefix(path, gen.bpath), defpath)

	if path.String() == "" || tpath.String() == defpath.String() {
		return name
	}
	switch val {
	case gen.schraw, gen.join:
		return ""
	case gen.schdef:
		return name
	}
	if gen.onf != nil {
		return gen.onf(val, tpath)
	}
	// Hidden labels (e.g. _aMap, _#Foo) are valid reference targets; their
	// leading underscore is a CUE marker like #.
	return strings.TrimLeft(strings.Trim(tpath.String(), "?#"), "_#")
}

// themaDocPaths are the fields of thema's CUE through which a schema value is
// reached. Their docs describe thema itself, not any schema, but the schema
// value carries them.
var themaDocPaths = []cue.Path{
	cue.MakePath(cue.Str("Lineage"), cue.Str("joinSchema")),
	cue.MakePath(cue.Str("SchemaDef"), cue.Str("schema")),
	cue.MakePath(cue.Str("SchemaDef"), cue.Hid("_join", util.ThemaPackageID)),
	cue.MakePath(cue.Str("SchemaDef"), cue.Hid("_schema", util.ThemaPackageID)),
}

// describer returns the default DescriptionFunc. It joins the doc comments
// attached to a value, as the generator does when no DescriptionFunc is set,
// but omits those of the thema fields in themaDocPaths.
//
// Docs are matched by text rather than position: thema's CUE may be loaded more
// than once (e.g. from the embedded module and as files of a lineage that lives
// in thema's own module), and only the text is the same each time.
func (gen *oapiGen) describer() func(cue.Value) string {
	themaDocs := make(map[string]bool)
	for _, p := range themaDocPaths {
		for _, cg := range gen.sch.Lineage().Runtime().Underlying().LookupPath(p).Doc() {
			themaDocs[cg.Text()] = true
		}
	}

	return func(v cue.Value) string {
		var docs []string
		for _, cg := range v.Doc() {
			if !themaDocs[cg.Text()] {
				docs = append(docs, cg.Text())
			}
		}
		return strings.Join(docs, "\n\n")
	}
}

func getSchemas(f *ast.File) []ast.Decl {
	compos := orp(astutil.GetFieldByLabel(f, "components"))
	schemas := orp(astutil.GetFieldByLabel(compos.Value, "schemas"))
	return schemas.Value.(*ast.StructLit).Elts
}

func pathAllowed(v cue.Value, path cue.Path) error {
	for i, sel := range path.Selectors() {
		if !v.Allows(sel) {
			return errors.Newf(v.Pos(), "subpath %q not present in schema", cue.MakePath(path.Selectors()[:i+1]...))
		}
	}

	return nil
}

func orp[T any](t T, err error) T {
	if err != nil {
		panic(err)
	}
	return t
}

func newHeader(title string, v thema.SyntacticVersion) *ast.StructLit {
	return ast.NewStruct(
		"title", ast.NewString(strings.Trim(title, "#?")),
		"version", ast.NewString(v.String()),
	)
}

func trimThemaPathPrefix(p, base cue.Path) cue.Path {
	if !cuetil.PathHasPrefix(p, base) {
		return p
	}

	rest := p.Selectors()[len(base.Selectors()):]
	if len(rest) == 0 {
		return cue.Path{}
	}
	switch rest[0].String() {
	case "schema", "_schema", "_join", "joinSchema":
		return cue.MakePath(rest[1:]...)
	default:
		return cue.MakePath(rest...)
	}
}

// func p(val cue.Value) string {
// 	return string(astutil.FmtNodeP(astutil.Format(val)))
// }
//
// func newEmptyDef(ctx *cue.Context, name string) (cue.Value, cue.Path) {
// 	rnd := randSeq(20)
// 	v := ctx.CompileString(fmt.Sprintf("%s: #%s: _", rnd, name))
// 	return v.LookupPath(cue.MakePath(cue.Str(rnd))), cue.MakePath(cue.Def(name))
// }

// var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
//
// // randSeq produces random (basic, not crypto) letters of a given length.
// func randSeq(n int) string {
// 	b := make([]rune, n)
// 	for i := range b {
// 		b[i] = letters[rand.Intn(len(letters))]
// 	}
// 	return string(b)
// }
