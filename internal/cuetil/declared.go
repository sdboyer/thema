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
	w := walker{ctx: ctx}
	return w.undeclared(data, sch, cue.MakePath())
}

// UndeclaredBuiltFields is like [UndeclaredFields], but for a value built by an
// expression, such as a lens result: only fields the expression constructs are
// checked. A field whose value is a reference (e.g. b: input.a) passes through
// whatever it refers to, and is not descended into.
func UndeclaredBuiltFields(ctx *cue.Context, built, sch cue.Value) []cue.Path {
	w := walker{ctx: ctx, skipRefs: true}
	return w.undeclared(built, sch, cue.MakePath())
}

type walker struct {
	ctx *cue.Context
	// skipRefs causes fields whose values are references to be skipped.
	skipRefs bool
}

func isReference(v cue.Value) bool {
	op, _ := v.Expr()
	return op == cue.SelectorOp || op == cue.IndexOp
}

func (w walker) undeclared(data, sch cue.Value, p cue.Path) []cue.Path {
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
			found := w.undeclaredIn(data, a, p)
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
	return w.undeclaredIn(data, sch, p)
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

func (w walker) undeclaredIn(data, sch cue.Value, p cue.Path) []cue.Path {
	switch data.IncompleteKind() {
	case cue.StructKind:
		return w.undeclaredInStruct(data, sch, p)
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
				found = append(found, w.undeclared(iter.Value(), elsch, appendPath(p, cue.Index(i)))...)
			}
		}
		return found
	}
	return nil
}

func (w walker) undeclaredInStruct(data, sch cue.Value, p cue.Path) []cue.Path {
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
		if w.skipRefs && isReference(diter.Value()) {
			continue
		}
		name := sel.Unquoted()
		fp := appendPath(p, sel)
		if fsch, ok := fields[name]; ok {
			found = append(found, w.undeclared(diter.Value(), fsch, fp)...)
			continue
		}
		matched := false
		for i, pat := range patterns {
			if pat.Pattern().Unify(w.ctx.Encode(name)).Err() == nil {
				found = append(found, w.undeclared(diter.Value(), patternVals[i], fp)...)
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
		if next, ok := declaredChild(ctx, alt, sels[0]); ok && DeclaresPath(ctx, next, cue.MakePath(sels[1:]...)) {
			return true
		}
	}
	return false
}

// declaredChild returns the schema sch declares for the child sel, if any.
func declaredChild(ctx *cue.Context, sch cue.Value, sel cue.Selector) (cue.Value, bool) {
	switch {
	case sel.Type().LabelType() == cue.IndexLabel:
		if v := sch.LookupPath(cue.MakePath(sel)); v.Exists() {
			return v, true
		}
		if v := sch.LookupPath(cue.MakePath(cue.AnyIndex)); v.Exists() {
			return v, true
		}
		return cue.Value{}, false
	case sel.IsString():
		iter, err := sch.Fields(cue.Optional(true), cue.Patterns(true))
		if err != nil {
			return cue.Value{}, false
		}
		name := sel.Unquoted()
		var pattern cue.Value
		for iter.Next() {
			s := iter.Selector()
			if s.ConstraintType() == cue.PatternConstraint {
				if !pattern.Exists() && s.Pattern().Unify(ctx.Encode(name)).Err() == nil {
					pattern = iter.Value()
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
				return v, true
			}
		}
		return pattern, pattern.Exists()
	}
	return cue.Value{}, false
}
