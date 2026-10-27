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
//
// Hidden fields follow closedness: one sch does not declare is not data and is
// not checked; beneath one it declares, fields are checked as anywhere else.
// Hidden labels are matched to sch's by name (see [UndeclaredConditionalFields]).
func UndeclaredFields(ctx *cue.Context, data, sch cue.Value) []cue.Path {
	w := walker{ctx: ctx}
	return w.undeclared(data, sch, cue.MakePath(), nil)
}

// UndeclaredLensFields is like [UndeclaredFields], but for a lens result,
// described by f (see [LensSyntax.Result]): only fields the lens writes,
// constant or computed, are checked. Data it copies from its input (e.g.
// b: input.a) is not checked, nor descended into.
func UndeclaredLensFields(ctx *cue.Context, result, sch cue.Value, f *LensField) []cue.Path {
	w := walker{ctx: ctx}
	return w.undeclared(result, sch, cue.MakePath(), f)
}

type walker struct {
	ctx *cue.Context
}

func (w walker) undeclared(data, sch cue.Value, p cue.Path, b *LensField) []cue.Path {
	if sch.IncompleteKind() == cue.TopKind {
		// _ declares everything beneath it.
		return nil
	}

	if alts := alternatives(sch, 0); len(alts) > 1 {
		// Judge data only against alternatives it could be an instance of. One that
		// can't hold the data at all (e.g. a default of []) would otherwise look
		// like a perfect match, as there would be nothing in it to compare.
		var fits []cue.Value
		for _, a := range alts {
			if a.Unify(data).Validate() == nil {
				fits = append(fits, a)
			}
		}
		if len(fits) > 0 {
			alts = fits
		}

		var best []cue.Path
		for i, a := range alts {
			found := w.undeclaredIn(data, a, p, b)
			if len(found) == 0 {
				return nil
			}
			if i == 0 || len(found) < len(best) {
				best = found
			}
		}
		return best
	} else if len(alts) == 1 {
		sch = alts[0]
	}
	return w.undeclaredIn(data, sch, p, b)
}

// maxAlternativesDepth bounds the expression depth alternatives explores.
const maxAlternativesDepth = 16

// alternatives returns the values sch may take, each a non-disjunctive schema:
// defaults are stripped (a disjunction with a default resolves to the default,
// and for some shapes Expr() then reports only the non-default value space),
// nested disjunctions are flattened, and a conjunction with a disjunctive member
// is distributed over that member's alternatives.
func alternatives(sch cue.Value, depth int) []cue.Value {
	if depth > maxAlternativesDepth {
		return []cue.Value{sch}
	}
	op, args := sch.Expr()
	switch op {
	case cue.OrOp:
		var out []cue.Value
		for _, a := range args {
			out = append(out, alternatives(a, depth+1)...)
		}
		return out
	case cue.AndOp:
		for i, a := range args {
			alts := alternatives(a, depth+1)
			if len(alts) < 2 {
				continue
			}
			var rest cue.Value
			for j, b := range args {
				if j == i {
					continue
				}
				if !rest.Exists() {
					rest = b
				} else {
					rest = rest.Unify(b)
				}
			}
			out := make([]cue.Value, len(alts))
			for k, alt := range alts {
				out[k] = alt.Unify(rest)
			}
			return out
		}
	case cue.NoOp:
		if _, hasDefault := sch.Default(); hasDefault && len(args) == 1 {
			return alternatives(args[0], depth+1)
		}
	}
	return []cue.Value{sch}
}

func (w walker) undeclaredIn(data, sch cue.Value, p cue.Path, b *LensField) []cue.Path {
	switch data.IncompleteKind() {
	case cue.StructKind:
		return w.undeclaredInStruct(data, sch, p, b)
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
				// Where a lens's list elements come from is not known: each is checked.
				found = append(found, w.undeclared(iter.Value(), elsch, appendPath(p, cue.Index(i)), nil)...)
			}
		}
		return found
	}
	return nil
}

// schemaFields returns the regular fields sch declares, by name, and its pattern
// constraints.
func schemaFields(sch cue.Value) (map[string]cue.Value, []cue.Selector, []cue.Value, error) {
	fields := make(map[string]cue.Value)
	var patterns []cue.Selector
	var patternVals []cue.Value
	siter, err := sch.Fields(cue.Optional(true), cue.Patterns(true))
	if err != nil {
		return nil, nil, nil, err
	}
	for siter.Next() {
		sel := siter.Selector()
		if sel.ConstraintType() == cue.PatternConstraint {
			patterns = append(patterns, sel)
			patternVals = append(patternVals, siter.Value())
			continue
		}
		if sel.IsString() {
			fv := siter.Value()
			if fv.IncompleteKind() == cue.BottomKind {
				// A recursive reference (e.g. next?: #Node) is not evaluated in place;
				// follow it to the value it refers to.
				if d := cue.Dereference(fv); d.IncompleteKind() != cue.BottomKind {
					fv = d
				}
			}
			fields[sel.Unquoted()] = fv
		}
	}
	return fields, patterns, patternVals, nil
}

