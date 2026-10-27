package compile

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/cueview"
)

// TestEvaluatorReportsAuthoredPositions is the probe from #64: a field built
// as AST, positioned in a file that isn't CUE and was never parsed, conflicts
// with a schema compiled from CUE text. The evaluator reports the AST's
// position as it would a parsed file's. No mapping table is involved.
func TestEvaluatorReportsAuthoredPositions(t *testing.T) {
	ctx := cuecontext.New()
	text := "ship {\n  firstfield: 42\n}\n"
	f := token.NewFile("ship.lens", 0, len(text))
	f.SetLinesForContent([]byte(text))

	v := ctx.BuildFile(fileOf(field("firstfield", at(f.Pos(strings.Index(text, "42"), token.NoRelPos), num("42")))))
	sch := ctx.CompileString("firstfield: string", cue.Filename("schema.cue"))
	err := v.Unify(sch).Validate()

	var got []string
	for _, p := range cerrors.Positions(err) {
		got = append(got, p.String())
	}
	if want := []string{"schema.cue:1:13", "ship.lens:2:15"}; !slices.Equal(got, want) {
		t.Errorf("positions %v, want %v; error:\n%s", got, want, cerrors.Details(err, nil))
	}
}

// TestPositionsCarrySemantics pins that a node's position decides how the
// evaluator treats it, not only where its errors point.
//
// CUE records a file's language version and experiments on its token.File,
// and some evaluation-time gates read them from each node's position. A
// token.File from token.NewFile has none, and nor does token.NoPos, so a node
// positioned in one, or not at all, is evaluated without the experiments
// stable at the current language version. Here that changes the meaning of ==
// across kinds (structcmp) and whether embedding a definition closes a struct
// (explicitopen). The same CUE parsed as text has the current semantics.
//
// So the compiler must position every node it builds in a token.File that
// carries the current language version: one from parsing the authoring file,
// which is CUE syntax. That rules out both leaving nodes unpositioned and
// positioning them with token.NewFile. (The experiments setters on token.File
// and ast.File take a type from CUE's internal packages.)
func TestPositionsCarrySemantics(t *testing.T) {
	parsed := parseSource(t, "ship/1.0.thema", "schema: {}\n").file
	synthetic := token.NewFile("ship/1.0.thema", 0, 100)
	synthetic.SetLinesForContent(make([]byte, 100))

	cases := []struct {
		src string
		// What x is with the current language version's semantics, and without:
		// its error, or else its value if it is a concrete scalar, or else valid.
		current, legacy string
	}{
		{
			src:     `x: 1 == "a"`,
			current: "false",
			legacy:  `error: x: invalid operands 1 and "a" to '==' (type int and string)`,
		},
		{
			src:     "#D: {a: int}\nx: {#D, b: 1}",
			current: "error: x.b: field not allowed",
			legacy:  "valid",
		},
	}
	for _, c := range cases {
		for _, pos := range []struct {
			name string
			// Positions every node at p, or leaves them as parsed if nil.
			p    func() token.Pos
			want string
		}{
			{"as parsed", nil, c.current},
			{"parsed authoring file", func() token.Pos { return parsed.Pos(1, token.NoRelPos) }, c.current},
			{"token.NewFile", func() token.Pos { return synthetic.Pos(1, token.NoRelPos) }, c.legacy},
			{"token.NoPos", func() token.Pos { return token.NoPos }, c.legacy},
		} {
			t.Run(pos.name, func(t *testing.T) {
				f, err := parser.ParseFile("in.cue", c.src)
				if err != nil {
					t.Fatal(err)
				}
				if pos.p != nil {
					reposition(f, pos.p())
				}
				x := cuecontext.New().BuildFile(f).LookupPath(cue.ParsePath("x"))
				got := "valid"
				if err := x.Validate(); err != nil {
					got = "error: " + err.Error()
				} else if x.IsConcrete() && x.Kind() != cue.StructKind {
					got = fmt.Sprint(x)
				}
				if got != pos.want {
					t.Errorf("%s: x is\n%s\nwant\n%s", c.src, got, pos.want)
				}
			})
		}
	}
}

