// Package cueview turns a cue.Value into syntax or text in a small set of named
// ways ("views"), each documented with what it shows, what it hides, and when to
// use it.
//
// No single serialization of a cue.Value shows everything: Source, Syntax under
// each set of options, and Expr each hide something, and each has quirks that
// surprise. The views here exist so that code picks one deliberately, by name,
// rather than re-deriving a set of options at each call site.
//
// What each view actually produces is pinned by the golden files in testdata,
// one per topic (definitions, references and lets, aliases, hidden fields,
// comprehensions, output shape), each annotated with what is surprising. When a
// CUE upgrade changes a view, those files show exactly how. When you find a new
// quirk, add a case there before relying on it.
//
// Tree builds on Evaluated to follow references within a value (lets, aliases,
// self, fields, the lets CUE generates for references leaving the value), for
// questions about where a value's contents come from that must not depend on how
// the value was constructed. Its behavior is pinned by tree_test.go.
//
// Value.Expr is not a view: it splits a value into an operator and operands, not
// into syntax, and is the tool for walking a value's expression structure. Note
// that it splits builtin calls into their arguments, e.g. close({a: int}) yields
// a conjunct with no source and the struct argument.
package cueview

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
)

// Written returns the syntax each conjunct of v was written as, in the order of
// v.Expr()'s operands, with source positions. It shows how v was constructed,
// not what it is:
//
//   - A conjunct built in Go (Encode, FillPath of a Go value) has no source: its
//     entry is nil.
//   - Where v's contents come from elsewhere (r: _R, r: {_R}, a let), the entry is
//     only the reference. Expr() splits a reference into two operands: a nil one,
//     then the reference.
//   - For a single conjunct, the entry is the whole *ast.Field, label and doc
//     comment included, not just its value.
//
// Use it to point errors at the author's text. Never decide anything about v's
// contents from it: two values that are equal can have different Written views.
func Written(v cue.Value) []ast.Node {
	_, args := v.Expr()
	out := make([]ast.Node, len(args))
	for i, a := range args {
		out[i] = a.Source()
	}
	return out
}

// Evaluated returns v as evaluated, without taking defaults or requiring
// concreteness: v.Syntax() with no options. Definitions, hidden, optional and
// required fields, pattern constraints, defaults (*"a" | "b"), close() and ...
// are all shown. It depends only on what v is, not on how v was constructed.
//
// Use it to inspect the structure of v, e.g. to find comprehensions that have
// not fired. Its quirks:
//
//   - References that leave v are inlined if concrete (r: ext becomes r: 5);
//     otherwise they are bound in a let generated at the end, named after the
//     referenced path in upper case (INPUT) and marked by a doc comment
//     "//cue:path: <path>". That comment is the only way to tell a generated let
//     from one the author wrote.
//   - References within v are kept as written: sibling and hidden fields (x: _h),
//     aliases (x~(X)), the author's lets, self.
//   - A recursively closed v (a definition, or x: #D) is wrapped as
//     {_#def, _#def: ...}, and a reference to a definition below the top as
//     R.#x with let R = {#x: ...}.
//   - Comprehensions are kept as written, including ones over concrete data that
//     have fired; a comprehension being present does not mean it is pending.
//   - Interpolations are partly evaluated ("v\(5)").
//   - A dual alias's label (x~(K,V)) is replaced by the label; a dynamic field's
//     label alias loses its value (label: string where Final shows "dyn").
//   - cue.Hidden(false) has no effect on this view.
//   - An error is exported as _|_ with its message in a comment, not as a
//     failure; only that comment tells it from an author's _|_.
//   - The result is an *ast.File when v needs an import (even of a builtin
//     package such as strings), and an expression otherwise.
//
// An internal export failure is returned as an error, not as the *ast.BadExpr
// CUE produces for it.
func Evaluated(v cue.Value) (ast.Node, error) {
	return checked(v.Syntax())
}

// SelfContained is Evaluated with references into imported packages inlined:
// v.Syntax(cue.InlineImports(true)). Evaluated leaves a reference such as
// shared.#Thing as written, with an import of its package, so the referenced
// content isn't in the result; here it is, bound in a generated let like any
// other reference that leaves v (THING.#x, with let THING = {#x: ...} and the
// doc comment "//cue:path: \"example.com/shared\".#Thing"). References to
// builtin packages (strings.MinRunes) stay references. It has all of
// Evaluated's other quirks.
//
// Use it where what v means must not depend on content outside it, such as
// hashing v's content.
func SelfContained(v cue.Value) (ast.Node, error) {
	return checked(v.Syntax(cue.InlineImports(true)))
}

// Raw returns v without simplification and without making it self-contained:
// v.Syntax(cue.Raw()). References are left dangling, as written (#D, h6.r, ext),
// so the result means nothing outside the scope v came from.
//
// Use it to see which names v's syntax refers to. Its quirk: a let declared
// outside v is inlined (r: OUT shows OUT's body), though field references are
// not.
func Raw(v cue.Value) (ast.Node, error) {
	return checked(v.Syntax(cue.Raw()))
}

// Final returns v with defaults taken and structs and lists treated as closed:
// v.Syntax(cue.Final()). Definitions, hidden and optional fields are omitted.
//
// It is not data. Required fields (r!: int) and references that are not
// concrete (r: INPUT.y) remain; close() and ... are dropped. References to
// hidden siblings and self are resolved, but an author's lets are kept. Pending
// comprehensions are inconsistent: a top-level if disappears, a nested one
// remains, a for remains, and a list's becomes []. An error anywhere makes the
// whole result _|_.
//
// Use it to see the value a user of v would see by default, where v need not be
// concrete. For data, use Data.
func Final(v cue.Value) (ast.Node, error) {
	return checked(v.Syntax(cue.Final()))
}

// Data returns v as concrete data: v.Syntax(cue.Concrete(true)), after checking
// that v is concrete. Definitions, hidden and optional fields are omitted.
//
// It fails if v is not concrete. Without that check, CUE writes each
// non-concrete value inline as _|_ with the message in a comment, and reports no
// error.
func Data(v cue.Value) (ast.Node, error) {
	if err := v.Validate(cue.Concrete(true)); err != nil {
		return nil, err
	}
	return checked(v.Syntax(cue.Concrete(true)))
}

// Documented is Evaluated with doc comments: v.Syntax(cue.Docs(true)), with all
// of Evaluated's quirks.
//
// Use it for output people read, such as generated code. Note that CUE ignores
// the argument to cue.Docs: cue.Docs(false) also includes docs.
func Documented(v cue.Value) (ast.Node, error) {
	return checked(v.Syntax(cue.Docs(true)))
}

// Text formats n as CUE source.
func Text(n ast.Node) (string, error) {
	b, err := format.Node(n)
	return string(b), err
}

// checked returns n, or an error if exporting failed. CUE reports an internal
// export failure as an *ast.BadExpr carrying the details in a comment, which a
// caller walking the syntax would otherwise read as a value with nothing in it.
func checked(n ast.Node) (ast.Node, error) {
	var bad *ast.BadExpr
	ast.Walk(n, func(n ast.Node) bool {
		if b, ok := n.(*ast.BadExpr); ok && bad == nil {
			bad = b
		}
		return bad == nil
	}, nil)
	if bad != nil {
		var msg string
		for _, cg := range ast.Comments(bad) {
			msg += cg.Text()
		}
		return nil, fmt.Errorf("exporting CUE value failed: %s", msg)
	}
	return n, nil
}