func (w walker) undeclaredInStruct(data, sch cue.Value, p cue.Path, b *LensField) []cue.Path {
	fields, patterns, patternVals, err := schemaFields(sch)
	if err != nil {
		// Not a struct-kinded schema; disagreements of kind are not this function's concern.
		return nil
	}

	var found []cue.Path
	diter, err := data.Fields(cue.Hidden(true))
	if err != nil {
		return nil
	}
	for diter.Next() {
		sel := diter.Selector()
		fp := appendPath(p, sel)
		if sel.LabelType() == cue.HiddenLabel {
			cb, through := b.child(sel.String())
			if fsch, ok := declaredHidden(sch, sel.String()); ok && !through {
				found = append(found, w.undeclared(diter.Value(), fsch, fp, cb)...)
			}
			continue
		}
		if !sel.IsString() {
			continue
		}
		name := sel.Unquoted()
		cb, through := b.child(dataKey(name))
		if through {
			continue
		}
		if fsch, ok := fields[name]; ok {
			found = append(found, w.undeclared(diter.Value(), fsch, fp, cb)...)
			continue
		}
		matched := false
		for i, pat := range patterns {
			if pat.Pattern().Unify(w.ctx.Encode(name)).Err() == nil {
				found = append(found, w.undeclared(diter.Value(), patternVals[i], fp, cb)...)
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

// DeclaresPath reports whether sch declares a field at path p, by the same rules
// as [UndeclaredFields]: a field of that name (regular, optional or required) or a
// matching pattern constraint at each step. An open struct's ... declares
// nothing, and _ declares everything beneath it. Where sch is a disjunction, any
// alternative declaring the path suffices.
func DeclaresPath(ctx *cue.Context, sch cue.Value, p cue.Path) bool {
	sels := p.Selectors()
	if len(sels) == 0 {
		return true
	}
	if sch.IncompleteKind() == cue.TopKind {
		return true
	}
	for _, alt := range alternatives(sch, 0) {
		if next, _, ok := DeclaredChild(ctx, alt, sels[0]); ok && DeclaresPath(ctx, next, cue.MakePath(sels[1:]...)) {
			return true
		}
	}
	return false
}

// DeclaredChild returns the schema sch declares for the child sel, if any, by
// the rules of [DeclaresPath]: a field of that name (regular, optional or
// required), or a pattern constraint matching it. The selector returned is the
// one sch declares it with, so that its constraint type tells a required or
// defaulted field from an optional one, and a named field from a label a
// pattern merely accepts (a pattern constraint). For an index into a list, the
// element at that index, or the list's open element type (cue.AnyIndex).
//
// Where sch is a disjunction, the caller chooses the alternative: see
// cuetil.alternatives and compat.Branches.
func DeclaredChild(ctx *cue.Context, sch cue.Value, sel cue.Selector) (cue.Value, cue.Selector, bool) {
	switch {
	case sel.Type().LabelType() == cue.IndexLabel:
		if v := sch.LookupPath(cue.MakePath(sel)); v.Exists() {
			return v, sel, true
		}
		if v := sch.LookupPath(cue.MakePath(cue.AnyIndex)); v.Exists() {
			return v, cue.AnyIndex, true
		}
		return cue.Value{}, cue.Selector{}, false
	case sel.IsString():
		iter, err := sch.Fields(cue.Optional(true), cue.Patterns(true))
		if err != nil {
			return cue.Value{}, cue.Selector{}, false
		}
		name := sel.Unquoted()
		var pattern cue.Value
		var patsel cue.Selector
		for iter.Next() {
			s := iter.Selector()
			if s.ConstraintType() == cue.PatternConstraint {
				if !pattern.Exists() && s.Pattern().Unify(ctx.Encode(name)).Err() == nil {
					pattern, patsel = iter.Value(), s
				}
				continue
			}
			if s.IsString() && s.Unquoted() == name {
				v := iter.Value()
				if v.IncompleteKind() == cue.BottomKind {
					if d := cue.Dereference(v); d.IncompleteKind() != cue.BottomKind {
						v = d
					}
				}
				return v, s, true
			}
		}
		return pattern, patsel, pattern.Exists()
	}
	return cue.Value{}, cue.Selector{}, false
}
