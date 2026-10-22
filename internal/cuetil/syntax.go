package cuetil

import (
	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
)

// ListBuiltByComprehension reports whether any conjunct of the list value v was
// written as a list literal with a comprehension element (e.g. [if x {...}]).
//
// Such a list's elements depend on evaluation, and a comprehension whose
// condition can't yet be decided may still evaluate to no elements, so this is
// determined from the syntax rather than the value.
func ListBuiltByComprehension(v cue.Value) bool {
	_, conjuncts := v.Expr()
	for _, c := range conjuncts {
		x := c.Source()
		if f, ok := x.(*ast.Field); ok {
			x = f.Value
		}
		if l, ok := x.(*ast.ListLit); ok {
			for _, elt := range l.Elts {
				if _, ok := elt.(*ast.Comprehension); ok {
					return true
				}
			}
		}
	}
	return false
}

// ComprehensionFieldPaths returns the paths, relative to v, of fields that any
// conjunct of the struct value v constructs inside comprehensions, e.g.
// {if x {extra: 1}} yields extra. Such fields may not exist until data makes
// the comprehension fire, so they are found from the syntax rather than the
// value. Fields with dynamic labels are not included.
func ComprehensionFieldPaths(v cue.Value) []cue.Path {
	var paths []cue.Path
	_, conjuncts := v.Expr()
	if len(conjuncts) == 0 {
		conjuncts = []cue.Value{v}
	}
	for _, c := range conjuncts {
		x := c.Source()
		if f, ok := x.(*ast.Field); ok {
			x = f.Value
		}
		if s, ok := x.(*ast.StructLit); ok {
			paths = append(paths, comprehensionFields(s.Elts, nil, false)...)
		}
	}
	return paths
}

func comprehensionFields(elts []ast.Decl, base []cue.Selector, inComprehension bool) []cue.Path {
	var paths []cue.Path
	for _, elt := range elts {
		switch e := elt.(type) {
		case *ast.Field:
			name, isIdent, err := ast.LabelName(e.Label)
			if err != nil || (!isIdent && name == "") {
				continue
			}
			p := append(append([]cue.Selector{}, base...), cue.Str(name))
			if inComprehension {
				paths = append(paths, cue.MakePath(p...))
			}
			if s, ok := e.Value.(*ast.StructLit); ok {
				paths = append(paths, comprehensionFields(s.Elts, p, inComprehension)...)
			}
		case *ast.Comprehension:
			if s, ok := e.Value.(*ast.StructLit); ok {
				paths = append(paths, comprehensionFields(s.Elts, base, true)...)
			}
		case *ast.EmbedDecl:
			if s, ok := e.Expr.(*ast.StructLit); ok {
				paths = append(paths, comprehensionFields(s.Elts, base, inComprehension)...)
			}
		}
	}
	return paths
}
