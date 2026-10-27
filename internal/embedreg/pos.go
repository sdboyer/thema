package embedreg

import (
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/internal/util"
)

// RuntimeRoot is the virtual absolute path at which thema's runtime loads
// thema's own CUE package for itself (runtime.go): the module path without a
// version, e.g. /github.com/sdboyer/thema. Lineages bound without importing
// thema are unified with thema's CUE from there; lineages that import thema
// get it from [Root].
func RuntimeRoot() string {
	base, _, _ := strings.Cut(util.ThemaModulePath, "@")
	return filepath.Join(util.Prefix, filepath.FromSlash(base))
}

// IsKernel reports whether p is a position in thema's own CUE, at [Root] or at
// [RuntimeRoot].
func IsKernel(p token.Pos) bool {
	if !p.IsValid() {
		return false
	}
	sep := string(filepath.Separator)
	f := p.Filename()
	return strings.HasPrefix(f, Root()+sep) || strings.HasPrefix(f, RuntimeRoot()+sep)
}

// AuthorPos returns the position of the first conjunct of v that is not in
// thema's own CUE, or v's position if there is none: where the author wrote
// what v holds, for an error about it.
func AuthorPos(v cue.Value) token.Pos {
	if !v.Exists() {
		return token.NoPos
	}
	_, args := v.Expr()
	for _, a := range args {
		if p := a.Pos(); p.IsValid() && !IsKernel(p) {
			return p
		}
	}
	return v.Pos()
}
