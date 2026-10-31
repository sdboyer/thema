package lenscheck

import (
	"math/big"

	"cuelang.org/go/cue"
)

// IntegerBounds reads the integer bounds of v as the totality check reads an
// operand's (intervalOf): a constant from its value, a conjunction
// intersecting, a disjunction taking the hull, bounds and sized types such as
// int32 setting an end. lo or hi is nil where v is unbounded on that side. ok
// is false where v is not integer-kinded.
//
// It is exported for encoding/capnp, which sizes an integer field by its
// bounds, so that the two read bounds the same way.
func IntegerBounds(v cue.Value) (lo, hi *big.Int, ok bool) {
	iv, _, ok := intervalOf(v)
	if !ok {
		return nil, nil, false
	}
	return iv.lo, iv.hi, true
}
