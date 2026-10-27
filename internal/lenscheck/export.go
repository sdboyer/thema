package lenscheck

import (
	"math/big"

	"cuelang.org/go/cue"
)

// IntegerBounds reads the integer bounds of v through Expr(), as the totality
// check reads an operand's: a conjunction intersects, a disjunction takes the
// hull, bounds and constants set an end, and a sized type such as int32 is
// followed to its bounds. lo or hi is nil where v is unbounded on that side.
// ok is false where v is not integer-kinded.
//
// A concrete integer is its own bounds. Expr() presents a negative literal as
// a unary minus over its magnitude, which intervalOf does not read, so the
// concrete case is answered before it.
//
// It is exported for encoding/capnp, which sizes an integer field by its
// bounds, so that the two read bounds the same way.
func IntegerBounds(v cue.Value) (lo, hi *big.Int, ok bool) {
	if v.IsConcrete() && v.Kind() == cue.IntKind {
		n, err := v.Int(nil)
		if err != nil {
			return nil, nil, false
		}
		return n, n, true
	}
	iv, _, ok := intervalOf(v)
	if !ok {
		return nil, nil, false
	}
	return iv.lo, iv.hi, true
}
