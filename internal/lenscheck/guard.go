package lenscheck

import (
	"fmt"
	"sort"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/cuetil"
)

// guardKind is the form of a comprehension's or lacuna's condition.
type guardKind int

const (
	// opaque is a condition the checker could not classify. It never matches
	// another guard, so coverage beneath it stays Unknown.
	opaque guardKind = iota
	// presence is p != _|_ (neg: p == _|_).
	presence
	// equality is p == c for a literal c (neg: p != c).
	equality
	// typeTest is (p & T) != _|_ for a type or literal T (neg: == _|_).
	typeTest
)

// A guard is one condition a comprehension or lacuna applies to the input,
// classified so that the walk can refine the source beneath it and compare
// guards to one another.
type guard struct {
	kind guardKind
	path cue.Path // the input path tested
	neg  bool
	lit  string    // equality: the literal; typeTest: the type, as written
	val  cue.Value // the literal or type
	text string    // opaque: the condition as written
}

// key identifies the condition, for comparing guards.
func (g guard) key() string {
	if g.kind == opaque {
		return "opaque|" + g.text
	}
	return fmt.Sprintf("%d|%s|%v|%s", g.kind, g.path, g.neg, g.lit)
}

func (g guard) negated() guard {
	g.neg = !g.neg
	return g
}

func (g guard) String() string {
	p := inputRef(g.path)
	switch g.kind {
	case presence:
		if g.neg {
			return p + " == _|_"
		}
		return p + " != _|_"
	case equality:
		if g.neg {
			return p + " != " + g.lit
		}
		return p + " == " + g.lit
	case typeTest:
		if g.neg {
			return "(" + p + " & " + g.lit + ") == _|_"
		}
		return "(" + p + " & " + g.lit + ") != _|_"
	}
	return g.text
}

func guardsString(gs []guard) string {
	if len(gs) == 0 {
		return "no condition"
	}
	var ss []string
	for _, g := range gs {
		ss = append(ss, g.String())
	}
	return strings.Join(ss, " && ")
}

func guardsKey(gs []guard) string {
	var ss []string
	for _, g := range gs {
		ss = append(ss, g.key())
	}
	return strings.Join(ss, "&")
}

func describeGuardSets(sets [][]guard) string {
	var ss []string
	seen := map[string]bool{}
	for _, gs := range sets {
		s := guardsString(gs)
		if !seen[s] {
			seen[s] = true
			ss = append(ss, s)
		}
	}
	sort.Strings(ss)
	return strings.Join(ss, "; ")
}

// parseGuard classifies a condition: a presence test, an equality or a type
// test on an input path, or a conjunction (&&) of these. Operands are matched
// in either order, since export may reorder them.
func (w *walker) parseGuard(x ast.Expr) ([]guard, bool) {
	switch e := x.(type) {
	case *ast.ParenExpr:
		return w.parseGuard(e.X)
	case *ast.UnaryExpr:
		if e.Op == token.NOT {
			gs, ok := w.parseGuard(e.X)
			if ok && len(gs) == 1 && gs[0].kind != opaque {
				return []guard{gs[0].negated()}, true
			}
		}
	case *ast.BinaryExpr:
		switch e.Op {
		case token.LAND:
			l, ok1 := w.parseGuard(e.X)
			r, ok2 := w.parseGuard(e.Y)
			return append(l, r...), ok1 && ok2
		case token.EQL, token.NEQ:
			if other, ok := bottomSide(e); ok {
				neg := e.Op == token.EQL
				if p, ok := w.pathOf(other); ok {
					return []guard{{kind: presence, path: p, neg: neg}}, true
				}
				if be, ok := stripParens(other).(*ast.BinaryExpr); ok && be.Op == token.AND {
					for _, pair := range [][2]ast.Expr{{be.X, be.Y}, {be.Y, be.X}} {
						p, ok := w.pathOf(pair[0])
						if !ok {
							continue
						}
						if tv, ok := w.constraintValue(pair[1]); ok {
							return []guard{{kind: typeTest, path: p, neg: neg, lit: text(pair[1]), val: tv}}, true
						}
					}
				}
				return nil, false
			}
			for _, pair := range [][2]ast.Expr{{e.X, e.Y}, {e.Y, e.X}} {
				p, ok := w.pathOf(pair[0])
				if !ok {
					continue
				}
				if lv, ok := w.literalValue(pair[1]); ok {
					return []guard{{kind: equality, path: p, neg: e.Op == token.NEQ, lit: text(pair[1]), val: lv}}, true
				}
			}
		}
	}
	return nil, false
}

