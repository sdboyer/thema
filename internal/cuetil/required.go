package cuetil

import (
	"slices"

	"cuelang.org/go/cue"
)

// RequiredPresent returns v with each required field it holds made regular, so
// that v stands for any instance of the schema it is: every instance has every
// required field, while v itself, being a schema, has none of them. Without
// this, a reference to a required field of v is an incomplete error (required
// field missing), not the field's constraint; since CUE v0.15, unifying that
// error with an equal constant even reports the two as conflicting (#154).
//
// Required fields are made regular wherever an instance must have them: in v's
// regular and required fields and in the elements of its lists, recursively.
// Beneath an optional field, or in an element a list's pattern allows, an
// instance may have nothing at all, and references there are already
// incomplete, so those are left as they are. v itself is unchanged.
func RequiredPresent(v cue.Value) cue.Value {
	return requiredPresent(v, v, nil)
}

func requiredPresent(root, v cue.Value, at []cue.Selector) cue.Value {
	switch v.IncompleteKind() {
	case cue.StructKind:
		iter, err := v.Fields(cue.Optional(true))
		if err != nil {
			return root
		}
		for iter.Next() {
			sel := iter.Selector()
			if sel.ConstraintType() == cue.OptionalConstraint {
				continue
			}
			p := slices.Concat(at, []cue.Selector{cue.Str(sel.Unquoted())})
			if sel.ConstraintType() == cue.RequiredConstraint {
				root = root.FillPath(cue.MakePath(p...), iter.Value())
			}
			root = requiredPresent(root, iter.Value(), p)
		}
	case cue.ListKind:
		iter, err := v.List()
		if err != nil {
			return root
		}
		for i := 0; iter.Next(); i++ {
			root = requiredPresent(root, iter.Value(), slices.Concat(at, []cue.Selector{cue.Index(i)}))
		}
	}
	return root
}
