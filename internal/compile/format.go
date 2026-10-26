package compile

import (
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"
)

// Format formats authoring-form text in thema's style: every struct keeps
// its braces, and every field starts its own line. CUE has no normal form
// and this does not try to be one; it is the one layout Decompile produces,
// so that a decompiled file equals its formatted source text for text.
func Format(src []byte) ([]byte, error) {
	f, err := parser.ParseFile("", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	layout(f)
	return format.Node(f)
}

// layoutFile supplies the positions layout needs. cue/format collapses
// a: {b: c} to a: b: c unless the struct's opening brace has a position, and
// separates top-level declarations with a blank line when their position
// says they start a section.
var layoutFile = token.NewFile("", 1, 1)

// layout gives n and everything beneath it thema's layout, forgetting any
// it had. With no positions, cue/format expands every struct with several
// fields and puts each on its own line; a single-field struct that is a
// field's value gets positions on its braces so that it is expanded too
// rather than collapsed.
func layout(n ast.Node) {
	var top []ast.Decl
	switch x := n.(type) {
	case *ast.File:
		top = x.Decls
	case *ast.StructLit:
		top = x.Elts
	}
	ast.Walk(n, func(n ast.Node) bool {
		ast.SetPos(n, token.NoPos)
		switch x := n.(type) {
		case *ast.StructLit:
			x.Rbrace = token.NoPos
		case *ast.ListLit:
			x.Rbrack = token.NoPos
		}
		return true
	}, func(n ast.Node) {
		if f, ok := n.(*ast.Field); ok {
			if s, ok := f.Value.(*ast.StructLit); ok && len(s.Elts) == 1 {
				s.Lbrace = layoutFile.Pos(0, token.NoRelPos)
				s.Rbrace = layoutFile.Pos(0, token.Newline)
			}
		}
	})
	for i, d := range top {
		if i > 0 {
			ast.SetPos(d, layoutFile.Pos(0, token.NewSection))
		}
	}
}