func stripParens(x ast.Expr) ast.Expr {
	for {
		p, ok := x.(*ast.ParenExpr)
		if !ok {
			return x
		}
		x = p.X
	}
}

// bottomSide returns the operand of e that is not _|_, if one is.
func bottomSide(e *ast.BinaryExpr) (ast.Expr, bool) {
	if _, ok := stripParens(e.Y).(*ast.BottomLit); ok {
		return e.X, true
	}
	if _, ok := stripParens(e.X).(*ast.BottomLit); ok {
		return e.Y, true
	}
	return nil, false
}

// literalValue returns the value of x if it is a scalar literal.
func (w *walker) literalValue(x ast.Expr) (cue.Value, bool) {
	switch e := stripParens(x).(type) {
	case *ast.BasicLit:
		if e.Kind == token.FLOAT {
			return cue.Value{}, false
		}
	case *ast.UnaryExpr:
		if lit, ok := e.X.(*ast.BasicLit); !ok || e.Op != token.SUB || lit.Kind != token.INT {
			return cue.Value{}, false
		}
	case *ast.Ident:
		if !w.predeclared(e) || (e.Name != "true" && e.Name != "false" && e.Name != "null") {
			return cue.Value{}, false
		}
	default:
		return cue.Value{}, false
	}
	v := w.ctx.BuildExpr(x)
	return v, v.Err() == nil
}

// constraintValue returns the value of x if it is a constraint written without
// references: a predeclared type (string, int32), a literal, a bound, or a
// disjunction or conjunction of these.
func (w *walker) constraintValue(x ast.Expr) (cue.Value, bool) {
	var ok func(x ast.Expr) bool
	ok = func(x ast.Expr) bool {
		switch e := x.(type) {
		case *ast.ParenExpr:
			return ok(e.X)
		case *ast.BasicLit:
			return true
		case *ast.Ident:
			return w.predeclared(e)
		case *ast.UnaryExpr:
			return ok(e.X)
		case *ast.BinaryExpr:
			return (e.Op == token.OR || e.Op == token.AND) && ok(e.X) && ok(e.Y)
		}
		return false
	}
	if !ok(x) {
		return cue.Value{}, false
	}
	v := w.ctx.BuildExpr(x)
	return v, v.Err() == nil
}

// checkGuards checks the guards gs, applied where outer already hold: an
// equality tests a path that must be present (an absent operand is an error,
// not false), and every tested path must be declared.
func (w *walker) checkGuards(gs, outer []guard, subj string, pos token.Pos) {
	for _, g := range gs {
		if g.kind == opaque {
			continue
		}
		src := w.lookupSource(g.path, outer)
		if !src.declared {
			w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleSourceUndeclared,
				Detail: fmt.Sprintf("is guarded by %s, but the source schema does not declare %s", g, inputRef(g.path)), Pos: pos})
			continue
		}
		if g.kind == equality && !src.present {
			w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleGuardAbsent,
				Detail: fmt.Sprintf("is guarded by %s, but %s may be absent (%s), and comparing an absent field is an error rather than false; test presence first: if %s != _|_", g, inputRef(g.path), src.why, inputRef(g.path)), Pos: pos})
		}
	}
}

// establishes reports whether gs establish that input path p is present: a
// presence or type test (not negated) on p or a path beneath it.
func establishes(gs []guard, p cue.Path) bool {
	for _, g := range gs {
		if (g.kind == presence || g.kind == typeTest) && !g.neg && cuetil.PathHasPrefix(g.path, p) {
			return true
		}
	}
	return false
}

