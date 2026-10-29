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
//
// Comments keep the line breaks they had, which are not layout: a blank
// line between two comment groups keeps them apart, and a comment's own line
// keeps it off the line before. See [arrange] for the line breaks between
// comments and declarations.
func layout(root ast.Node) {
	var top []ast.Decl
	switch x := root.(type) {
	case *ast.File:
		top = x.Decls
	case *ast.StructLit:
		top = x.Elts
	}
	ast.Walk(root, func(n ast.Node) bool {
		if cg, ok := n.(*ast.CommentGroup); ok {
			for _, c := range cg.List {
				c.Slash = layoutFile.Pos(0, c.Slash.RelPos())
			}
			return false
		}
		ast.SetPos(n, token.NoPos)
		switch x := n.(type) {
		case *ast.StructLit:
			x.Rbrace = token.NoPos
		case *ast.ListLit:
			x.Rbrack = token.NoPos
		}
		return true
	}, func(n ast.Node) {
		switch x := n.(type) {
		case *ast.Field:
			if s, ok := x.Value.(*ast.StructLit); ok && len(s.Elts) == 1 {
				s.Lbrace = layoutFile.Pos(0, token.NoRelPos)
				s.Rbrace = layoutFile.Pos(0, token.Newline)
			}
		case *ast.StructLit:
			if n != root {
				arrange(x.Elts, false)
			}
		}
	})
	arrange(top, true)
}

// arrange lays out the line breaks between the declarations of a body and
// the comments around them. The declarations of the top-level body are
// separated by a blank line, those of a struct by a line break, whatever
// separated them before; so is a declaration's doc comment from what
// precedes it. But a blank line next to a comment can decide what the
// comment is attached to: the parser makes a comment group directly above a
// declaration its doc comment, and one a blank line away a comment on its
// own. So a declaration keeps a blank line after a comment on its own,
// whether that is the last comment of the declaration before it or one
// above its doc comment. The first declaration of the top-level body keeps
// the line breaks before it, which separate it from the file's comments.
func arrange(decls []ast.Decl, top bool) {
	for i, d := range decls {
		lead := token.Newline
		switch {
		case i == 0 && top:
			lead = token.NoRelPos
		case i > 0 && (top || endsWithOwnComment(decls[i-1])):
			lead = token.NewSection
		}
		var above []*ast.CommentGroup
		for _, cg := range ast.Comments(d) {
			if cg.Position == 0 {
				above = append(above, cg)
			}
		}
		if len(above) == 0 {
			if lead == token.NewSection {
				ast.SetPos(d, layoutFile.Pos(0, lead))
			}
			continue
		}
		// A comment ending the line before stays there.
		if first := above[0]; lead != token.NoRelPos && !first.Line {
			first.List[0].Slash = layoutFile.Pos(0, lead)
		}
		if last := above[len(above)-1]; !last.Doc && !last.Line {
			ast.SetPos(d, layoutFile.Pos(0, token.NewSection))
		}
	}
}

// endsWithOwnComment reports whether d's last comment follows it on a line
// of its own.
func endsWithOwnComment(d ast.Decl) bool {
	cgs := ast.Comments(d)
	if len(cgs) == 0 {
		return false
	}
	last := cgs[len(cgs)-1]
	return last.Position > 0 && !last.Line
}
