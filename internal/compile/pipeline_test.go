package compile

import (
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
	"errors"

	"github.com/sdboyer/thema"
	terrors "github.com/sdboyer/thema/errors"
)

// ship holds the parts of the normal form a pipeline case varies. Everything
// else is built by lineage from the authoring files' common shape:
//
//	ship/history/0.0.thema: schema with title and an optional count
//	ship/history/1.0.thema: schema with title and count: int & <10, then the
//	                        forward and backward lenses of the 0.0 <-> 1.0 edge
type ship struct {
	v0, v1 *source
	// The forward lens's count; input.count positioned at "from.count" if nil.
	fwdCount ast.Expr
	// Further fields of the forward lens's result.
	fwdExtra []ast.Decl
	// The forward lens's lacunas.
	fwdLacunas []ast.Expr
	// Whether schema 1.0 ends in ..., written as "...".
	open bool
	// Leave the forward lens's result, and its implicit copy of title,
	// unpositioned rather than at the lens.
	unpositioned bool
}

// lineage builds the normal form as the compiler would, with every node
// positioned (TestPositionsCarrySemantics and TestUnpositionedNodes say why):
//
//   - authored constructs at their text;
//   - constructs with no text of their own at the nearest authored construct:
//     a lens's from, to, input and implicit copies at the lens; a schema entry
//     and its version at the start of the version's file; the name at the start
//     of the tip's file.
func (s ship) lineage() *ast.File {
	v0, v1 := s.v0, s.v1
	s0 := at(v0.at("schema: {"), strct(
		field("title", at(v0.at("string"), ident("string"))),
		optional("count", at(v0.at("int"), ident("int"))),
	))
	s1elts := []ast.Decl{
		field("title", at(v1.at("title: string"), ident("string"))),
		field("count", at(v1.at("int & <10"), &ast.BinaryExpr{
			X:  at(v1.at("int & <10"), ident("int")),
			Op: token.AND,
			Y:  at(v1.at("<10"), &ast.UnaryExpr{Op: token.LSS, X: at(v1.at("<10"), num("10"))}),
		})),
	}
	if s.open {
		s1elts = append(s1elts, at(v1.at("..."), &ast.Ellipsis{}))
	}
	s1 := at(v1.at("schema: {"), strct(s1elts...))

	lens := func(from, to string, header, resultPos token.Pos, result []ast.Decl, lacunas []ast.Expr) ast.Expr {
		elts := []ast.Decl{
			field(from, at(header, ident("_"))),
			field(to, at(resultPos, strct(result...))),
		}
		if lacunas != nil {
			elts = append(elts, field("lacunas", at(header, ast.NewList(lacunas...))))
		}
		return at(header, strct(elts...))
	}
	bwd, fwd := v1.at("backward: {"), v1.at("forward: {")
	fwdCount := s.fwdCount
	if fwdCount == nil {
		fwdCount = sel(v1.at("from.count"), "v0_0", "count")
	}
	fwdResultPos := fwd
	if s.unpositioned {
		fwdResultPos = token.NoPos
	}
	fwdResult := append([]ast.Decl{
		// Implicit copy: title isn't mentioned in the forward lens.
		field("title", sel(fwdResultPos, "v0_0", "title")),
		field("count", fwdCount),
	}, s.fwdExtra...)

	return &ast.File{Decls: []ast.Decl{
		field("name", at(v1.header(), ast.NewString("ship"))),
		field("schemas", at(v1.header(), strct(
			key("0", at(v0.header(), strct(key("0", at(v0.header(), strct(field("schema", s0))))))),
			key("1", at(v1.header(), strct(key("0", at(v1.header(), strct(field("schema", s1))))))),
		))),
		field("lenses", at(v1.header(), strct(
			key("1.0->0.0", lens("v1_0", "v0_0", bwd, bwd, []ast.Decl{
				field("title", sel(bwd, "v1_0", "title")),
				field("count", sel(v1.at("to.count"), "v1_0", "count")),
			}, nil)),
			key("0.0->1.0", lens("v0_0", "v1_0", fwd, fwdResultPos, fwdResult, s.fwdLacunas)),
		))),
	}}
}

