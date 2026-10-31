package cuetil

import (
	"cuelang.org/go/cue"
)

// UndeclaredFields returns the paths of all fields in data, at any depth, that
// sch does not declare.
//
// A field is declared if sch has a field of that name (regular, optional or
// required). Only a field with a predictable name is declared: a name that a
// pattern constraint (e.g. [string]: T) matches is not, nor is one an open
// struct's ... admits. Unlike closedness, this reports fields that sch merely
// tolerates. A field a pattern matches is reported whole, with what its value
// holds, as is any other undeclared field (see [PatternKey]). Where sch is a
// disjunction, data needs to match only one of its branches.
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
	if op == cue.SelectorOp {
		// A reference (x.schema, #D): its alternatives are those of the value
		// it refers to, which Expr does not look through.
		if d := cue.Dereference(sch); d != sch {
			return alternatives(d, depth+1)
		}
	}
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

// schemaFields returns the fields sch declares, by name.
func schemaFields(sch cue.Value) (map[string]cue.Value, error) {
	fields := make(map[string]cue.Value)
	siter, err := sch.Fields(cue.Optional(true))
	if err != nil {
		return nil, err
	}
	for siter.Next() {
		sel := siter.Selector()
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
	return fields, nil
}

func (w walker) undeclaredInStruct(data, sch cue.Value, p cue.Path, b *LensField) []cue.Path {
	fields, err := schemaFields(sch)
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
		found = append(found, fp)
	}
	return found
}

func appendPath(p cue.Path, sel cue.Selector) cue.Path {
	return cue.MakePath(append(p.Selectors(), sel)...)
}

// DeclaresPath reports whether sch declares a field at path p, by the same rules
// as [UndeclaredFields]: a field of that name (regular, optional or required) at
// each step. A pattern constraint matching a name declares nothing, nor does an
// open struct's ..., and _ declares everything beneath it. Where sch is a
// disjunction, any alternative declaring the path suffices.
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
// required); a pattern constraint matching it declares nothing (see
// [PatternKey]). The selector returned is the one sch declares it with, so
// that its constraint type tells a required or defaulted field from an
// optional one. For an index into a list, the element at that index, or the
// list's open element type (cue.AnyIndex).
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
		iter, err := sch.Fields(cue.Optional(true))
		if err != nil {
			return cue.Value{}, cue.Selector{}, false
		}
		name := sel.Unquoted()
		for iter.Next() {
			s := iter.Selector()
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
	}
	return cue.Value{}, cue.Selector{}, false
}

// PatternKey returns the shortest prefix of p whose last step sch accepts only
// through a pattern constraint: each step before it is a field sch declares
// (see [DeclaresPath]), and its last is a name no field declares that a
// pattern constraint of its parent matches, as env in labels.env where sch
// declares labels: [string]: string. Such a key is undeclared data: a lens
// neither reads nor writes it, and translation carries it like a field an
// open struct's ... admits. ok is false where p has no such prefix: every step
// is declared, or one is neither declared nor matched by a pattern, or lies
// beneath _, which declares everything. Where sch is a disjunction, the
// alternatives that declare a step are followed, and a step none declares is
// a key where one of them has a pattern matching it.
func PatternKey(ctx *cue.Context, sch cue.Value, p cue.Path) (cue.Path, bool) {
	cur := []cue.Value{sch}
	sels := p.Selectors()
	for i, sel := range sels {
		var next []cue.Value
		matched := false
		for _, v := range cur {
			if v.IncompleteKind() == cue.TopKind {
				return cue.Path{}, false
			}
			for _, alt := range alternatives(v, 0) {
				if alt.IncompleteKind() == cue.TopKind {
					return cue.Path{}, false
				}
				if child, _, ok := DeclaredChild(ctx, alt, sel); ok {
					next = append(next, child)
					continue
				}
				matched = matched || patternMatches(ctx, alt, sel)
			}
		}
		if len(next) == 0 {
			if matched {
				return cue.MakePath(sels[:i+1]...), true
			}
			return cue.Path{}, false
		}
		cur = next
	}
	return cue.Path{}, false
}

// patternMatches reports whether a pattern constraint of sch matches the
// field name sel.
func patternMatches(ctx *cue.Context, sch cue.Value, sel cue.Selector) bool {
	if !sel.IsString() {
		return false
	}
	iter, err := sch.Fields(cue.Patterns(true))
	if err != nil {
		return false
	}
	name := ctx.Encode(sel.Unquoted())
	for iter.Next() {
		if s := iter.Selector(); s.ConstraintType() == cue.PatternConstraint && s.Pattern().Unify(name).Err() == nil {
			return true
		}
	}
	return false
}
