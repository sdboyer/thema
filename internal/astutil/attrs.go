// Package astutil holds helpers for CUE syntax trees.
package astutil

import (
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
)

// StripAttrs removes every attribute named name from the fields in n, and
// returns how many it removed.
func StripAttrs(n ast.Node, name string) int {
	removed := 0
	astutil.Apply(n, func(c astutil.Cursor) bool {
		if f, ok := c.Node().(*ast.Field); ok {
			attrs := f.Attrs[:0]
			for _, a := range f.Attrs {
				if a.Name() == name {
					removed++
					continue
				}
				attrs = append(attrs, a)
			}
			f.Attrs = attrs
		}
		return true
	}, nil)
	return removed
}