// lacunaLit builds a lacuna positioned at p, with the given fields.
func lacunaLit(p token.Pos, fields ...*ast.Field) ast.Expr {
	elts := make([]ast.Decl, len(fields))
	for i, f := range fields {
		elts[i] = f
	}
	return at(p, strct(elts...))
}

func str(p token.Pos, s string) ast.Expr {
	return at(p, ast.NewString(s))
}

// fieldRefs builds a list of lacuna field references, each {path: <path>},
// positioned at p.
func fieldRefs(p token.Pos, paths ...string) ast.Expr {
	refs := make([]ast.Expr, len(paths))
	for i, path := range paths {
		refs[i] = at(p, strct(field("path", str(p, path))))
	}
	return at(p, ast.NewList(refs...))
}

// A pipeline case builds a lineage from its archive and runs it through
// BindLineage, then Translate if it binds. It must fail at one of them.
type pipelineCase struct {
	build func(t *testing.T, a *archive) *ast.File
	// If set, translate data.json, an instance of 0.0, to 1.0.
	translate bool
	// The error the case must fail with.
	is error
}

func shipFrom(t *testing.T, a *archive) ship {
	return ship{v0: a.source(t, "ship/history/0.0.thema"), v1: a.source(t, "ship/history/1.0.thema")}
}

var pipelineCases = map[string]pipelineCase{
	"compat": {
		build: func(t *testing.T, a *archive) *ast.File {
			v0, v1 := a.source(t, "ship/history/0.0.thema"), a.source(t, "ship/history/0.1.thema")
			return &ast.File{Decls: []ast.Decl{
				field("name", at(v1.header(), ast.NewString("ship"))),
				field("schemas", at(v1.header(), strct(key("0", at(v1.header(), strct(
					key("0", at(v0.header(), strct(field("schema",
						at(v0.at("schema: {"), strct(field("title", at(v0.at("string"), ident("string"))))))))),
					key("1", at(v1.header(), strct(field("schema",
						at(v1.at("schema: {"), strct(field("title", at(v1.at("int"), ident("int"))))))))),
				)))))),
				field("lenses", at(v1.header(), strct(key("0.1->0.0", at(v1.at("backward: {"), strct(
					field("v0_1", at(v1.at("backward: {"), ident("_"))),
					field("v0_0", at(v1.at("backward: {"), strct(
						field("title", at(v1.at(`"untitled"`), ast.NewString("untitled"))),
					))),
				)))))),
			}}
		},
		is: terrors.ErrInvalidLineage,
	},
	"lens-conflict-bind": {
		build: func(t *testing.T, a *archive) *ast.File {
			s := shipFrom(t, a)
			s.fwdCount = at(s.v1.at(`"many"`), ast.NewString("many"))
			return s.lineage()
		},
		is: terrors.ErrInvalidLens,
	},
	"lens-conflict-translate": {
		build:     func(t *testing.T, a *archive) *ast.File { return shipFrom(t, a).lineage() },
		translate: true,
		is:        terrors.ErrInvalidLens,
	},
	"lens-nonconcrete": {
		build:     func(t *testing.T, a *archive) *ast.File { return shipFrom(t, a).lineage() },
		translate: true,
		is:        terrors.ErrInvalidLens,
	},
	"lens-undeclared": {
		build: func(t *testing.T, a *archive) *ast.File {
			s := shipFrom(t, a)
			s.open = true
			s.fwdExtra = []ast.Decl{field("extra", at(s.v1.at("extra: 1"), num("1")))}
			return s.lineage()
		},
		is: terrors.ErrInvalidLens,
	},
	"lens-ref-schema": {
		build: func(t *testing.T, a *archive) *ast.File {
			s := shipFrom(t, a)
			// schemas["1"]["0"].schema.count: the forward lens reads the target
			// schema's count instead of input's.
			p := s.v1.at("to.schema.count")
			s.fwdCount = at(p, &ast.SelectorExpr{
				X: at(p, &ast.SelectorExpr{
					X: at(p, &ast.IndexExpr{
						X:     at(p, &ast.IndexExpr{X: at(p, ident("schemas")), Index: at(p, ast.NewString("1"))}),
						Index: at(p, ast.NewString("0")),
					}),
					Sel: at(p, ident("schema")),
				}),
				Sel: at(p, ident("count")),
			})
			return s.lineage()
		},
		is: terrors.ErrInvalidLens,
	},
	"lacuna-undeclared-path": {
		build: func(t *testing.T, a *archive) *ast.File {
			s := shipFrom(t, a)
			p := s.v1.at("lacuna: {")
			s.fwdLacunas = []ast.Expr{lacunaLit(p,
				field("targetFields", fieldRefs(s.v1.at(`"cuont"`), "cuont")),
				field("message", str(p, "count is a placeholder")),
				field("type", str(p, "Placeholder")),
			)}
			return s.lineage()
		},
		is: terrors.ErrInvalidLens,
	},
	"lacuna-bad-type": {
		build: func(t *testing.T, a *archive) *ast.File {
			s := shipFrom(t, a)
			p := s.v1.at("lacuna: {")
			s.fwdLacunas = []ast.Expr{lacunaLit(p,
				field("targetFields", fieldRefs(p, "count")),
				field("message", str(p, "count is a placeholder")),
				field("type", str(s.v1.at(`"Guess"`), "Guess")),
			)}
			return s.lineage()
		},
		is: terrors.ErrInvalidLineage,
	},
	"lacuna-unknown-field": {
		build: func(t *testing.T, a *archive) *ast.File {
			s := shipFrom(t, a)
			p := s.v1.at("lacuna: {")
			s.fwdLacunas = []ast.Expr{lacunaLit(p,
				field("targetFields", fieldRefs(p, "count")),
				field("message", str(p, "count is a placeholder")),
				field("type", str(p, "Placeholder")),
				field("severity", str(s.v1.at(`"high"`), "high")),
			)}
			return s.lineage()
		},
		is: terrors.ErrInvalidLineage,
	},
}

