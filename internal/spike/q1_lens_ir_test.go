// Spike for #83 question 1 (and 6): does the lens syntax, as cuetil.ParseLens
// sees it, carry what a lens IR needs? Throwaway: never merged.
package spike

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/load"
	"cuelang.org/go/cue/token"
	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/exemplars"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/cueview"
	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/util"
)

type src struct {
	name string
	lin  thema.Lineage
	root cue.Value // value BindLineage was given
	err  error
}

func corpus(t *testing.T) []src {
	t.Helper()
	ctx := cuecontext.New()
	rt := thema.NewRuntime(ctx)
	var out []src
	for name, lin := range exemplars.All(rt) {
		out = append(out, src{name: "exemplars/" + name, lin: lin, root: lin.Underlying()})
	}
	for _, glob := range []string{"../../testdata/cases/lens-*.txtar", "../../testdata/known/lens-*.txtar", "../../testdata/lineage/*.txtar", "../../testdata/known/ext-lens-*.txtar"} {
		files, _ := filepath.Glob(glob)
		for _, f := range files {
			ar, err := txtar.ParseFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var in string
			for _, af := range ar.Files {
				if af.Name == "in.cue" {
					in = string(af.Data)
				}
			}
			if in == "" {
				continue
			}
			if !strings.Contains(in, "package ") {
				in = "package m\n" + in
			}
			cfg := &load.Config{Dir: "/m", Overlay: map[string]load.Source{
				"/m/cue.mod/module.cue": load.FromString(util.ModuleFile("example.com/m")),
				"/m/in.cue":             load.FromString(in),
			}}
			if err := embedreg.Configure(cfg, thema.CueJointFS); err != nil {
				t.Fatal(err)
			}
			bi := load.Instances([]string{"."}, cfg)[0]
			if bi.Err != nil {
				out = append(out, src{name: f, err: bi.Err})
				continue
			}
			v := ctx.BuildInstance(bi)
			if lv := v.LookupPath(cue.ParsePath("lin")); lv.Exists() {
				v = lv
			}
			s := src{name: strings.TrimPrefix(f, "../../testdata/"), root: v}
			func() {
				defer func() {
					if r := recover(); r != nil {
						s.err = fmt.Errorf("panic: %v", r)
					}
				}()
				s.lin, s.err = thema.BindLineage(v, rt)
			}()
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// ---- prototype lowering ----------------------------------------------------

type lower struct {
	t      *cueview.Tree
	inputs []*ast.Field
	w      *strings.Builder
	// tallies
	copies, consts, computed, guards, unknownGuards, unknownComputed int
}

func (l *lower) fromInput(c cueview.Conjunct) bool {
	for _, in := range l.inputs {
		if c.Via != nil && l.t.Within(c.Via, in) {
			return true
		}
	}
	return false
}

func fmtNode(n ast.Node) string {
	b, err := format.Node(n, format.Simplify())
	if err != nil {
		return fmt.Sprintf("<%T>", n)
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

// inputPath reconstructs the path a reference into input selects, e.g.
// input.a.b -> a.b. ok is false when x is not a selector chain rooted at an
// identifier (that identifier having resolved into input is c.Via's job).
func inputPath(x ast.Expr) (string, bool) {
	var parts []string
	for {
		switch e := x.(type) {
		case *ast.SelectorExpr:
			name, _, err := ast.LabelName(e.Sel)
			if err != nil {
				return "", false
			}
			parts = append([]string{name}, parts...)
			x = e.X
		case *ast.IndexExpr:
			parts = append([]string{"[" + fmtNode(e.Index) + "]"}, parts...)
			x = e.X
		case *ast.ParenExpr:
			x = e.X
		case *ast.Ident:
			return strings.Join(parts, "."), true
		default:
			return "", false
		}
	}
}

// guard classifies a comprehension condition the way #82 wants: presence,
// equality or type test on a declared path, or a boolean combination.
func (l *lower) guard(x ast.Expr) string {
	switch e := x.(type) {
	case *ast.ParenExpr:
		return l.guard(e.X)
	case *ast.BinaryExpr:
		switch e.Op {
		case token.LAND, token.LOR:
			return fmt.Sprintf("(%s %s %s)", l.guard(e.X), e.Op, l.guard(e.Y))
		case token.NEQ, token.EQL:
			if _, bot := e.Y.(*ast.BottomLit); bot {
				// presence or type test
				inner := e.X
				if p, ok := inner.(*ast.ParenExpr); ok {
					inner = p.X
				}
				if be, ok := inner.(*ast.BinaryExpr); ok && be.Op == token.AND {
					if p, ok := inputPath(be.X); ok {
						return fmt.Sprintf("typetest(%s is %s)%s", p, fmtNode(be.Y), neg(e.Op == token.EQL))
					}
				}
				if p, ok := inputPath(e.X); ok {
					return fmt.Sprintf("present(%s)%s", p, neg(e.Op == token.EQL))
				}
			}
			if p, ok := inputPath(e.X); ok {
				if lit, ok := e.Y.(*ast.BasicLit); ok {
					return fmt.Sprintf("eq(%s, %s)%s", p, lit.Value, neg(e.Op == token.NEQ))
				}
			}
		}
	}
	l.unknownGuards++
	return "UNKNOWN-GUARD<" + fmtNode(x) + ">"
}

func neg(b bool) string {
	if b {
		return " NEGATED"
	}
	return ""
}

// operands lists what a computed expression is built from: input paths, ops,
// builtin calls, literals.
func (l *lower) operands(x ast.Expr) (ops []string, ok bool) {
	ok = true
	ast.Walk(x, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.SelectorExpr, *ast.IndexExpr:
			if p, isPath := inputPath(e.(ast.Expr)); isPath {
				// resolve: does the root ident reach input?
				root := rootIdent(e.(ast.Expr))
				if root != nil && root.Node != nil && l.identIsInput(root) {
					ops = append(ops, "in:"+p)
					return false
				}
				if root != nil && root.Name == "strings" || isPkgSel(e) {
					return true
				}
				ops = append(ops, "ref:"+p)
				return false
			}
		case *ast.BinaryExpr:
			ops = append(ops, "op:"+e.Op.String())
		case *ast.UnaryExpr:
			ops = append(ops, "unop:"+e.Op.String())
		case *ast.Interpolation:
			ops = append(ops, "interp")
		case *ast.CallExpr:
			ops = append(ops, "call:"+fmtNode(e.Fun))
		case *ast.BasicLit:
			ops = append(ops, "lit:"+e.Value)
		case *ast.Comprehension:
			ops = append(ops, "COMPREHENSION")
			ok = false
			return false
		}
		return true
	}, nil)
	return ops, ok
}

func isPkgSel(e ast.Node) bool {
	s, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := s.X.(*ast.Ident)
	if !ok {
		return false
	}
	_, imp := id.Node.(*ast.ImportSpec)
	return imp || id.Node == nil && id.Name == "strings"
}

func rootIdent(x ast.Expr) *ast.Ident {
	for {
		switch e := x.(type) {
		case *ast.SelectorExpr:
			x = e.X
		case *ast.IndexExpr:
			x = e.X
		case *ast.ParenExpr:
			x = e.X
		case *ast.Ident:
			return e
		default:
			return nil
		}
	}
}

func (l *lower) identIsInput(id *ast.Ident) bool {
	for _, c := range l.t.Conjuncts(id) {
		if l.fromInput(c) {
			return true
		}
	}
	return false
}

func (l *lower) walk(x ast.Expr, path string, guards []string, depth int) {
	ind := strings.Repeat("  ", depth)
	g := ""
	if len(guards) > 0 {
		g = " under " + strings.Join(guards, " && ")
	}
	for _, c := range l.t.Conjuncts(x) {
		switch {
		case l.fromInput(c):
			p, ok := inputPath(c.Expr)
			if !ok {
				p = "??" + fmtNode(c.Expr)
			}
			l.copies++
			fmt.Fprintf(l.w, "%sCOPY %s <- input.%s%s\n", ind, path, p, g)
		default:
			switch e := c.Expr.(type) {
			case *ast.StructLit:
				fmt.Fprintf(l.w, "%sSTRUCT %s%s\n", ind, path, g)
				l.decls(e.Elts, path, guards, depth+1)
			case *ast.BasicLit:
				l.consts++
				fmt.Fprintf(l.w, "%sCONST %s = %s%s\n", ind, path, e.Value, g)
			case *ast.ListLit:
				fmt.Fprintf(l.w, "%sLIST %s (%d elts)%s\n", ind, path, len(e.Elts), g)
				for i, elt := range e.Elts {
					if ex, ok := elt.(ast.Expr); ok {
						l.walk(ex, fmt.Sprintf("%s[%d]", path, i), guards, depth+1)
					} else {
						fmt.Fprintf(l.w, "%s  COMPREHENSION-ELT %s\n", ind, fmtNode(elt))
					}
				}
			case *ast.Ident:
				if e.Node == nil { // predeclared: _, int, string...
					fmt.Fprintf(l.w, "%sTYPE %s : %s%s\n", ind, path, e.Name, g)
				} else {
					fmt.Fprintf(l.w, "%sREF? %s = %s (%T)%s\n", ind, path, e.Name, e.Node, g)
				}
			default:
				ops, ok := l.operands(c.Expr)
				l.computed++
				if !ok {
					l.unknownComputed++
				}
				fmt.Fprintf(l.w, "%sCOMPUTED %s = %s  ops=%v%s\n", ind, path, fmtNode(c.Expr), ops, g)
			}
		}
	}
}

func (l *lower) decls(decls []ast.Decl, path string, guards []string, depth int) {
	for _, d := range decls {
		switch e := d.(type) {
		case *ast.Field:
			name, _, err := ast.LabelName(e.Label)
			if err != nil {
				fmt.Fprintf(l.w, "%sDYNAMIC-LABEL %s\n", strings.Repeat("  ", depth), fmtNode(e.Label))
				continue
			}
			child := name
			if path != "" {
				child = path + "." + name
			}
			l.walk(e.Value, child, guards, depth)
		case *ast.Comprehension:
			gs := append([]string{}, guards...)
			for _, cl := range e.Clauses {
				switch c := cl.(type) {
				case *ast.IfClause:
					l.guards++
					gs = append(gs, l.guard(c.Condition))
				case *ast.ForClause:
					gs = append(gs, "EACH<"+fmtNode(c.Source)+">")
				case *ast.LetClause:
					gs = append(gs, "LET<"+fmtNode(c)+">")
				}
			}
			l.walk(e.Value, path, gs, depth)
		case *ast.EmbedDecl:
			l.walk(e.Expr, path, guards, depth)
		case *ast.LetClause:
			// lets are followed through references; nothing to emit
		default:
			fmt.Fprintf(l.w, "%sDECL %T %s\n", strings.Repeat("  ", depth), d, fmtNode(d))
		}
	}
}

func TestQ1LensIR(t *testing.T) {
	var report strings.Builder
	tot := &lower{}
	for _, s := range corpus(t) {
		fmt.Fprintf(&report, "\n==== %s\n", s.name)
		if s.err != nil {
			fmt.Fprintf(&report, "  BIND FAIL: %s\n", firstLine(s.err.Error()))
			continue
		}
		lenses := s.lin.Underlying().LookupPath(cue.MakePath(cue.Str("lenses")))
		iter, _ := lenses.Fields()
		for iter.Next() {
			lens := iter.Value()
			fmt.Fprintf(&report, "-- lens %s\n", iter.Selector().Unquoted())
			syn, err := cuetil.ParseLens(lens)
			if err != nil {
				fmt.Fprintf(&report, "  ParseLens: %v\n", err)
				continue
			}
			fmt.Fprintf(&report, "  ParseLens origins: %s\n", describe(syn.Result(), ""))
			tree, err := cueview.NewTree(lens)
			if err != nil {
				fmt.Fprintf(&report, "  NewTree: %v\n", err)
				continue
			}
			l := &lower{t: tree, w: &report}
			var result, lacunas ast.Expr
			for _, c := range tree.Root() {
				sl, ok := c.Expr.(*ast.StructLit)
				if !ok {
					continue
				}
				for _, fd := range cueview.FieldDecls(sl.Elts, func(x ast.Expr) []ast.Expr {
					var o []ast.Expr
					for _, c := range tree.Conjuncts(x) {
						o = append(o, c.Expr)
					}
					return o
				}) {
					name, _, _ := ast.LabelName(fd.Field.Label)
					switch name {
					case "input":
						l.inputs = append(l.inputs, fd.Field)
					case "result":
						result = fd.Field.Value
					case "lacunas":
						lacunas = fd.Field.Value
					}
				}
			}
			if result != nil {
				l.walk(result, "", nil, 1)
			}
			if lacunas != nil {
				fmt.Fprintf(&report, "  lacunas:\n")
				l.walk(lacunas, "lacunas", nil, 2)
			}
			tot.copies += l.copies
			tot.consts += l.consts
			tot.computed += l.computed
			tot.guards += l.guards
			tot.unknownGuards += l.unknownGuards
			tot.unknownComputed += l.unknownComputed
		}
	}
	fmt.Fprintf(&report, "\n==== TOTALS copies=%d consts=%d computed=%d (unknown %d) guards=%d (unknown %d)\n",
		tot.copies, tot.consts, tot.computed, tot.unknownComputed, tot.guards, tot.unknownGuards)
	os.WriteFile("q1_report.txt", []byte(report.String()), 0o644)
	t.Log("wrote q1_report.txt")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func describe(f *cuetil.LensField, ind string) string {
	if f == nil {
		return "<nil>"
	}
	// LensField's children are unexported; print via %+v is useless. Use
	// reflection-free summary: origin only at top. (Children shown by walk.)
	return f.Origin.String()
}

// Q6: what does a bound lineage know about its CUE module?
func TestQ6ModulePath(t *testing.T) {
	var report strings.Builder
	for _, s := range corpus(t) {
		if s.err != nil {
			continue
		}
		v := s.root
		bi := v.BuildInstance()
		mod, id, pkg, dir := "<nil>", "<nil>", "", ""
		if bi != nil {
			mod, id, pkg, dir = bi.Module, bi.ID(), bi.PkgName, bi.Dir
		}
		pos := v.Pos()
		fmt.Fprintf(&report, "%-45s name=%-16s module=%-40s id=%-45s pkg=%-6s dir=%s pos=%s path=%s\n",
			s.name, s.lin.Name(), mod, id, pkg, dir, pos.Filename(), v.Path())
		// Underlying of the bound lineage: does it keep the build instance?
		if ub := s.lin.Underlying().BuildInstance(); ub == nil {
			fmt.Fprintf(&report, "%-45s   (lin.Underlying().BuildInstance() == nil)\n", "")
		}
	}
	os.WriteFile("q6_report.txt", []byte(report.String()), 0o644)
	t.Log("wrote q6_report.txt")
}
