package cuetil

import (
	"cuelang.org/go/cue"
)

// UndeclaredFields returns the paths of all fields in data, at any depth, that
// sch does not declare.
//
// A field is declared if sch has a field of that name (regular, optional or
// required), or a pattern constraint (e.g. [=~"^x"]: T) matching its name. An
// open struct's ... declares nothing: unlike closedness, this reports fields that
// sch merely tolerates. Where sch is a disjunction, data needs to match only one
// of its branches.
func UndeclaredFields(ctx *cue.Context, data, sch cue.Value) []cue.Path {
	return undeclared(ctx, data, sch, cue.MakePath())
}

func undeclared(ctx *cue.Context, data, sch cue.Value, p cue.Path) []cue.Path {
	// A disjunction with a default resolves to the default, and for some shapes
	// (notably lists) Expr() then reports neither the default nor an OrOp, just the
	// non-default value space. Judge data against that space, not the default.
	if _, hasDefault := sch.Default(); hasDefault {
		if op, args := sch.Expr(); op == cue.NoOp && len(args) == 1 {
			sch = args[0]
		}
	}

	if op, branches := sch.Expr(); op == cue.OrOp {
		// Judge data only against branches it could be an instance of. A branch
		// that can't hold the data at all (e.g. a default of []) would otherwise
		// look like a perfect match, as there would be nothing in it to compare.
		var fits []cue.Value
		for _, b := range branches {
			if b.Unify(data).Validate() == nil {
				fits = append(fits, b)
			}
		}
		if len(fits) > 0 {
			branches = fits
		}

		var best []cue.Path
		for i, b := range branches {
			found := undeclared(ctx, data, b, p)
			if len(found) == 0 {
				return nil
			}
			if i == 0 || len(found) < len(best) {
				best = found
			}
		}
		return best
	}

	switch data.IncompleteKind() {
	case cue.StructKind:
		return undeclaredInStruct(ctx, data, sch, p)
	case cue.ListKind:
		var found []cue.Path
		iter, err := data.List()
		if err != nil {
			return nil
		}
		for i := 0; iter.Next(); i++ {
			elsch := sch.LookupPath(cue.MakePath(cue.Index(i)))
			if !elsch.Exists() {
				elsch = sch.LookupPath(cue.MakePath(cue.AnyIndex))
			}
			if elsch.Exists() {
				found = append(found, undeclared(ctx, iter.Value(), elsch, appendPath(p, cue.Index(i)))...)
			}
		}
		return found
	}
	return nil
}

func undeclaredInStruct(ctx *cue.Context, data, sch cue.Value, p cue.Path) []cue.Path {
	fields := make(map[string]cue.Value)
	var patterns []cue.Selector
	var patternVals []cue.Value
	siter, err := sch.Fields(cue.Optional(true), cue.Patterns(true))
	if err != nil {
		// Not a struct-kinded schema; disagreements of kind are not this function's concern.
		return nil
	}
	for siter.Next() {
		sel := siter.Selector()
		if sel.ConstraintType() == cue.PatternConstraint {
			patterns = append(patterns, sel)
			patternVals = append(patternVals, siter.Value())
			continue
		}
		if sel.IsString() {
			fields[sel.Unquoted()] = siter.Value()
		}
	}

	var found []cue.Path
	diter, err := data.Fields()
	if err != nil {
		return nil
	}
	for diter.Next() {
		sel := diter.Selector()
		if !sel.IsString() {
			continue
		}
		name := sel.Unquoted()
		fp := appendPath(p, sel)
		if fsch, ok := fields[name]; ok {
			found = append(found, undeclared(ctx, diter.Value(), fsch, fp)...)
			continue
		}
		matched := false
		for i, pat := range patterns {
			if pat.Pattern().Unify(ctx.Encode(name)).Err() == nil {
				found = append(found, undeclared(ctx, diter.Value(), patternVals[i], fp)...)
				matched = true
				break
			}
		}
		if !matched {
			found = append(found, fp)
		}
	}
	return found
}

func appendPath(p cue.Path, sel cue.Selector) cue.Path {
	return cue.MakePath(append(p.Selectors(), sel)...)
}