// TestPipelinePositions runs lineages built as AST, positioned in authoring
// files, through thema's real pipeline (BindLineage, then Translate), and
// records, for each class of failure, the error and where its positions point:
// an authoring file, thema's kernel CUE, the data, or nowhere.
//
// Each archive in testdata/pipeline holds the authoring files (the text only
// supplies positions; the syntax is a placeholder, not a design), data for
// translation cases, and as goldens:
//
//   - out/error: the error as the user sees it, cerrors.Details;
//   - out/positions: the positions it carries as structured data, classified,
//     which is all a tool can map back to source. Positions that appear only in
//     out/error's text are lost to tools.
//
// Each archive's comment says what is surprising.
func TestPipelinePositions(t *testing.T) {
	files, err := filepath.Glob("testdata/pipeline/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(pipelineCases) {
		t.Errorf("%d archives in testdata/pipeline, but %d cases", len(files), len(pipelineCases))
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".txtar")
		t.Run(name, func(t *testing.T) {
			pc, ok := pipelineCases[name]
			if !ok {
				t.Fatalf("no case for %s", file)
			}
			a := readArchive(t, file)
			err := runPipeline(t, a, pc)
			if err == nil {
				t.Fatal("lineage bound and translated without error")
			}
			if !errors.Is(err, pc.is) {
				t.Errorf("error is not %v: %v", pc.is, err)
			}
			a.compare(t, map[string]string{
				"out/error":     cerrors.Details(err, nil),
				"out/positions": positions(err),
			})
		})
	}
}

func runPipeline(t *testing.T, a *archive, pc pipelineCase) error {
	t.Helper()
	ctx := cuecontext.New()
	rt := thema.NewRuntime(ctx)
	v := ctx.BuildFile(pc.build(t, a))
	if v.Err() != nil {
		t.Fatalf("building the normal form failed: %v", v.Err())
	}
	lin, err := thema.BindLineage(v, rt)
	if err != nil || !pc.translate {
		return err
	}
	data := ctx.CompileString(a.file(t, "data.json"), cue.Filename("data.json"))
	inst, err := thema.SchemaP(lin, thema.SV(0, 0)).Validate(data)
	if err != nil {
		t.Fatalf("data.json is not an instance of 0.0: %v", err)
	}
	_, _, err = inst.Translate(thema.SV(1, 0))
	return err
}
