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
	"github.com/sdboyer/thema/internal/util"
)

// What a schema or a lens may refer to. The rules, their IDs and what an
// author does about each are in docs/invariants.md, References.
//
// A schema may refer only to its own body (anything declared inside it, at any
// depth) and to CUE's builtin packages. A lens may refer only to the instance
// it takes (the field named after its from version, v1_0 for a lens from 1.0),
// its own body and the builtin packages. thema's own package is part of the substrate
// (thema.Lacuna, thema.LacunaTypes), not something an author can edit, so it
// is allowed too.
//
// References are read from the owner's syntax, the Raw view, which is the only
// view that keeps every reference as the author wrote it. The evaluator does
// not: it resolves a let to its value, an index expression to the element it
// selects, and leaves the body of a pending comprehension unevaluated, so what
// a value refers to is not recoverable from the value alone. Identifiers in the
// syntax are resolved within it (astutil.Resolve); one that resolves to a
// declaration inside the owner is the owner's own, one that resolves to an
// import names a package, and one that resolves to nothing is a reference that
// leaves the owner, classified by where CUE's lexical scoping finds it: in the
// lineage, or outside it.

// Labels of the lineage fields the reference rules tell apart.
const (
	labelSchemas    = "schemas"
	labelLenses     = "lenses"
	labelJoinSchema = "joinSchema"
)

// refRule is a rule of the reference policy, named as in docs/invariants.md.
type refRule string

const (
	refSiblingSchema  refRule = "ref-sibling-schema"
	refLineageMeta    refRule = "ref-lineage-meta"
	refOutsideLineage refRule = "ref-outside-lineage"
	refImport         refRule = "ref-import"
	refLensToSchema   refRule = "ref-lens-to-schema"
	refLensToLens     refRule = "ref-lens-to-lens"
	refUnresolved     refRule = "ref-unresolved"
)

// refOwner is a value whose references are checked: one schema, the
// joinSchema, or one lens.
type refOwner struct {
	// name is how the owner is referred to in errors: "schema 0.1",
	// "joinSchema", "lens 0.1->0.0".
	name string
	// v is the owner within the raw lineage.
	v cue.Value
	// rel is the owner's path relative to the lineage.
	rel cue.Path
	// lens says whether the owner is a lens; otherwise it is a schema or the
	// joinSchema, which follow the schema rules.
	lens bool
	// takes is the name of the field a lens binds the instance it takes to
	// (cuetil.LensParam), for the lens rule's wording.
	takes string
}

// refFinding is one reference that breaks a rule.
type refFinding struct {
	rule refRule
	// field is the path of labels, within the owner, of the field whose value
	// holds the reference, as far as it can be located: labels built by
	// comprehensions or interpolation, and let bodies, have no field.
	field []string
	// expr is the reference as written.
	expr string
	// pkg is the import path of the package referred to (ref-import).
	pkg string
}

// String renders the finding in the house format:
//
//	<path> <what is wrong> (rule <name>)
func (f refFinding) String(o refOwner) string {
	subject := o.name
	if len(f.field) > 0 {
		subject = o.name + ": " + strings.Join(f.field, ".")
	}
	var what string
	switch f.rule {
	case refSiblingSchema:
		what = "a schema in the lineage"
	case refLensToSchema:
		what = "a schema"
	case refLensToLens:
		what = "another lens"
	case refLineageMeta:
		what = "which is declared in the lineage outside the " + o.kind()
	case refOutsideLineage:
		what = "which is declared outside the lineage"
	case refImport:
		what = fmt.Sprintf("in package %q, which is not one of CUE's builtin packages", f.pkg)
	case refUnresolved:
		return fmt.Sprintf("%s refers to %s, which thema cannot resolve statically; write the reference with constant labels, or declare what it needs inside the %s (rule %s)", subject, f.expr, o.kind(), f.rule)
	}
	return fmt.Sprintf("%s refers to %s, %s; %s (rule %s)", subject, f.expr, what, o.may(), f.rule)
}

func (o refOwner) kind() string {
	switch {
	case o.lens:
		return "lens"
	case o.name == labelJoinSchema:
		return labelJoinSchema
	}
	return "schema"
}

