package thema

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/cueview"
)

// What the value cannot show, read from the owner's syntax.
//
// The evaluator leaves one construct out of the value. A pending comprehension
// declares nothing: its condition, its for clause's source and the fields of
// its body are not part of the value until an instance decides it, and
// filling the instance a lens takes with its source schema decides only
// conditions on structure, not a presence test on an optional field or an
// equality on a field with more than one value.
//
// Comprehensions are read from the Raw view (cueview.Raw), the one view that
// keeps every reference as the author wrote it: the Evaluated view inlines a
// reference leaving the owner whenever it can, which for a reference to a
// sibling schema or the lineage's name is always. Raw has no positions and
// resolves nothing, so an identifier it leaves dangling is classified by
// where CUE's lexical scoping would find it: in a struct between the owner
// and the lineage, in the lineage, or outside. A let declared outside the
// owner is inlined by Raw, so its expression is read where it is used; a let
// declared inside is followed to its expression. This is the only place in
// the check that reads syntax; everything else is the value.

// walkSyntax reads every comprehension from the owner's syntax.
func (w *refWalker) walkSyntax() {
	for _, c := range authorConjuncts(w.rc.lin) {
		v := c.LookupPath(w.owner.rel)
		if !v.Exists() {
			continue
		}
		n, err := cueview.Raw(v)
		if err != nil {
			w.add(refUnresolved, "its syntax, which could not be read: "+err.Error(), "", nil, token.NoPos)
			return
		}
		f := asFile(n)
		astutil.Resolve(f, func(token.Pos, string, ...any) {})
		s := &refSyntax{w: w, parents: make(map[ast.Node]ast.Node), lets: make(map[*ast.LetClause]bool)}
		var stack []ast.Node
		ast.Walk(f, func(node ast.Node) bool {
			if len(stack) > 0 {
				s.parents[node] = stack[len(stack)-1]
			}
			stack = append(stack, node)
			return true
		}, func(ast.Node) { stack = stack[:len(stack)-1] })

		ast.Walk(f, func(node ast.Node) bool {
			c, ok := node.(*ast.Comprehension)
			if !ok {
				return true
			}
			s.field = s.pathTo(c)
			s.visit(c)
			return false
		}, nil)
	}
}

