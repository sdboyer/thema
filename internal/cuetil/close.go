package cuetil

import (
	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
)

var closePath = cue.MakePath(cue.Def("#closed"))

// CloseRecursive returns v recursively closed, as if it had been declared as a
// CUE definition. v itself is unchanged.
//
// thema's CUE takes no position on the openness of the schemas lineage authors
// write. Operations that need closed semantics, such as checking backwards
// compatibility or validating data that may not contain extra fields, apply this
// at the point of use.
func CloseRecursive(ctx *cue.Context, v cue.Value) cue.Value {
	// Built from AST rather than compiled from source, so the wrapper carries no
	// source position that could show up in user-facing errors.
	wrapper := ctx.BuildExpr(ast.NewStruct(&ast.Field{Label: ast.NewIdent("#closed"), Value: ast.NewIdent("_")}))
	return wrapper.FillPath(closePath, v).LookupPath(closePath)
}
