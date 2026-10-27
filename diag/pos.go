package diag

import (
	"cuelang.org/go/cue"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/internal/embedreg"
)

// OriginOf classifies a filename: thema's own CUE, a file with no name, or
// the author's.
func OriginOf(filename string) Origin {
	switch {
	case filename == "":
		return OriginSynth
	case embedreg.IsKernelFile(filename):
		return OriginThema
	}
	return OriginUser
}

// PosOf converts a CUE position. It returns nil for an invalid position.
func PosOf(p token.Pos) *Pos {
	if !p.IsValid() {
		return nil
	}
	return &Pos{
		File:   p.Filename(),
		Line:   p.Line(),
		Col:    p.Column(),
		Offset: p.Offset(),
		Origin: OriginOf(p.Filename()),
	}
}

// AuthorPos returns the position of the author's text for v: the first
// conjunct of v whose position is not in thema's own CUE. It reports false
// when v has no such conjunct and no position of its own.
func AuthorPos(v cue.Value) (token.Pos, bool) {
	_, args := v.Expr()
	for _, a := range args {
		if p := a.Pos(); p.IsValid() && !embedreg.IsKernelFile(p.Filename()) {
			return p, true
		}
	}
	if p := v.Pos(); p.IsValid() {
		return p, !embedreg.IsKernelFile(p.Filename())
	}
	return token.NoPos, false
}

// dedup removes invalid and repeated positions, keeping order.
func dedup(ps []token.Pos) []token.Pos {
	seen := make(map[token.Pos]bool, len(ps))
	out := make([]token.Pos, 0, len(ps))
	for _, p := range ps {
		if !p.IsValid() || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