// authorConjuncts returns the conjuncts of a lineage value that are not
// thema's own, the author's declarations: for lin: thema.Lineage & {...}, the
// struct; for a lineage declared in several fields, each of them. An owner is
// exported from each of these whole, so that what it declares stays in scope
// of what refers to it (an embedded definition beside its declaration).
func authorConjuncts(v cue.Value) []cue.Value {
	op, args := v.Expr()
	if op != cue.AndOp {
		args = []cue.Value{v}
	}
	var out []cue.Value
	for _, a := range args {
		if kernelPos(a) {
			continue
		}
		if root, p := a.ReferencePath(); len(p.Selectors()) > 0 {
			if bi := root.BuildInstance(); bi != nil && isThemaPackage(bi.ImportPath) {
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

// asFile wraps an exported node as a file, as cueview.Tree does.
func asFile(n ast.Node) *ast.File {
	switch x := n.(type) {
	case *ast.File:
		return x
	case *ast.StructLit:
		return &ast.File{Decls: x.Elts}
	case ast.Expr:
		return &ast.File{Decls: []ast.Decl{&ast.EmbedDecl{Expr: x}}}
	}
	panic(fmt.Sprintf("unreachable - Raw exports a file or an expression, not %T", n))
}

// isPredeclared reports whether CUE predeclares name, by asking the evaluator:
// an identifier it accepts on its own, with nothing in scope, is predeclared.
func (rc *refChecker) isPredeclared(name string) bool {
	ok, seen := rc.predeclared[name]
	if !seen {
		ok = rc.ctx.CompileString(name).Err() == nil
		rc.predeclared[name] = ok
	}
	return ok
}

// refSyntax visits expressions of an owner's syntax, classifying each
// identifier that refers to something.
type refSyntax struct {
	w       *refWalker
	parents map[ast.Node]ast.Node
	// field is the path of labels of the field being visited, for locating a
	// finding; unlocated is the depth below which labels are not constant (a
	// computed label, a let body), where field stops growing.
	field     []string
	unlocated int
	// lets are the let clauses already followed.
	lets map[*ast.LetClause]bool
}

func (s *refSyntax) add(rule refRule, expr, pkg string) {
	s.w.add(rule, expr, pkg, s.field, token.NoPos)
}

// pathTo returns the labels of the fields enclosing n, as far as they are
// constant.
func (s *refSyntax) pathTo(n ast.Node) []string {
	var rev []string
	for p := s.parents[n]; p != nil; p = s.parents[p] {
		if f, ok := p.(*ast.Field); ok {
			name, ok := constLabel(f.Label)
			if !ok {
				rev = rev[:0]
				continue
			}
			rev = append(rev, name)
		}
	}
	out := make([]string, len(rev))
	for i, name := range rev {
		out[len(rev)-1-i] = name
	}
	return out
}

func (s *refSyntax) visit(n ast.Node) {
	switch x := n.(type) {
	case nil:
	case *ast.File:
		for _, d := range x.Decls {
			s.visit(d)
		}
	case *ast.Package, *ast.ImportDecl, *ast.Attribute, *ast.CommentGroup, *ast.BasicLit, *ast.BottomLit:
	case *ast.EmbedDecl:
		s.visit(x.Expr)
	case *ast.Field:
		label, ok := constLabel(x.Label)
		if !ok {
			// A computed label is an expression; the field it builds cannot be
			// located by name.
			s.visitLabel(x.Label)
			s.unlocated++
			s.visit(x.Value)
			s.unlocated--
			return
		}
		if s.unlocated == 0 {
			s.field = append(s.field, label)
		}
		s.visit(x.Value)
		if s.unlocated == 0 {
			s.field = s.field[:len(s.field)-1]
		}
	case *ast.LetClause:
		s.let(x)
	case *ast.Alias:
		s.visit(x.Expr)
	case *ast.Comprehension:
		for _, c := range x.Clauses {
			s.visit(c)
		}
		s.visit(x.Value)
	case *ast.ForClause:
		s.visit(x.Source)
	case *ast.IfClause:
		s.visit(x.Condition)
	case *ast.Ellipsis:
		s.visit(x.Type)
	case *ast.StructLit:
		for _, e := range x.Elts {
			s.visit(e)
		}
	case *ast.ListLit:
		for _, e := range x.Elts {
			s.visit(e)
		}
	case *ast.ParenExpr:
		s.visit(x.X)
	case *ast.Interpolation:
		for _, e := range x.Elts {
			s.visit(e)
		}
	case *ast.SelectorExpr:
		// Only the head of a selection is a reference; Sel is a label.
		s.visit(x.X)
	case *ast.IndexExpr:
		s.visit(x.X)
		s.visit(x.Index)
	case *ast.SliceExpr:
		s.visit(x.X)
		s.visit(x.Low)
		s.visit(x.High)
	case *ast.CallExpr:
		s.visit(x.Fun)
		for _, a := range x.Args {
			s.visit(a)
		}
	case *ast.UnaryExpr:
		s.visit(x.X)
	case *ast.BinaryExpr:
		s.visit(x.X)
		s.visit(x.Y)
	case *ast.PostfixExpr:
		s.visit(x.X)
	case *ast.Ident:
		s.ident(x)
	default:
		// Syntax this walk does not know (a function, a try clause, bad
		// syntax) may refer to anything.
		s.add(refUnresolved, fmt.Sprintf("%T syntax", n), "")
	}
}

// let visits a let's expression, once. A let's body is held by no field.
func (s *refSyntax) let(x *ast.LetClause) {
	if s.lets[x] {
		return
	}
	s.lets[x] = true
	s.unlocated++
	s.visit(x.Expr)
	s.unlocated--
}

// visitLabel visits the expressions in a computed label: an interpolation, a
// parenthesized expression, or a pattern constraint's expression.
func (s *refSyntax) visitLabel(l ast.Label) {
	switch l.(type) {
	case *ast.Ident, *ast.BasicLit:
		// A constant label holds no expression.
	default:
		s.visit(l)
	}
}

// constLabel returns the name of a label an author can look up by name.
func constLabel(l ast.Label) (string, bool) {
	switch x := l.(type) {
	case *ast.Ident:
		return x.Name, true
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			if s, err := literal.Unquote(x.Value); err == nil {
				return s, true
			}
		}
	}
	return "", false
}

// ident classifies one identifier in expression position.
func (s *refSyntax) ident(id *ast.Ident) {
	switch node := id.Node.(type) {
	case nil:
		if s.w.rc.isPredeclared(id.Name) {
			return
		}
		s.dangling(id)
	case *ast.ImportSpec:
		path, err := literal.Unquote(node.Path.Value)
		if err != nil {
			s.add(refUnresolved, s.chainText(id), "")
			return
		}
		// An import path may carry a package qualifier, "path:name".
		path, _, _ = strings.Cut(path, ":")
		if compat.IsBuiltinPackage(path) || isThemaPackage(path) {
			return
		}
		s.add(refImport, s.chainText(id), path)
	case *ast.LetClause:
		// A let declared within the owner: what it refers to is what matters.
		s.let(node)
	default:
		// Declared within the owner: a field, alias or clause of its own.
	}
}

// refLabel is one step of a selection chain after its head: a constant name,
// or a computed one.
type refLabel struct {
	name    string
	dynamic bool
}

// chain returns the outermost selection built on id (id itself if none) and
// the labels selected after id.
func (s *refSyntax) chain(id *ast.Ident) (ast.Expr, []refLabel) {
	var cur ast.Expr = id
	var labels []refLabel
	for {
		switch p := s.parents[cur].(type) {
		case *ast.SelectorExpr:
			if p.X != cur {
				return cur, labels
			}
			switch sel := p.Sel.(type) {
			case *ast.Ident:
				labels = append(labels, refLabel{name: sel.Name})
			case *ast.BasicLit:
				if str, err := literal.Unquote(sel.Value); err == nil {
					labels = append(labels, refLabel{name: str})
				} else {
					labels = append(labels, refLabel{dynamic: true})
				}
			default:
				labels = append(labels, refLabel{dynamic: true})
			}
			cur = p
		case *ast.IndexExpr:
			if p.X != cur {
				return cur, labels
			}
			if lit, ok := p.Index.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if str, err := literal.Unquote(lit.Value); err == nil {
					labels = append(labels, refLabel{name: str})
					cur = p
					continue
				}
			}
			labels = append(labels, refLabel{dynamic: true})
			cur = p
		default:
			return cur, labels
		}
	}
}

func (s *refSyntax) chainText(id *ast.Ident) string {
	x, _ := s.chain(id)
	text, err := cueview.Text(x)
	if err != nil {
		return id.Name
	}
	return text
}

// dangling classifies an identifier that resolves to nothing in the owner's
// syntax. CUE resolves it lexically, innermost scope first: the structs between
// the owner and the lineage, the lineage's own fields, the lineage's own label
// among its siblings, then whatever encloses the lineage.
func (s *refSyntax) dangling(id *ast.Ident) {
	x, labels := s.chain(id)
	text, err := cueview.Text(x)
	if err != nil {
		text = id.Name
	}
	head := id.Name
	lin := s.w.rc.lin

	// The structs between the owner and the lineage: a schema entry (schema,
	// examples, an author's hidden helpers), the schemas and lenses maps (keyed
	// by versions and edges, never by identifiers), then the lineage itself.
	sels := s.w.owner.rel.Selectors()
	for i := len(sels) - 1; i >= 0; i-- {
		anc := lin.LookupPath(cue.MakePath(sels[:i]...))
		if !fieldByName(anc, head).Exists() {
			continue
		}
		if i == 0 {
			s.inLineage(text, append([]refLabel{{name: head}}, labels...))
		} else {
			s.add(refLineageMeta, text, "")
		}
		return
	}

	// The lineage's own label, as a field of the struct that holds it, or its
	// path from the root of the file, in any root it is declared in: the text
	// is the author's, written where the lineage is declared, which need not
	// be where it was bound (placement.go).
	for _, lp := range s.w.rc.placed.paths() {
		lsels := lp.Selectors()
		n := len(lsels)
		if selName(lsels[n-1]) == head {
			s.inLineage(text, labels)
			return
		}
		if n > 1 && selName(lsels[0]) == head && len(labels) >= n-1 {
			full := true
			for i := 1; i < n && full; i++ {
				full = !labels[i-1].dynamic && labels[i-1].name == selName(lsels[i])
			}
			if full {
				s.inLineage(text, labels[n-1:])
				return
			}
		}
	}

	s.add(refOutsideLineage, text, "")
}

// inLineage classifies a reference into the lineage by the labels it selects
// from the lineage, as refWalker.inLineage does by path.
func (s *refSyntax) inLineage(text string, labels []refLabel) {
	if len(labels) > 0 && labels[0].dynamic {
		s.add(refUnresolved, text, "")
		return
	}
	var sels []cue.Selector
	dynamic := false
	for _, l := range labels {
		if l.dynamic {
			dynamic = true
			break
		}
		sels = append(sels, cue.Str(l.name))
	}
	s.w.inLineage(sels, dynamic, text, s.field, token.NoPos)
}
