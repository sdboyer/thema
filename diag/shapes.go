package diag

import (
	cerrors "cuelang.org/go/cue/errors"
)

// A shape recognizes one kind of error the CUE evaluator produces, by the
// format string of its message, and says how thema describes it. The format
// strings are pinned by the menagerie (diag/testdata/menagerie): a CUE upgrade
// that changes one fails the menagerie's digest before it can silently stop
// matching here.
type shape struct {
	// name identifies the shape in tests and in docs/cue-errors.md.
	name string
	// msg is the format string of the CUE message this shape matches, as the
	// menagerie records it. Empty for a shape matched by error type.
	msg string
	// match reports whether e has this shape.
	match func(e cerrors.Error) bool
	// build describes e. It sets Code, Severity, Headline and Address; From
	// places positions and renders the path.
	build func(e cerrors.Error, ctx Context) Diagnostic
}

// shapes is the table, in matching order. It is filled as thema's operations
// migrate to diag; an entry no shape matches is reported with [E0902].
var shapes []shape

// recognize matches e against the shapes.
func (ctx Context) recognize(e cerrors.Error) (Diagnostic, bool) {
	for _, s := range shapes {
		if s.match(e) {
			d := s.build(e, ctx)
			if d.Detail == "" {
				d.Detail = e.Error()
			}
			ctx.place(&d, e.Path(), positionsOf(e), "")
			return d, true
		}
	}
	return Diagnostic{}, false
}
