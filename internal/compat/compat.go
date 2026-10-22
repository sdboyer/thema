package compat

import (
	"cuelang.org/go/cue"

	"github.com/sdboyer/thema/internal/cuetil"
)

// ThemaCompatible is the canonical Thema algorithm for checking that the
// [cue.Value] s is (backwards) compatible with p. A nil return indicates
// compatibility.
//
// Both values are closed recursively before comparison, whatever openness their
// authors wrote: subsumption between open structs misjudges field additions and
// removals (e.g. adding an optional field to an open struct narrows the values
// it accepts for that field name).
func ThemaCompatible(ctx *cue.Context, p, s cue.Value) error {
	return cuetil.CloseRecursive(ctx, s).Subsume(cuetil.CloseRecursive(ctx, p), cue.Raw(), cue.All())
}