func (o refOwner) may() string {
	if o.lens {
		return "a lens may refer only to " + o.takes + ", the instance it takes, its own body and CUE's builtin packages"
	}
	return "a " + o.kind() + " may refer only to its own body and CUE's builtin packages"
}

// refChecker checks the owners of one lineage.
type refChecker struct {
	ctx *cue.Context
	// lin is the lineage as bound: the author's value, with thema.Lineage
	// unified in where the author did so. Scopes are looked up in it.
	lin cue.Value
	// authored holds the author's own conjuncts of lin: what they wrote,
	// without thema.Lineage. An owner is read from these, so that its syntax
	// holds only the author's references (see check).
	authored []cue.Value
	// predeclared caches which unresolved identifiers CUE predeclares (int,
	// string, len, self, ...), asked of the evaluator.
	predeclared map[string]bool
}

func newRefChecker(ctx *cue.Context, lin cue.Value) *refChecker {
	return &refChecker{ctx: ctx, lin: lin, authored: authorConjuncts(lin), predeclared: make(map[string]bool)}
}

// check returns the references in o that break a rule, in syntax order. An
// owner whose syntax cannot be read is a single unresolved finding.
//
// The owner is read from each of the author's conjuncts of the lineage that
// declares it, not from the lineage as bound: there, thema's own CUE unifies a
// lens with Lens and a schema with _, and exporting the bound value shows
// thema's conjunct as a dangling reference beside the author's. Each author's
// conjunct is one declaration, so what it declares at the owner's path is a
// lexical unit, exported whole: an embedded definition stays in scope of its
// declaration. (Splitting the owner itself into conjuncts would not do: CUE
// reports an embedding as a conjunct of its own, apart from the struct that
// declares what it embeds.)
func (rc *refChecker) check(o refOwner) []refFinding {
	w := &refWalker{rc: rc, owner: o}
	for _, c := range rc.authored {
		v := c.LookupPath(o.rel)
		if !v.Exists() {
			continue
		}
		n, err := cueview.Raw(v)
		if err != nil {
			return []refFinding{{rule: refUnresolved, expr: "its syntax, which could not be read: " + err.Error()}}
		}
		f := asFile(n)
		astutil.Resolve(f, func(token.Pos, string, ...any) {})
		w.parents = make(map[ast.Node]ast.Node)
		var stack []ast.Node
		ast.Walk(f, func(node ast.Node) bool {
			if len(stack) > 0 {
				w.parents[node] = stack[len(stack)-1]
			}
			stack = append(stack, node)
			return true
		}, func(ast.Node) { stack = stack[:len(stack)-1] })
		w.visit(f)
	}
	return w.findings
}

