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
		var x ast.Node = c.Source()
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