// reposition sets the position of every node in f to p.
func reposition(f *ast.File, p token.Pos) {
	astutil.Apply(f, func(c astutil.Cursor) bool {
		switch n := c.Node().(type) {
		case *ast.File:
		case *ast.StructLit:
			n.Lbrace, n.Rbrace = p, p
		default:
			ast.SetPos(n, p)
		}
		return true
	}, nil)
}

func mustFormat(t *testing.T, n ast.Node) string {
	t.Helper()
	b, err := format.Node(n)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// TestSetPosOnStructLit pins a trap for a compiler that positions nodes with
// ast.SetPos: on a struct literal with no Lbrace and some elements, it sets
// the first element's position instead of the struct's, and the struct's Pos
// stays derived from that element. cue/format then also prints the struct
// inline (a: b: 1) when it has one field, as an unset Lbrace with a positioned
// label means "collapse". Set Lbrace directly, as at does.
//
// Layout in cue/format output follows each position's RelPos, not its line:
// positions from token.File.Pos(offset, token.NoRelPos) give no line breaks,
// so a struct with a positioned Lbrace prints on one line. Formatting the
// normal form for people to read (#63) means setting RelPos too.
func TestSetPosOnStructLit(t *testing.T) {
	src := parseSource(t, "ship/1.0.thema", "forward: {\n\tcount: 1\n}\n")
	p := src.at("forward: {")

	inner := field("count", num("1"))
	s := strct(inner)
	ast.SetPos(s, p)
	if s.Lbrace.IsValid() {
		t.Errorf("ast.SetPos set the struct's Lbrace")
	}
	if inner.Pos() != p {
		t.Errorf("ast.SetPos did not position the struct's first element: %v", inner.Pos())
	}
	if got, want := mustFormat(t, fileOf(field("result", s))), "result: count: 1"; got != want {
		t.Errorf("formatted as\n%s\nwant\n%s", got, want)
	}

	s = at(p, strct(field("count", num("1"))))
	if s.Lbrace != p {
		t.Errorf("at did not set the struct's Lbrace")
	}
	if got, want := mustFormat(t, fileOf(field("result", s))), "result: {count: 1}"; got != want {
		t.Errorf("formatted as\n%s\nwant\n%s", got, want)
	}
}

// TestUnpositionedNodes compares leaving nodes the compiler generates (here
// implicit copies, r: {a: s.a, b: s.b}) unpositioned with positioning them at
// the nearest authored construct (here the lens they belong to).
//
// The evaluator cites a reference's position alongside the values a conflict
// involves, and an incomplete reference's error has only the reference's
// position. Unpositioned, that position is simply missing: a conflict still
// points at its values, but an optional field read where it may be absent
// points nowhere at all. (An unpositioned node is also evaluated with legacy
// semantics; see TestPositionsCarrySemantics.)
func TestUnpositionedNodes(t *testing.T) {
	src := parseSource(t, "ship/1.0.thema", "schema: {\n\ta?: string\n\tb: bytes\n}\n\nforward: {\n}\n")
	lens := src.at("forward: {")
	schema := cue.Filename("schema.cue")

	for _, c := range []struct {
		name string
		copy token.Pos
		// Positions of each error, rendered.
		incomplete, conflict []string
	}{
		{
			name:       "unpositioned",
			copy:       token.NoPos,
			incomplete: nil,
			conflict:   []string{"schema.cue:1:7", "ship/1.0.thema:3:5"},
		},
		{
			name:       "at the lens",
			copy:       lens,
			incomplete: []string{"ship/1.0.thema:6:1"},
			conflict:   []string{"schema.cue:1:7", "ship/1.0.thema:3:5", "ship/1.0.thema:6:1"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := cuecontext.New()
			v := ctx.BuildFile(fileOf(
				field("s", at(src.at("schema: {"), strct(optional("a", at(src.at("string"), ident("string"))), field("b", at(src.at("bytes"), ident("bytes")))))),
				field("r", at(c.copy, strct(field("a", sel(c.copy, "s", "a")), field("b", sel(c.copy, "s", "b"))))),
			))

			// s.a is optional, so copying it is incomplete.
			err := v.LookupPath(cue.ParsePath("r.a")).Validate(cue.Concrete(true))
			if !strings.Contains(err.Error(), "cannot reference optional field") {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := posStrings(err); !slices.Equal(got, c.incomplete) {
				t.Errorf("incomplete copy reported at %v, want %v", got, c.incomplete)
			}

			// Against a target that wants b to be an int, the copy of s.b conflicts.
			err = v.Unify(ctx.CompileString("r: b: int", schema)).LookupPath(cue.ParsePath("r.b")).Validate()
			if !strings.Contains(err.Error(), "conflicting values") {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := posStrings(err); !slices.Equal(got, c.conflict) {
				t.Errorf("conflicting copy reported at %v, want %v", got, c.conflict)
			}
		})
	}
}

func posStrings(err error) []string {
	var out []string
	for _, p := range cerrors.Positions(err) {
		out = append(out, p.String())
	}
	return out
}

// TestUnpositionedResultFallsToKernel shows what BindLineage reports when a
// lens result has no position. thema's Go checks position their errors at the
// first conjunct of a value not in thema's own CUE (bind.go userPos), and
// recognize thema's CUE by the embedded module's root, embedreg.Root(). But
// when BindLineage unifies the lineage with thema.Lineage itself, as it does
// for a normal form the compiler builds without importing thema, thema's CUE
// comes from the Runtime, loaded at a path without the module version. So with
// no authored position to find, the error points into thema's lineage.cue,
// at Lens's result field, as if the author had written it.
//
// The lens-conflict-bind pipeline case is the same lineage with the result
// positioned at the lens.
func TestUnpositionedResultFallsToKernel(t *testing.T) {
	a := readArchive(t, "testdata/pipeline/lens-conflict-bind.txtar")
	s := shipFrom(t, a)
	s.fwdCount = at(s.v1.at(`"many"`), ast.NewString("many"))
	s.unpositioned = true

	ctx := cuecontext.New()
	_, err := thema.BindLineage(ctx.BuildFile(s.lineage()), thema.NewRuntime(ctx))
	if err == nil {
		t.Fatal("lineage bound")
	}
	errs := cerrors.Errors(err)
	if len(errs) != 1 {
		t.Fatalf("want one error, got %d: %v", len(errs), err)
	}
	p := errs[0].Position()
	if classify(p) != whereKernel || !strings.HasSuffix(p.Filename(), "lineage.cue") {
		t.Errorf("error positioned at %v, want thema's lineage.cue", p)
	}
}

// TestSeparatelyBuiltFiles builds each version's part of the normal form from
// its own ast.File, with its own BuildFile call, and assembles the lineage from
// the resulting values with FillPath. Positions survive: the error and its
// positions are exactly those of the same lineage built as one ast.File (the
// lens-conflict-bind pipeline case).
func TestSeparatelyBuiltFiles(t *testing.T) {
	a := readArchive(t, "testdata/pipeline/lens-conflict-bind.txtar")
	s := shipFrom(t, a)
	s.fwdCount = at(s.v1.at(`"many"`), ast.NewString("many"))
	merged := s.lineage()

	ctx := cuecontext.New()
	rt := thema.NewRuntime(ctx)
	_, mergedErr := thema.BindLineage(ctx.BuildFile(merged), rt)

	// Split the merged AST into one file per version, plus a skeleton with _
	// where each version's parts go.
	// entry returns the value at a label path in the merged AST, descending
	// through keyed structs.
	entry := func(labels ...string) ast.Expr {
		decls := merged.Decls
		var v ast.Expr
		for _, label := range labels {
			v = nil
			for _, d := range decls {
				if f, ok := d.(*ast.Field); ok {
					if name, _, _ := ast.LabelName(f.Label); name == label {
						v = f.Value
					}
				}
			}
			if v == nil {
				t.Fatalf("no %s", strings.Join(labels, "."))
			}
			if s, ok := v.(*ast.StructLit); ok {
				decls = s.Elts
			}
		}
		return v
	}
	s0, s1 := entry("schemas", "0", "0"), entry("schemas", "1", "0")
	bwd, fwd := entry("lenses", "1.0->0.0"), entry("lenses", "0.0->1.0")
	v0 := ctx.BuildFile(fileOf(field("schema", s0)))
	v1 := ctx.BuildFile(fileOf(field("schema", s1), field("backward", bwd), field("forward", fwd)))
	skel := ctx.BuildFile(fileOf(
		merged.Decls[0],
		field("schemas", strct(key("0", strct(key("0", ident("_")))), key("1", strct(key("0", ident("_")))))),
		field("lenses", strct(key("1.0->0.0", ident("_")), key("0.0->1.0", ident("_")))),
	))
	lin := skel.
		FillPath(cue.MakePath(cue.Str("schemas"), cue.Str("0"), cue.Str("0")), v0.LookupPath(cue.ParsePath("schema"))).
		FillPath(cue.MakePath(cue.Str("schemas"), cue.Str("1"), cue.Str("0")), v1.LookupPath(cue.ParsePath("schema"))).
		FillPath(cue.MakePath(cue.Str("lenses"), cue.Str("1.0->0.0")), v1.LookupPath(cue.ParsePath("backward"))).
		FillPath(cue.MakePath(cue.Str("lenses"), cue.Str("0.0->1.0")), v1.LookupPath(cue.ParsePath("forward")))
	_, splitErr := thema.BindLineage(lin, rt)

	if mergedErr == nil || splitErr == nil {
		t.Fatalf("lineage bound: merged %v, split %v", mergedErr, splitErr)
	}
	if got, want := cerrors.Details(splitErr, nil), cerrors.Details(mergedErr, nil); got != want {
		t.Errorf("separately built error:\n%s\nwant, as merged:\n%s", got, want)
	}
	if got, want := positions(splitErr), positions(mergedErr); got != want {
		t.Errorf("separately built positions:\n%s\nwant, as merged:\n%s", got, want)
	}
}

// TestCrossFileReferences shows that a reference from one authoring file to a
// name declared in another (here 1.0 reusing a definition from 0.0) resolves
// only if both files are compiled in one scope. Built separately, the
// reference is an error. Added to one build.Instance as files of one package,
// it resolves, and a conflict through it cites both files.
func TestCrossFileReferences(t *testing.T) {
	v0 := parseSource(t, "ship/history/0.0.thema", "#Title: string\n")
	v1 := parseSource(t, "ship/1.0.thema", "schema: {\n\ttitle: #Title & 1\n}\n")
	pkg := func(s *source) ast.Decl { return at(s.header(), &ast.Package{Name: ident("ship")}) }
	f0 := fileOf(pkg(v0), field("#Title", at(v0.at("string"), ident("string"))))
	f1 := fileOf(pkg(v1), field("schema", at(v1.at("schema: {"), strct(field("title", &ast.BinaryExpr{
		X:  at(v1.at("#Title"), ident("#Title")),
		Op: token.AND,
		Y:  at(v1.at("1"), num("1")),
	})))))

	ctx := cuecontext.New()
	if err := ctx.BuildFile(f1).Err(); err == nil || !strings.Contains(err.Error(), `reference "#Title" not found`) {
		t.Errorf("built alone, 1.0's reference to 0.0 did not fail as unresolved: %v", err)
	}

	inst := build.NewContext().NewInstance("ship", nil)
	for _, f := range []*ast.File{f0, f1} {
		if err := inst.AddSyntax(f); err != nil {
			t.Fatal(err)
		}
	}
	err := ctx.BuildInstance(inst).Validate()
	if got, want := posStrings(err), []string{"ship/1.0.thema:2:9", "ship/1.0.thema:2:18", "ship/history/0.0.thema:1:9"}; !slices.Equal(got, want) {
		t.Errorf("conflict across files reported at %v, want %v; error:\n%s", got, want, cerrors.Details(err, nil))
	}
}

// TestCommentsSurviveFormat shows that comments attached to the normal form's
// AST survive into cue/format output, whether or not they are positioned, and
// also through evaluation: the evaluated value's syntax, with docs, has the
// doc comments. Line comments do not survive evaluation.
func TestCommentsSurviveFormat(t *testing.T) {
	src := parseSource(t, "ship/1.0.thema", "// A ship.\nschema: {\n\t// Its name.\n\ttitle: string // never empty\n}\n")
	title := field("title", at(src.at("string"), ident("string")))
	ast.AddComment(title, &ast.CommentGroup{Doc: true, List: []*ast.Comment{{Slash: src.at("// Its"), Text: "// Its name."}}})
	ast.AddComment(title, &ast.CommentGroup{Line: true, Position: 4, List: []*ast.Comment{{Slash: src.at("// never"), Text: "// never empty"}}})
	// A comment the compiler generates, with no source.
	count := field("count", ident("int"))
	ast.AddComment(count, &ast.CommentGroup{Doc: true, List: []*ast.Comment{{Text: "// Added in 1.0."}}})
	schema := field("schema", at(src.at("schema: {"), strct(title, count)))
	ast.AddComment(schema, &ast.CommentGroup{Doc: true, List: []*ast.Comment{{Slash: src.at("// A ship"), Text: "// A ship."}}})
	f := fileOf(schema)

	wantAST := `// A ship.
schema: {
	// Its name.
	title: string // never empty
	// Added in 1.0.
	count: int
}`
	if got := mustFormat(t, f); got != wantAST {
		t.Errorf("formatted AST:\n%s\nwant\n%s", got, wantAST)
	}

	wantEval := `{
	// A ship.
	schema: {
		// Its name.
		title: string
		// Added in 1.0.
		count: int
	}
}`
	if got := mustFormat(t, cuecontext.New().BuildFile(f).Syntax(cue.Docs(true))); got != wantEval {
		t.Errorf("formatted evaluated value:\n%s\nwant\n%s", got, wantEval)
	}
}

// TestViewsOfAuthoredValues pins what thema's syntax views, which its Go-side
// lens checks are built on, keep of authored positions.
//
// cueview.Written returns the AST the value was built from, so its nodes keep
// their authored positions. cueview.Tree (and so cuetil.ParseLens) is built
// from the value's exported syntax, which carries no positions at all, however
// the value was built: a check that finds something in the Tree can't point
// at it from there, and must look up the cue.Value at the same path instead.
func TestViewsOfAuthoredValues(t *testing.T) {
	src := parseSource(t, "ship/1.0.thema", "forward: {\n\tcount: from.count\n}\n")
	ctx := cuecontext.New()
	built := ctx.BuildFile(fileOf(field("lens", at(src.at("forward: {"), strct(
		field("v0_0", at(src.at("forward: {"), ident("_"))),
		field("v1_0", at(src.at("forward: {"), strct(field("count", sel(src.at("from.count"), "v0_0", "count"))))),
	)))))
	parsed := ctx.CompileString("lens: {\n\tv0_0: _\n\tv1_0: count: v0_0.count\n}\n", cue.Filename("lens.cue"))

	lens := built.LookupPath(cue.ParsePath("lens"))
	var written []string
	for _, n := range cueview.Written(lens.LookupPath(cue.ParsePath("v1_0.count"))) {
		if n != nil {
			written = append(written, n.Pos().String())
		}
	}
	if want := []string{"ship/1.0.thema:2:9"}; !slices.Equal(written, want) {
		t.Errorf("Written positions %v, want %v", written, want)
	}

	for name, v := range map[string]cue.Value{"built": lens, "parsed": parsed.LookupPath(cue.ParsePath("lens"))} {
		tree, err := cueview.NewTree(v)
		if err != nil {
			t.Fatal(err)
		}
		ast.Walk(tree.File, func(n ast.Node) bool {
			if n.Pos().IsValid() {
				t.Errorf("%s: Tree node %T has position %v", name, n, n.Pos())
			}
			return true
		}, nil)
	}
}
