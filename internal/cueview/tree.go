package cueview

import (
	"fmt"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/token"
)

// Tree is a value's Evaluated syntax with every identifier resolved, for
// following references within the value to what they denote.
//
// Because it is built from Evaluated, it depends only on what the value is, not
// on how it was constructed. References that leave the value become generated
// lets inside it (see Evaluated), so they too can be followed; only predeclared
// identifiers (int, string, self, ...) and builtin packages are left unresolved.
// Export the value whose internal references matter: for a lens, the whole lens,
// so that references to its input stay references rather than being inlined.
type Tree struct {
	File   *ast.File
	parent map[ast.Node]ast.Node
}

// NewTree exports v (see Evaluated) and resolves its identifiers.
func NewTree(v cue.Value) (*Tree, error) {
	n, err := Evaluated(v)
	if err != nil {
		return nil, err
	}
	var f *ast.File
	switch x := n.(type) {
	case *ast.File:
		f = x
	case *ast.StructLit:
		f = &ast.File{Decls: x.Elts}
	case ast.Expr:
		f = &ast.File{Decls: []ast.Decl{&ast.EmbedDecl{Expr: x}}}
	default:
		return nil, fmt.Errorf("unexpected export of %T", n)
	}
	var errs []string
	astutil.Resolve(f, func(_ token.Pos, msg string, args ...any) {
		errs = append(errs, fmt.Sprintf(msg, args...))
	})
	if len(errs) > 0 {
		return nil, fmt.Errorf("resolving exported CUE: %s", strings.Join(errs, "; "))
	}
	t := &Tree{File: f, parent: make(map[ast.Node]ast.Node)}
	var stack []ast.Node
	ast.Walk(f, func(n ast.Node) bool {
		if len(stack) > 0 {
			t.parent[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		return true
	}, func(ast.Node) { stack = stack[:len(stack)-1] })
	return t, nil
}

// A Conjunct is one of the expressions a value unifies, found by following
// references. Expr is a struct or list literal, a scalar, an operation other than
// & on values, or a reference that could not be followed further: one to a
// predeclared identifier, or a selection CUE's syntax doesn't let us navigate
// (into _, or into a value only a comprehension produces). Via is the last field
// declaration the references passed through, if any: whatever Expr is, it is
// part of that field's value.
type Conjunct struct {
	Expr ast.Expr
	Via  *ast.Field
}

// Root returns the conjuncts of the whole exported value.
func (t *Tree) Root() []Conjunct {
	if x := soleEmbed(t.File.Decls); x != nil {
		return t.Conjuncts(x)
	}
	return []Conjunct{{Expr: &ast.StructLit{Elts: t.File.Decls}}}
}

// Conjuncts returns the conjuncts x denotes. It looks through parentheses,
// default markers, & and | (taking every disjunct), close(), structs that only
// embed,
// and references within the tree: to fields, including hidden fields and the
// _#def wrapper of a definition; to lets, including the lets CUE generates for
// references leaving the value (R.#x for a definition); through aliases and
// self; and selections into struct and list literals.
func (t *Tree) Conjuncts(x ast.Expr) []Conjunct {
	return t.conjuncts(x, nil, map[ast.Node]bool{})
}

func (t *Tree) conjuncts(x ast.Expr, via *ast.Field, seen map[ast.Node]bool) []Conjunct {
	if seen[x] {
		// A reference cycle: stop at the reference.
		return []Conjunct{{Expr: x, Via: via}}
	}
	seen[x] = true
	defer delete(seen, x)

	if ops, ok := t.operands(x); ok {
		var out []Conjunct
		for _, op := range ops {
			out = append(out, t.conjuncts(op, via, seen)...)
		}
		return out
	}

	switch e := x.(type) {
	case *ast.Ident:
		return t.ident(e, via, seen)
	case *ast.SelectorExpr:
		if name, _, err := ast.LabelName(e.Sel); err == nil {
			return t.selectAll(t.conjuncts(e.X, via, seen), x, func(s ast.Expr) []Conjunct {
				return t.selectField(s, name, seen)
			})
		}
	case *ast.IndexExpr:
		if lit, ok := e.Index.(*ast.BasicLit); ok && lit.Kind == token.INT {
			if i, err := strconv.Atoi(lit.Value); err == nil {
				return t.selectAll(t.conjuncts(e.X, via, seen), x, func(s ast.Expr) []Conjunct {
					return t.selectElem(s, i, seen)
				})
			}
		}
	}
	return []Conjunct{{Expr: x, Via: via}}
}

// operands returns the expressions x unifies, if x is only a wrapper around
// them: parentheses, a default marker, & or | (every disjunct), close(), or a
// struct that only embeds.
func (t *Tree) operands(x ast.Expr) ([]ast.Expr, bool) {
	switch e := x.(type) {
	case *ast.ParenExpr:
		return []ast.Expr{e.X}, true
	case *ast.UnaryExpr:
		if e.Op == token.MUL {
			return []ast.Expr{e.X}, true
		}
	case *ast.BinaryExpr:
		if e.Op == token.AND || e.Op == token.OR {
			return []ast.Expr{e.X, e.Y}, true
		}
	case *ast.StructLit:
		if inner := soleEmbed(e.Elts); inner != nil {
			return []ast.Expr{inner}, true
		}
	case *ast.CallExpr:
		// close() closes its argument, and adds nothing to it. This is the
		// predeclared close, not something in the tree of that name.
		if f, ok := e.Fun.(*ast.Ident); ok && f.Name == "close" && !t.has(f.Node) && len(e.Args) == 1 {
			return e.Args, true
		}
	}
	return nil, false
}

// selectElem returns the conjuncts of element i of the list literal s, if the
// literal says what it is.
func (t *Tree) selectElem(s ast.Expr, i int, seen map[ast.Node]bool) []Conjunct {
	l, ok := s.(*ast.ListLit)
	if !ok || i >= len(l.Elts) {
		return nil
	}
	for _, elt := range l.Elts[:i+1] {
		if _, ok := elt.(*ast.Comprehension); ok {
			// Elements before i may be produced by a comprehension.
			return nil
		}
	}
	return t.conjuncts(l.Elts[i], nil, seen)
}

// selectAll applies sel to each conjunct of a selection's base. Where sel finds
// nothing, the selection x is kept, as part of the base conjunct's field.
func (t *Tree) selectAll(base []Conjunct, x ast.Expr, sel func(ast.Expr) []Conjunct) []Conjunct {
	var out []Conjunct
	for _, b := range base {
		found := sel(b.Expr)
		if len(found) == 0 {
			out = append(out, Conjunct{Expr: x, Via: b.Via})
			continue
		}
		for _, f := range found {
			if f.Via == nil {
				f.Via = b.Via
			}
			out = append(out, f)
		}
	}
	return out
}

// selectField returns the conjuncts of the field name declared in the struct
// literal s, including in its embeddings and comprehensions.
func (t *Tree) selectField(s ast.Expr, name string, seen map[ast.Node]bool) []Conjunct {
	lit, ok := s.(*ast.StructLit)
	if !ok {
		return nil
	}
	var out []Conjunct
	for _, f := range FieldDecls(lit.Elts, func(x ast.Expr) []ast.Expr {
		var exprs []ast.Expr
		for _, c := range t.conjuncts(x, nil, seen) {
			exprs = append(exprs, c.Expr)
		}
		return exprs
	}) {
		if n, _, err := ast.LabelName(f.Field.Label); err == nil && n == name {
			out = append(out, t.conjuncts(f.Field.Value, f.Field, seen)...)
		}
	}
	return out
}

func (t *Tree) ident(id *ast.Ident, via *ast.Field, seen map[ast.Node]bool) []Conjunct {
	if (id.Name == "self" || id.Name == "__self") && !t.has(id.Node) {
		return t.self(id)
	}
	switch n := id.Node.(type) {
	case *ast.LetClause:
		return t.conjuncts(n.Expr, via, seen)
	case *ast.Field:
		// A postfix alias of a field.
		return t.conjuncts(n.Value, n, seen)
	case ast.Expr:
		if fc, ok := t.parent[n].(*ast.ForClause); ok {
			// A comprehension's key or value variable. A value is part of what the
			// comprehension iterates over; a key is a label.
			if fc.Value != n {
				break
			}
			var out []Conjunct
			for _, c := range t.conjuncts(fc.Source, via, seen) {
				out = append(out, Conjunct{Expr: id, Via: c.Via})
			}
			return out
		}
		return t.field(id, n, via, seen)
	}
	return []Conjunct{{Expr: id, Via: via}}
}

// self returns the conjunct the predeclared self denotes: the innermost struct
// or list enclosing it.
func (t *Tree) self(id *ast.Ident) []Conjunct {
	for p := t.parent[id]; p != nil; p = t.parent[p] {
		switch s := p.(type) {
		case *ast.StructLit, *ast.ListLit:
			return []Conjunct{{Expr: s.(ast.Expr), Via: t.fieldOf(s)}}
		case *ast.File:
			return []Conjunct{{Expr: &ast.StructLit{Elts: s.Decls}}}
		}
	}
	return []Conjunct{{Expr: id}}
}

// field returns the conjuncts of the field id refers to: every declaration of
// it in id.Scope, of which value is one.
func (t *Tree) field(id *ast.Ident, value ast.Expr, via *ast.Field, seen map[ast.Node]bool) []Conjunct {
	var out []Conjunct
	for _, f := range scopeFields(id.Scope) {
		if name, _, err := ast.LabelName(f.Label); err == nil && name == id.Name {
			out = append(out, t.conjuncts(f.Value, f, seen)...)
		}
	}
	if len(out) > 0 {
		return out
	}
	return t.conjuncts(value, via, seen)
}

// has reports whether n is part of the tree.
func (t *Tree) has(n ast.Node) bool {
	_, ok := t.parent[n]
	return ok
}

// fieldOf returns the field whose value is x, if any.
func (t *Tree) fieldOf(x ast.Node) *ast.Field {
	if f, ok := t.parent[x].(*ast.Field); ok && f.Value == x {
		return f
	}
	return nil
}

// Within reports whether n is the field f or lies within it.
func (t *Tree) Within(n ast.Node, f *ast.Field) bool {
	for ; n != nil; n = t.parent[n] {
		if n == f {
			return true
		}
	}
	return false
}

func scopeFields(scope ast.Node) []*ast.Field {
	var decls []ast.Decl
	switch s := scope.(type) {
	case *ast.File:
		decls = s.Decls
	case *ast.StructLit:
		decls = s.Elts
	}
	var out []*ast.Field
	for _, d := range decls {
		if f, ok := d.(*ast.Field); ok {
			out = append(out, f)
		}
	}
	return out
}

// A FieldDecl is a field declared in a struct literal, directly or within its
// embeddings and comprehensions.
type FieldDecl struct {
	Field *ast.Field
	// Conditional is true if the declaration is inside a comprehension.
	Conditional bool
}

// FieldDecls returns the fields declared by decls: directly, within
// comprehensions, and within embedded expressions, whose conjuncts embedded
// returns (e.g. Tree.Conjuncts, mapped to their Expr).
func FieldDecls(decls []ast.Decl, embedded func(ast.Expr) []ast.Expr) []FieldDecl {
	var out []FieldDecl
	var walk func(decls []ast.Decl, cond bool)
	walk = func(decls []ast.Decl, cond bool) {
		for _, d := range decls {
			switch x := d.(type) {
			case *ast.Field:
				out = append(out, FieldDecl{Field: x, Conditional: cond})
			case *ast.Comprehension:
				if s, ok := x.Value.(*ast.StructLit); ok {
					walk(s.Elts, true)
				}
			case *ast.EmbedDecl:
				for _, e := range embedded(x.Expr) {
					if s, ok := e.(*ast.StructLit); ok {
						walk(s.Elts, cond)
					}
				}
			}
		}
	}
	walk(decls, false)
	return out
}

// soleEmbed returns the expression embedded by decls, if that is all decls
// holds besides imports and let clauses.
func soleEmbed(decls []ast.Decl) ast.Expr {
	var embed ast.Expr
	for _, d := range decls {
		switch e := d.(type) {
		case *ast.ImportDecl, *ast.LetClause:
		case *ast.EmbedDecl:
			if embed != nil {
				return nil
			}
			embed = e.Expr
		default:
			return nil
		}
	}
	return embed
}
