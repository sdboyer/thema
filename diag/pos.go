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

// AuthorPos is [embedreg.AuthorPos], reporting whether the position is the
// author's: false when v has only positions in thema's own CUE, or none.
func AuthorPos(v cue.Value) (token.Pos, bool) {
	p := embedreg.AuthorPos(v)
	return p, p.IsValid() && !embedreg.IsKernel(p)
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