// authorConjuncts returns the conjuncts of a lineage value that are not
// references into thema's package, the author's own declarations: for
// lin: thema.Lineage & {...}, the struct; for a lineage declared in several
// fields, each of them.
func authorConjuncts(v cue.Value) []cue.Value {
	op, args := v.Expr()
	if op != cue.AndOp {
		args = []cue.Value{v}
	}
	var out []cue.Value
	for _, a := range args {
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

// isThemaPackage reports whether an import path names thema's own CUE package,
// with or without its major version suffix.
func isThemaPackage(path string) bool {
	mod, _, _ := strings.Cut(util.ThemaModulePath, "@")
	path, _, _ = strings.Cut(path, "@")
	return path == mod
}

// refWalker visits the expressions of an owner's syntax, classifying each
// identifier that refers to something.
type refWalker struct {
	rc      *refChecker
	owner   refOwner
	parents map[ast.Node]ast.Node
	// field is the path of labels of the field being visited, for locating a
	// finding; unlocated is the depth below which labels are not constant (a
	// computed label, a let body), where field stops growing.
	field     []string
	unlocated int
	findings  []refFinding
}

func (w *refWalker) visit(n ast.Node) {
	switch x := n.(type) {
	case nil:
	case *ast.File:
		for _, d := range x.Decls {
			w.visit(d)
		}
	case *ast.Package, *ast.ImportDecl, *ast.Attribute, *ast.CommentGroup, *ast.BasicLit, *ast.BottomLit:
	case *ast.EmbedDecl:
		w.visit(x.Expr)
	case *ast.Field:
		label, ok := constLabel(x.Label)
		if !ok {
			// A computed label is an expression; the field it builds cannot be
			// located by name.
			w.visitLabel(x.Label)
			w.unlocated++
			w.visit(x.Value)
			w.unlocated--
			return
		}
		if w.unlocated == 0 {
			w.field = append(w.field, label)
		}
		w.visit(x.Value)
		if w.unlocated == 0 {
			w.field = w.field[:len(w.field)-1]
		}
	case *ast.LetClause:
		// A let's body is held by no field.
		w.unlocated++
		w.visit(x.Expr)
		w.unlocated--
	case *ast.Alias:
		w.visit(x.Expr)
	case *ast.Comprehension:
		for _, c := range x.Clauses {
			w.visit(c)
		}
		w.visit(x.Value)
	case *ast.ForClause:
		w.visit(x.Source)
	case *ast.IfClause:
		w.visit(x.Condition)
	case *ast.Ellipsis:
		w.visit(x.Type)
	case *ast.StructLit:
		for _, e := range x.Elts {
			w.visit(e)
		}
	case *ast.ListLit:
		for _, e := range x.Elts {
			w.visit(e)
		}
	case *ast.ParenExpr:
		w.visit(x.X)
	case *ast.Interpolation:
		for _, e := range x.Elts {
			w.visit(e)
		}
	case *ast.SelectorExpr:
		// Only the head of a selection is a reference; Sel is a label.
		w.visit(x.X)
	case *ast.IndexExpr:
		w.visit(x.X)
		w.visit(x.Index)
	case *ast.SliceExpr:
		w.visit(x.X)
		w.visit(x.Low)
		w.visit(x.High)
	case *ast.CallExpr:
		w.visit(x.Fun)
		for _, a := range x.Args {
			w.visit(a)
		}
	case *ast.UnaryExpr:
		w.visit(x.X)
	case *ast.BinaryExpr:
		w.visit(x.X)
		w.visit(x.Y)
	case *ast.PostfixExpr:
		w.visit(x.X)
	case *ast.Ident:
		w.ident(x)
	default:
		// Syntax this walk does not know (a function, a try clause, bad
		// syntax) may refer to anything.
		w.add(refUnresolved, fmt.Sprintf("%T syntax", n), "")
	}
}

// visitLabel visits the expressions in a computed label: an interpolation, a
// parenthesized expression, or a pattern constraint's expression.
func (w *refWalker) visitLabel(l ast.Label) {
	switch l.(type) {
	case *ast.Ident, *ast.BasicLit:
		// A constant label holds no expression.
	default:
		w.visit(l)
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
func (w *refWalker) ident(id *ast.Ident) {
	switch node := id.Node.(type) {
	case nil:
		if w.rc.isPredeclared(id.Name) {
			return
		}
		w.dangling(id)
	case *ast.ImportSpec:
		path, err := literal.Unquote(node.Path.Value)
		if err != nil {
			w.add(refUnresolved, w.chainText(id), "")
			return
		}
		// An import path may carry a package qualifier, "path:name".
		path, _, _ = strings.Cut(path, ":")
		if compat.IsBuiltinPackage(path) || isThemaPackage(path) {
			return
		}
		w.add(refImport, w.chainText(id), path)
	default:
		// Declared within the owner: a field, let, alias or clause of its own.
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
func (w *refWalker) chain(id *ast.Ident) (ast.Expr, []refLabel) {
	var cur ast.Expr = id
	var labels []refLabel
	for {
		switch p := w.parents[cur].(type) {
		case *ast.SelectorExpr:
			if p.X != cur {
				return cur, labels
			}
			switch sel := p.Sel.(type) {
			case *ast.Ident:
				labels = append(labels, refLabel{name: sel.Name})
			case *ast.BasicLit:
				if s, err := literal.Unquote(sel.Value); err == nil {
					labels = append(labels, refLabel{name: s})
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
				if s, err := literal.Unquote(lit.Value); err == nil {
					labels = append(labels, refLabel{name: s})
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

func (w *refWalker) chainText(id *ast.Ident) string {
	x, _ := w.chain(id)
	s, err := cueview.Text(x)
	if err != nil {
		return id.Name
	}
	return s
}

// dangling classifies an identifier that resolves to nothing in the owner's
// syntax. CUE resolves it lexically, innermost scope first: the structs between
// the owner and the lineage, the lineage's own fields, the lineage's own label
// among its siblings, then whatever encloses the lineage.
func (w *refWalker) dangling(id *ast.Ident) {
	x, labels := w.chain(id)
	text, err := cueview.Text(x)
	if err != nil {
		text = id.Name
	}
	head := id.Name

	// The structs between the owner and the lineage: a schema entry (schema,
	// examples, an author's hidden helpers), the schemas and lenses maps (keyed
	// by versions and edges, never by identifiers), then the lineage itself.
	sels := w.owner.rel.Selectors()
	for i := len(sels) - 1; i >= 0; i-- {
		anc := w.rc.lin.LookupPath(cue.MakePath(sels[:i]...))
		if !fieldByName(anc, head).Exists() {
			continue
		}
		if i == 0 {
			w.inLineage(text, append([]refLabel{{name: head}}, labels...))
		} else {
			w.add(refLineageMeta, text, "")
		}
		return
	}

	// The lineage's own label, as a field of the struct that holds it, or its
	// path from the root of the file.
	lsels := w.rc.lin.Path().Selectors()
	if n := len(lsels); n > 0 {
		if selName(lsels[n-1]) == head {
			w.inLineage(text, labels)
			return
		}
		if n > 1 && selName(lsels[0]) == head && len(labels) >= n-1 {
			full := true
			for i := 1; i < n && full; i++ {
				full = !labels[i-1].dynamic && labels[i-1].name == selName(lsels[i])
			}
			if full {
				w.inLineage(text, labels[n-1:])
				return
			}
		}
	}

	w.add(refOutsideLineage, text, "")
}

// inLineage classifies a reference into the lineage by the labels it selects
// from the lineage.
func (w *refWalker) inLineage(text string, labels []refLabel) {
	if len(labels) == 0 {
		w.add(refLineageMeta, text, "")
		return
	}
	if labels[0].dynamic {
		w.add(refUnresolved, text, "")
		return
	}
	switch labels[0].name {
	case labelSchemas:
		// A bare lin.schemas is the lineage's collection of schemas (len(lin.schemas));
		// anything selected from it is a schema.
		switch {
		case len(labels) == 1:
			w.add(refLineageMeta, text, "")
		case w.owner.lens:
			w.add(refLensToSchema, text, "")
		default:
			w.add(refSiblingSchema, text, "")
		}
	case labelLenses:
		if len(labels) > 1 && w.owner.lens {
			w.add(refLensToLens, text, "")
		} else {
			w.add(refLineageMeta, text, "")
		}
	default:
		w.add(refLineageMeta, text, "")
	}
}

func (w *refWalker) add(rule refRule, expr, pkg string) {
	w.findings = append(w.findings, refFinding{
		rule:  rule,
		field: append([]string(nil), w.field...),
		expr:  expr,
		pkg:   pkg,
	})
}

// selName is the name an identifier would have to carry to select sel.
func selName(sel cue.Selector) string {
	if sel.IsString() {
		return sel.Unquoted()
	}
	return sel.String()
}

// fieldByName returns the field of v with the given label as written (a
// regular, optional, required, hidden or definition field), or a value that
// does not exist.
func fieldByName(v cue.Value, name string) cue.Value {
	iter, err := v.Fields(cue.Optional(true), cue.Hidden(true), cue.Definitions(true))
	if err != nil {
		return cue.Value{}
	}
	for iter.Next() {
		if selName(iter.Selector()) == name {
			return iter.Value()
		}
	}
	return cue.Value{}
}

// refPos positions a finding at the author's line: the field holding the
// reference where it exists in the value, else the nearest enclosing field
// that does (a field built by a pending comprehension exists only once data
// arrives), else the owner.
func refPos(o refOwner, field []string) token.Pos {
	for i := len(field); i >= 0; i-- {
		v := o.v
		for _, name := range field[:i] {
			v = fieldByName(v, name)
		}
		if v.Exists() {
			return userPos(v)
		}
	}
	return userPos(o.v)
}