// refine narrows the alternatives of the source at p by the guards on exactly
// p: an equality fixes the value, a negated one removes a constant
// alternative, a type test keeps what unifies with the type, a negated one
// drops the alternatives the type subsumes. It reports whether any guard
// applied.
func refine(ctx *cue.Context, branches []cue.Value, p cue.Path, gs []guard) ([]cue.Value, bool) {
	refined := false
	for _, g := range gs {
		if g.kind == opaque || g.kind == presence || g.path.String() != p.String() {
			continue
		}
		refined = true
		var out []cue.Value
		switch {
		case g.kind == equality && !g.neg:
			out = []cue.Value{g.val}
		case g.kind == equality && g.neg:
			for _, b := range branches {
				if compat.ConstantTarget(b) && cuetil.Equal(b, g.val) == nil {
					continue
				}
				out = append(out, b)
			}
		case g.kind == typeTest && !g.neg:
			for _, b := range branches {
				u := b.Unify(g.val)
				if u.Validate() == nil {
					out = append(out, compat.Branches(u)...)
				}
			}
		case g.kind == typeTest && g.neg:
			for _, b := range branches {
				if g.val.Subsume(b, cue.Raw()) == nil {
					continue
				}
				out = append(out, b)
			}
		}
		branches = out
	}
	return branches, refined
}

// guardsHavePrefix reports whether gs begins with prefix.
func guardsHavePrefix(gs, prefix []guard) bool {
	if len(gs) < len(prefix) {
		return false
	}
	for i, g := range prefix {
		if gs[i].key() != g.key() {
			return false
		}
	}
	return true
}

// guardsImplied reports whether every guard in gs is among have.
func guardsImplied(have, gs []guard) bool {
	for _, g := range gs {
		found := false
		for _, h := range have {
			if h.key() == g.key() && g.kind != opaque {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// guardsDisjoint reports whether a and b can never hold together: one has a
// guard the other negates, or each tests the same path for a value or type
// and no value satisfies both (the two unify to an error).
func guardsDisjoint(a, b []guard) bool {
	for _, g := range a {
		if g.kind == opaque {
			continue
		}
		for _, h := range b {
			if h.key() == g.negated().key() {
				return true
			}
			if g.kind != presence && h.kind != presence && !g.neg && !h.neg &&
				g.path.String() == h.path.String() && g.val.Unify(h.val).Err() != nil {
				return true
			}
		}
	}
	return false
}

// covers reports whether the guard sets cands, each the conditions under which
// some write gives a field its value, together hold for every instance. An
// empty set is unconditional. Otherwise the sets' first guards must test one
// path and together cover it (coverSet), and beneath each first guard the rest
// must cover in turn.
func (w *walker) covers(cands [][]guard) bool {
	for _, c := range cands {
		if len(c) == 0 {
			return true
		}
	}
	if len(cands) == 0 {
		return false
	}
	var firsts []guard
	seen := map[string]bool{}
	for _, c := range cands {
		g := c[0]
		if g.kind == opaque || g.path.String() != cands[0][0].path.String() {
			return false
		}
		if !seen[g.key()] {
			seen[g.key()] = true
			firsts = append(firsts, g)
		}
	}
	if !w.coverSet(firsts) {
		return false
	}
	for _, f := range firsts {
		var rest [][]guard
		for _, c := range cands {
			if c[0].key() == f.key() {
				rest = append(rest, c[1:])
			}
		}
		if !w.covers(rest) {
			return false
		}
	}
	return true
}

// coverSet reports whether the guards, all on one input path, together hold for
// every instance: a guard and its negation; type tests one of which subsumes
// each alternative of the path; or equalities with every alternative of the
// path, each a constant. The latter two need the path present.
func (w *walker) coverSet(firsts []guard) bool {
	for i, a := range firsts {
		for _, b := range firsts[i+1:] {
			if a.negated().key() == b.key() {
				return true
			}
		}
	}
	allTypes, allEq := true, true
	for _, g := range firsts {
		allTypes = allTypes && g.kind == typeTest && !g.neg
		allEq = allEq && g.kind == equality && !g.neg
	}
	if !allTypes && !allEq {
		return false
	}
	src := w.lookupSource(firsts[0].path, nil)
	if !src.declared || !src.present || len(src.branches) == 0 {
		return false
	}
	for _, b := range src.branches {
		ok := false
		for _, g := range firsts {
			if allTypes && g.val.Subsume(b, cue.Raw()) == nil {
				ok = true
				break
			}
			if allEq && compat.ConstantTarget(b) && cuetil.Equal(b, g.val) == nil {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
