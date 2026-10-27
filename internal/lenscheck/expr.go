package lenscheck

import (
	"fmt"
	"math"
	"math/big"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/cueview"
)

// interval is a range of integers; a nil bound is unbounded on that side.
type interval struct {
	lo, hi *big.Int
}

var (
	minInt64 = big.NewInt(math.MinInt64)
	maxInt64 = big.NewInt(math.MaxInt64)
)

func point(n *big.Int) interval { return interval{lo: n, hi: n} }

func (a interval) bounded() bool { return a.lo != nil && a.hi != nil }

// int64 reports whether every value in a fits an int64.
func (a interval) int64() bool {
	return a.bounded() && a.lo.Cmp(minInt64) >= 0 && a.hi.Cmp(maxInt64) <= 0
}

// within reports whether every value in a is in b.
func (a interval) within(b interval) bool {
	if b.lo != nil && (a.lo == nil || a.lo.Cmp(b.lo) < 0) {
		return false
	}
	if b.hi != nil && (a.hi == nil || a.hi.Cmp(b.hi) > 0) {
		return false
	}
	return true
}

func (a interval) contains(n *big.Int) bool {
	return (a.lo == nil || a.lo.Cmp(n) <= 0) && (a.hi == nil || a.hi.Cmp(n) >= 0)
}

// hull is the smallest interval containing a and b.
func (a interval) hull(b interval) interval {
	var out interval
	if a.lo != nil && b.lo != nil {
		out.lo = minBig(a.lo, b.lo)
	}
	if a.hi != nil && b.hi != nil {
		out.hi = maxBig(a.hi, b.hi)
	}
	return out
}

// meet is the intersection of a and b (possibly empty, which callers treat as
// a dead alternative).
func (a interval) meet(b interval) interval {
	out := a
	if b.lo != nil && (out.lo == nil || b.lo.Cmp(out.lo) > 0) {
		out.lo = b.lo
	}
	if b.hi != nil && (out.hi == nil || b.hi.Cmp(out.hi) < 0) {
		out.hi = b.hi
	}
	return out
}

func (a interval) add(b interval) interval {
	var out interval
	if a.lo != nil && b.lo != nil {
		out.lo = new(big.Int).Add(a.lo, b.lo)
	}
	if a.hi != nil && b.hi != nil {
		out.hi = new(big.Int).Add(a.hi, b.hi)
	}
	return out
}

func (a interval) neg() interval {
	var out interval
	if a.hi != nil {
		out.lo = new(big.Int).Neg(a.hi)
	}
	if a.lo != nil {
		out.hi = new(big.Int).Neg(a.lo)
	}
	return out
}

func (a interval) sub(b interval) interval { return a.add(b.neg()) }

// mul is the product; unbounded if either side is.
func (a interval) mul(b interval) interval {
	if !a.bounded() || !b.bounded() {
		return interval{}
	}
	ps := []*big.Int{
		new(big.Int).Mul(a.lo, b.lo), new(big.Int).Mul(a.lo, b.hi),
		new(big.Int).Mul(a.hi, b.lo), new(big.Int).Mul(a.hi, b.hi),
	}
	lo, hi := ps[0], ps[0]
	for _, p := range ps[1:] {
		lo, hi = minBig(lo, p), maxBig(hi, p)
	}
	return interval{lo: lo, hi: hi}
}

// magnitude is the largest absolute value in a.
func (a interval) magnitude() *big.Int {
	if !a.bounded() {
		return nil
	}
	return maxBig(new(big.Int).Abs(a.lo), new(big.Int).Abs(a.hi))
}

func (a interval) String() string {
	lo, hi := "-inf", "+inf"
	if a.lo != nil {
		lo = a.lo.String()
	}
	if a.hi != nil {
		hi = a.hi.String()
	}
	if lo == hi {
		return lo
	}
	return fmt.Sprintf("[%s, %s]", lo, hi)
}

func minBig(a, b *big.Int) *big.Int {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

func maxBig(a, b *big.Int) *big.Int {
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}

// intervalOf reads the integer bounds of v through Expr(): a conjunction
// intersects, a disjunction takes the hull, bounds and constants set an end,
// and a type (int, number, or a reference such as int32 to its bounds) is
// read as written. exact reports whether v admits exactly the integers in the
// interval, which the target of a computed value must; the source of an operand
// need only be contained. ok is false where v is not integer-kinded.
func intervalOf(v cue.Value) (iv interval, exact bool, ok bool) {
	return intervalOfDepth(v, 0)
}

// maxExprDepth bounds how far intervalOf follows an expression's structure,
// against a value whose Expr() refers back to itself.
const maxExprDepth = 32

func intervalOfDepth(v cue.Value, depth int) (interval, bool, bool) {
	if depth > maxExprDepth {
		return interval{}, false, true
	}
	if _, rp := v.ReferencePath(); len(rp.Selectors()) > 0 {
		if d := cue.Dereference(v); d != v {
			return intervalOfDepth(d, depth+1)
		}
	}
	op, args := v.Expr()
	switch op {
	case cue.NoOp:
		if _, hasDefault := v.Default(); hasDefault && len(args) == 1 {
			return intervalOfDepth(args[0], depth+1)
		}
		k := v.IncompleteKind()
		if k&cue.IntKind == 0 {
			return interval{}, false, false
		}
		if v.IsConcrete() && k == cue.IntKind {
			n, err := v.Int(nil)
			if err != nil {
				return interval{}, false, false
			}
			return point(n), true, true
		}
		if k == cue.IntKind || k == cue.NumberKind {
			return interval{}, true, true
		}
		// Admits non-numbers too (string | int as a single NoOp cannot happen;
		// a validator value): contained, not exact.
		return interval{}, false, true
	case cue.AndOp:
		out, exact := interval{}, true
		for _, a := range args {
			iv, ex, ok := intervalOfDepth(a, depth+1)
			if !ok {
				return interval{}, false, false
			}
			out, exact = out.meet(iv), exact && ex
		}
		return out, exact, true
	case cue.OrOp:
		var out interval
		var points []*big.Int
		intArgs, allPoints := 0, true
		var lastExact bool
		for _, a := range args {
			iv, ex, ok := intervalOfDepth(a, depth+1)
			if !ok {
				continue
			}
			if intArgs == 0 {
				out = iv
			} else {
				out = out.hull(iv)
			}
			intArgs++
			lastExact = ex
			if ex && iv.bounded() && iv.lo.Cmp(iv.hi) == 0 {
				points = append(points, iv.lo)
			} else {
				allPoints = false
			}
		}
		if intArgs == 0 {
			return interval{}, false, false
		}
		// Exact where the integer alternatives are consecutive constants, or
		// there is one integer alternative (the others being of other kinds).
		exact := (intArgs == 1 && lastExact) || (allPoints && contiguous(points))
		return out, exact, true
	case cue.GreaterThanEqualOp, cue.GreaterThanOp, cue.LessThanEqualOp, cue.LessThanOp:
		if len(args) != 1 || !args[0].IsConcrete() || args[0].Kind() != cue.IntKind {
			return interval{}, false, true
		}
		n, err := args[0].Int(nil)
		if err != nil {
			return interval{}, false, true
		}
		switch op {
		case cue.GreaterThanEqualOp:
			return interval{lo: n}, true, true
		case cue.GreaterThanOp:
			return interval{lo: new(big.Int).Add(n, big.NewInt(1))}, true, true
		case cue.LessThanEqualOp:
			return interval{hi: n}, true, true
		default:
			return interval{hi: new(big.Int).Sub(n, big.NewInt(1))}, true, true
		}
	}
	// A validator call, != n, a regular expression: contained, not exact.
	return interval{}, false, v.IncompleteKind()&cue.IntKind != 0
}

// contiguous reports whether the points, sorted, are consecutive integers.
func contiguous(ps []*big.Int) bool {
	if len(ps) == 0 {
		return false
	}
	sorted := append([]*big.Int(nil), ps...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].Cmp(sorted[j-1]) < 0; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	one := big.NewInt(1)
	for i := 1; i < len(sorted); i++ {
		if new(big.Int).Sub(sorted[i], sorted[i-1]).Cmp(one) != 0 {
			return false
		}
	}
	return true
}

// shape is what a computed expression may evaluate to: its kind, and for
// integers the interval its values lie in.
type shape struct {
	kind cue.Kind
	iv   interval
	// dead is set where a refined operand has no alternatives left: the
	// expression is in an arm no instance reaches.
	dead bool
}

func (s shape) String() string {
	switch s.kind {
	case cue.IntKind:
		return "an integer in " + s.iv.String()
	case cue.StringKind:
		return "a string"
	case cue.BoolKind:
		return "a bool"
	}
	return s.kind.String()
}

// exprCtx carries what a computed expression's analysis needs from the write
// it belongs to.
type exprCtx struct {
	q      cue.Path
	subj   string
	gs     []pattern
	pos    token.Pos
	paths  map[string]int // occurrences of each input path read
	failed bool
}

func (w *walker) fail(c *exprCtx, verdict Verdict, rule Rule, detail string) {
	c.failed = true
	w.add(Finding{Path: c.subj, Verdict: verdict, Rule: rule, Detail: detail, Pos: c.pos})
}

// computed proves the computed expression x written at q: a head
// ([if c {x}, y][0]), a table (T[input.k]), or an expression over the closed
// operation set whose result the target accepts.
func (w *walker) computed(x ast.Expr, q cue.Path, gs []pattern, pos token.Pos) {
	if hd, ok := asHead(x); ok {
		w.head(hd, q, gs, pos)
		return
	}
	if tb, ok := w.asTable(x); ok {
		w.table(tb, q, gs, pos)
		return
	}
	w.record(&write{q: q, patterns: gs, kind: computedW, text: text(x), pos: pos})
	c := &exprCtx{q: q, subj: w.subject(q), gs: gs, pos: pos, paths: map[string]int{}}
	sh, ok := w.shape(x, c)
	if !ok || sh.dead {
		return
	}
	repeated := false
	for _, n := range c.paths {
		repeated = repeated || n > 1
	}
	w.fits(sh, c, repeated, text(x))
}

// shape analyzes the expression x, reporting what is outside the provable set.
func (w *walker) shape(x ast.Expr, c *exprCtx) (shape, bool) {
	switch e := x.(type) {
	case *ast.ParenExpr:
		return w.shape(e.X, c)
	case *ast.BasicLit:
		switch e.Kind {
		case token.INT:
			n, err := w.ctx.BuildExpr(e).Int(nil)
			if err != nil {
				w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("uses the literal %s, which the checker cannot read as an integer", e.Value))
				return shape{}, false
			}
			return shape{kind: cue.IntKind, iv: point(n)}, true
		case token.STRING:
			return shape{kind: cue.StringKind}, true
		case token.FLOAT:
			w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("uses the float literal %s; floats are outside the provable set, which has integers, strings and bools", e.Value))
			return shape{}, false
		}
	case *ast.Ident:
		if w.predeclared(e) {
			if isBoolName(e.Name) {
				return shape{kind: cue.BoolKind}, true
			}
			w.fail(c, NotTotal, RuleNotAValue, fmt.Sprintf("uses %s, a constraint rather than a value, in a computation", e.Name))
			return shape{}, false
		}
		return w.operand(x, c)
	case *ast.SelectorExpr, *ast.IndexExpr:
		return w.operand(x, c)
	case *ast.UnaryExpr:
		s, ok := w.shape(e.X, c)
		if !ok || s.dead {
			return s, ok
		}
		switch e.Op {
		case token.SUB:
			if s.kind != cue.IntKind {
				w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("negates %s", s))
				return shape{}, false
			}
			return w.arith(c, shape{kind: cue.IntKind, iv: s.iv.neg()}, text(x))
		case token.NOT:
			if s.kind != cue.BoolKind {
				w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("applies ! to %s", s))
				return shape{}, false
			}
			return s, true
		}
		w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("uses the operator %s, which is outside the provable set", e.Op))
		return shape{}, false
	case *ast.BinaryExpr:
		return w.binary(e, c)
	case *ast.Interpolation:
		for _, elt := range e.Elts {
			if lit, ok := elt.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				continue
			}
			s, ok := w.shape(elt, c)
			if !ok {
				return shape{}, false
			}
			if s.dead {
				return s, true
			}
			if s.kind != cue.StringKind && s.kind != cue.IntKind {
				w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("interpolates %s (%s); only strings and integers may be interpolated", text(elt), s))
				return shape{}, false
			}
		}
		return shape{kind: cue.StringKind}, true
	case *ast.CallExpr:
		return w.call(e, c)
	case *ast.BottomLit:
		w.fail(c, NotTotal, RuleNotAValue, "uses _|_, an error, in a computation")
		return shape{}, false
	}
	w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("uses %s, which is outside the provable set", text(x)))
	return shape{}, false
}

// binary analyzes a binary operation. A comparison that classifies as a pattern
// is a bool, with the pattern's own checks.
func (w *walker) binary(e *ast.BinaryExpr, c *exprCtx) (shape, bool) {
	switch e.Op {
	case token.EQL, token.NEQ:
		if gs, ok := w.parsePattern(e); ok {
			w.checkPatterns(gs, c.gs, c.subj, c.pos)
			return shape{kind: cue.BoolKind}, true
		}
	case token.QUO:
		w.fail(c, Unknown, RuleUnknownOp, "divides with /, which yields a float in CUE; use quo or div for integer division")
		return shape{}, false
	case token.AND, token.OR:
		w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("uses %s inside a computation; unify or choose outside the expression", e.Op))
		return shape{}, false
	}
	l, ok := w.shape(e.X, c)
	if !ok {
		return shape{}, false
	}
	r, ok := w.shape(e.Y, c)
	if !ok {
		return shape{}, false
	}
	if l.dead || r.dead {
		return shape{dead: true}, true
	}
	switch e.Op {
	case token.ADD:
		if l.kind == cue.StringKind && r.kind == cue.StringKind {
			return shape{kind: cue.StringKind}, true
		}
		if l.kind == cue.IntKind && r.kind == cue.IntKind {
			return w.arith(c, shape{kind: cue.IntKind, iv: l.iv.add(r.iv)}, text(e))
		}
	case token.SUB:
		if l.kind == cue.IntKind && r.kind == cue.IntKind {
			return w.arith(c, shape{kind: cue.IntKind, iv: l.iv.sub(r.iv)}, text(e))
		}
	case token.MUL:
		if l.kind == cue.IntKind && r.kind == cue.IntKind {
			return w.arith(c, shape{kind: cue.IntKind, iv: l.iv.mul(r.iv)}, text(e))
		}
	case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
		if l.kind == r.kind && (l.kind == cue.IntKind || l.kind == cue.StringKind || (l.kind == cue.BoolKind && (e.Op == token.EQL || e.Op == token.NEQ))) {
			return shape{kind: cue.BoolKind}, true
		}
	case token.LAND, token.LOR:
		if l.kind == cue.BoolKind && r.kind == cue.BoolKind {
			return shape{kind: cue.BoolKind}, true
		}
	default:
		w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("uses the operator %s, which is outside the provable set", e.Op))
		return shape{}, false
	}
	w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("applies %s to %s and %s", e.Op, l, r))
	return shape{}, false
}

// arith checks an integer operation's result: where it has finite bounds, it
// must lie within int64. An unbounded result is carried to the target check,
// which refutes it by a finite side or reports its bounds unknown.
func (w *walker) arith(c *exprCtx, s shape, op string) (shape, bool) {
	if !s.iv.bounded() {
		return s, true
	}
	if !s.iv.int64() {
		verdict, rule := NotTotal, RuleArithOverflow
		for _, n := range c.paths {
			if n > 1 {
				verdict, rule = Unknown, RuleUnknownBounds
			}
		}
		w.fail(c, verdict, rule, fmt.Sprintf("computes %s, whose result may be %s, outside int64; bound the fields it reads so that the result fits", op, s.iv))
		return shape{}, false
	}
	return s, true
}

// operand reads an input path (or a constant reached by reference) used in an
// expression.
func (w *walker) operand(x ast.Expr, c *exprCtx) (shape, bool) {
	conjs := w.tree.Conjuncts(x)
	if len(conjs) == 0 {
		w.fail(c, Unknown, RuleUnknownReference, fmt.Sprintf("reads %s, which the checker cannot follow to the input or to a constant", text(x)))
		return shape{}, false
	}
	var out *shape
	for _, cj := range conjs {
		var s shape
		if w.syn.FromInput(cj) {
			p, ok := w.sourcePath(cj)
			if !ok {
				w.fail(c, Unknown, RuleUnknownReference, fmt.Sprintf("reads %s, a selection into the input the checker cannot follow; select fields by name", text(cj.Expr)))
				return shape{}, false
			}
			c.paths[p.String()]++
			src := w.lookupSource(p, c.gs)
			if !src.declared {
				w.fail(c, NotTotal, RuleSourceUndeclared, fmt.Sprintf("reads %s, which the source schema does not declare", w.inputRef(p)))
				return shape{}, false
			}
			if !src.present {
				w.fail(c, NotTotal, RuleSourceAbsent, fmt.Sprintf("reads %s, which may be absent: %s; write the field under if %s != _|_", w.inputRef(p), src.why, w.inputRef(p)))
				return shape{}, false
			}
			s, ok = w.shapeOf(src.branches, p, c)
			if !ok {
				return shape{}, false
			}
		} else {
			switch ex := cj.Expr.(type) {
			case *ast.BasicLit, *ast.UnaryExpr, *ast.Interpolation, *ast.BinaryExpr, *ast.CallExpr, *ast.ParenExpr:
				var ok bool
				s, ok = w.shape(ex, c)
				if !ok {
					return shape{}, false
				}
			case *ast.Ident:
				if w.predeclared(ex) && isBoolName(ex.Name) {
					s = shape{kind: cue.BoolKind}
					break
				}
				w.fail(c, Unknown, RuleUnknownReference, fmt.Sprintf("reads %s, which the checker cannot follow to the input or to a constant", text(x)))
				return shape{}, false
			default:
				if cj.Expr == x {
					w.fail(c, Unknown, RuleUnknownReference, fmt.Sprintf("reads %s, a selection into the input the checker cannot follow (a computed index or label); select fields by name", text(x)))
				} else {
					w.fail(c, Unknown, RuleUnknownReference, fmt.Sprintf("reads %s, which resolves to %s rather than to the input or a scalar constant", text(x), text(cj.Expr)))
				}
				return shape{}, false
			}
		}
		if s.dead {
			continue
		}
		if out == nil {
			out = &s
			continue
		}
		if out.kind != s.kind {
			w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("reads %s, which may be %s or %s; the checker needs operands of one kind: write it under a type test", text(x), *out, s))
			return shape{}, false
		}
		out.iv = out.iv.hull(s.iv)
	}
	if out == nil {
		return shape{dead: true}, true
	}
	return *out, true
}

// shapeOf is the shape of the source alternatives at p: one scalar kind, and
// for integers the hull of their bounds.
func (w *walker) shapeOf(branches []cue.Value, p cue.Path, c *exprCtx) (shape, bool) {
	if len(branches) == 0 {
		return shape{dead: true}, true
	}
	var out shape
	for i, b := range branches {
		k := b.IncompleteKind()
		switch k {
		case cue.IntKind, cue.StringKind, cue.BoolKind:
		default:
			w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("reads %s, which may be %s; computations are over integers, strings and bools (floats and numbers are excluded): write it under a type test, or change the source field", w.inputRef(p), k))
			return shape{}, false
		}
		if i > 0 && out.kind != k {
			w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("reads %s, which may be %s or %s; the checker needs operands of one kind: write it under a type test", w.inputRef(p), out.kind, k))
			return shape{}, false
		}
		out.kind = k
		if k == cue.IntKind {
			iv, _, _ := intervalOf(b)
			if i == 0 {
				out.iv = iv
			} else {
				out.iv = out.iv.hull(iv)
			}
		}
	}
	return out, true
}

// call analyzes a builtin call: len, the integer divisions quo/rem/div/mod,
// and the total string functions of the strings package.
func (w *walker) call(e *ast.CallExpr, c *exprCtx) (shape, bool) {
	switch fun := e.Fun.(type) {
	case *ast.Ident:
		if !w.predeclared(fun) {
			break
		}
		switch fun.Name {
		case "len":
			if len(e.Args) != 1 {
				break
			}
			return w.length(e.Args[0], c)
		case "quo", "rem", "div", "mod":
			if len(e.Args) != divisionArity {
				break
			}
			return w.division(fun.Name, e, c)
		}
	case *ast.SelectorExpr:
		pkg, ok := fun.X.(*ast.Ident)
		if !ok {
			break
		}
		spec, ok := pkg.Node.(*ast.ImportSpec)
		if !ok {
			break
		}
		path, _ := literalString(spec.Path)
		name, _, err := ast.LabelName(fun.Sel)
		if err != nil {
			break
		}
		if path == "strings" {
			if sig, ok := stringFuncs[name]; ok {
				return w.stringCall(e, sig, c)
			}
		}
	}
	w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("calls %s, which is outside the provable set (len; quo, rem, div, mod; %s)", text(e.Fun), stringFuncNames()))
	return shape{}, false
}

// stringSig is the signature of a total string function: its argument kinds
// and result kind.
type stringSig struct {
	args   []cue.Kind
	result cue.Kind
}

var stringFuncs = map[string]stringSig{
	"ToUpper":    {[]cue.Kind{cue.StringKind}, cue.StringKind},
	"ToLower":    {[]cue.Kind{cue.StringKind}, cue.StringKind},
	"TrimSpace":  {[]cue.Kind{cue.StringKind}, cue.StringKind},
	"Trim":       {[]cue.Kind{cue.StringKind, cue.StringKind}, cue.StringKind},
	"TrimLeft":   {[]cue.Kind{cue.StringKind, cue.StringKind}, cue.StringKind},
	"TrimRight":  {[]cue.Kind{cue.StringKind, cue.StringKind}, cue.StringKind},
	"TrimPrefix": {[]cue.Kind{cue.StringKind, cue.StringKind}, cue.StringKind},
	"TrimSuffix": {[]cue.Kind{cue.StringKind, cue.StringKind}, cue.StringKind},
	"Replace":    {[]cue.Kind{cue.StringKind, cue.StringKind, cue.StringKind, cue.IntKind}, cue.StringKind},
	"HasPrefix":  {[]cue.Kind{cue.StringKind, cue.StringKind}, cue.BoolKind},
	"HasSuffix":  {[]cue.Kind{cue.StringKind, cue.StringKind}, cue.BoolKind},
	"Contains":   {[]cue.Kind{cue.StringKind, cue.StringKind}, cue.BoolKind},
}

func stringFuncNames() string {
	names := make([]string, 0, len(stringFuncs))
	for n := range stringFuncs {
		names = append(names, "strings."+n)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	s := ""
	for i, n := range names {
		if i > 0 {
			s += ", "
		}
		s += n
	}
	return s
}

func (w *walker) stringCall(e *ast.CallExpr, sig stringSig, c *exprCtx) (shape, bool) {
	if len(e.Args) != len(sig.args) {
		w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("calls %s with %d arguments, where it takes %d", text(e.Fun), len(e.Args), len(sig.args)))
		return shape{}, false
	}
	for i, a := range e.Args {
		s, ok := w.shape(a, c)
		if !ok {
			return shape{}, false
		}
		if s.dead {
			return s, true
		}
		if s.kind != sig.args[i] {
			w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("passes %s (%s) to %s, which takes %s there", text(a), s, text(e.Fun), sig.args[i]))
			return shape{}, false
		}
	}
	return shape{kind: sig.result}, true
}

// length analyzes len(x): a non-negative integer that, being a Go length,
// fits int64; for a list read from the input, within the list's length bounds.
func (w *walker) length(x ast.Expr, c *exprCtx) (shape, bool) {
	if p, ok := w.pathOf(x); ok {
		src := w.lookupSource(p, c.gs)
		c.paths[p.String()]++
		switch {
		case !src.declared:
			w.fail(c, NotTotal, RuleSourceUndeclared, fmt.Sprintf("reads %s, which the source schema does not declare", w.inputRef(p)))
			return shape{}, false
		case !src.present:
			w.fail(c, NotTotal, RuleSourceAbsent, fmt.Sprintf("reads %s, which may be absent: %s; write the field under if %s != _|_", w.inputRef(p), src.why, w.inputRef(p)))
			return shape{}, false
		}
		var out *interval
		for _, b := range src.branches {
			var iv interval
			switch b.IncompleteKind() {
			case cue.ListKind:
				iv, _, _ = intervalOf(b.Len())
				iv = iv.meet(lengthRange)
			case cue.StringKind, cue.BytesKind:
				iv = lengthRange
			default:
				w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("takes the length of %s, which may be %s; len is provable over strings and lists", w.inputRef(p), b.IncompleteKind()))
				return shape{}, false
			}
			if out == nil {
				out = &iv
			} else {
				h := out.hull(iv)
				out = &h
			}
		}
		if out == nil {
			return shape{dead: true}, true
		}
		return shape{kind: cue.IntKind, iv: *out}, true
	}
	for _, cj := range w.tree.Conjuncts(x) {
		if l, ok := cj.Expr.(*ast.ListLit); ok {
			return w.listLength(l, c)
		}
	}
	s, ok := w.shape(x, c)
	if !ok || s.dead {
		return s, ok
	}
	if s.kind != cue.StringKind {
		w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("takes the length of %s (%s); len is provable over strings and lists read from the input", text(x), s))
		return shape{}, false
	}
	return shape{kind: cue.IntKind, iv: lengthRange}, true
}

// listLength bounds the length of a list literal the lens builds (reached by
// name, through a let): one per written element, and for a comprehension
// over a source list, up to that list's length, or exactly it when nothing
// filters.
func (w *walker) listLength(l *ast.ListLit, c *exprCtx) (shape, bool) {
	total := point(big.NewInt(0))
	for _, elt := range l.Elts {
		switch e := elt.(type) {
		case *ast.Ellipsis:
			w.fail(c, Unknown, RuleUnknownOp, "takes the length of an open list ([...]), which is a constraint rather than a value")
			return shape{}, false
		case *ast.Comprehension:
			var fc *ast.ForClause
			filtered := false
			for _, cl := range e.Clauses {
				switch cc := cl.(type) {
				case *ast.ForClause:
					if fc != nil {
						w.fail(c, Unknown, RuleUnknownOp, "takes the length of a list built by nested for clauses")
						return shape{}, false
					}
					fc = cc
				case *ast.IfClause:
					filtered = true
				}
			}
			if fc == nil {
				w.fail(c, Unknown, RuleUnknownOp, "takes the length of a list built by a comprehension with no for clause")
				return shape{}, false
			}
			p, ok := w.pathOf(fc.Source)
			if !ok {
				w.fail(c, Unknown, RuleUnknownReference, fmt.Sprintf("takes the length of a list built over %s, which the checker cannot follow to a list in the input", text(fc.Source)))
				return shape{}, false
			}
			c.paths[p.String()]++
			src := w.lookupSource(p, c.gs)
			switch {
			case !src.declared:
				w.fail(c, NotTotal, RuleSourceUndeclared, fmt.Sprintf("takes the length of a list built over %s, which the source schema does not declare", w.inputRef(p)))
				return shape{}, false
			case !src.present:
				w.fail(c, NotTotal, RuleSourceAbsent, fmt.Sprintf("takes the length of a list built over %s, which may be absent: %s", w.inputRef(p), src.why))
				return shape{}, false
			}
			var n *interval
			for _, b := range src.branches {
				if b.IncompleteKind() != cue.ListKind {
					w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("takes the length of a list built over %s, which may be %s rather than a list", w.inputRef(p), b.IncompleteKind()))
					return shape{}, false
				}
				iv, _, _ := intervalOf(b.Len())
				iv = iv.meet(lengthRange)
				if filtered {
					iv.lo = big.NewInt(0)
				}
				if n == nil {
					n = &iv
				} else {
					h := n.hull(iv)
					n = &h
				}
			}
			if n != nil {
				total = total.add(*n)
			}
		default:
			total = total.add(point(big.NewInt(1)))
		}
	}
	return shape{kind: cue.IntKind, iv: total.meet(lengthRange)}, true
}

// lengthRange is the range of a length: a Go int, so within int64.
var lengthRange = interval{lo: big.NewInt(0), hi: big.NewInt(math.MaxInt64)}

// division analyzes quo, rem, div or mod: the divisor must exclude zero, and
// the result's magnitude is bounded by the dividend's (quo, div) or the
// divisor's (rem, mod).
func (w *walker) division(name string, e *ast.CallExpr, c *exprCtx) (shape, bool) {
	a, ok := w.shape(e.Args[0], c)
	if !ok {
		return shape{}, false
	}
	b, ok := w.shape(e.Args[1], c)
	if !ok {
		return shape{}, false
	}
	if a.dead || b.dead {
		return shape{dead: true}, true
	}
	if a.kind != cue.IntKind || b.kind != cue.IntKind {
		w.fail(c, Unknown, RuleUnknownOp, fmt.Sprintf("calls %s on %s and %s; it takes integers", name, a, b))
		return shape{}, false
	}
	zero := big.NewInt(0)
	if b.iv.contains(zero) {
		if w.admitsZero(e.Args[1], c) {
			w.fail(c, NotTotal, RuleDivisionByZero, fmt.Sprintf("divides by %s, which may be 0", text(e.Args[1])))
		} else {
			w.fail(c, Unknown, RuleUnknownDivisor, fmt.Sprintf("divides by %s, whose range %s includes 0, though 0 may not be an instance; bound the divisor away from 0 (>0 or <0)", text(e.Args[1]), b.iv))
		}
		return shape{}, false
	}
	var m *big.Int
	switch name {
	case "quo", "div":
		m = a.iv.magnitude()
	default:
		m = b.iv.magnitude()
		if m != nil {
			m = new(big.Int).Sub(m, big.NewInt(1))
		}
	}
	if m == nil {
		return shape{kind: cue.IntKind}, true
	}
	return shape{kind: cue.IntKind, iv: interval{lo: new(big.Int).Neg(m), hi: m}}, true
}

// admitsZero reports whether the divisor expression x certainly admits 0: a
// literal 0, or an input path one of whose alternatives accepts 0.
func (w *walker) admitsZero(x ast.Expr, c *exprCtx) bool {
	if lit, ok := stripParens(x).(*ast.BasicLit); ok {
		return lit.Kind == token.INT && lit.Value == "0"
	}
	p, ok := w.pathOf(x)
	if !ok {
		return false
	}
	zero := w.ctx.CompileString("0")
	for _, b := range w.lookupSource(p, c.gs).branches {
		if b.Unify(zero).Validate(cue.Concrete(true)) == nil {
			return true
		}
	}
	return false
}

// fits checks that the target accepts every value a computed expression may
// take.
func (w *walker) fits(s shape, c *exprCtx, repeated bool, expr string) {
	tgt, ok := w.targetAt(c.q, c.pos)
	if !ok || tgt.IncompleteKind() == cue.TopKind {
		return
	}
	if tgt.IncompleteKind()&s.kind == 0 {
		w.fail(c, NotTotal, RuleComputedNarrowed, fmt.Sprintf("is %s (%s), which the target schema does not accept: it wants %s", s, expr, tgt.IncompleteKind()))
		return
	}
	switch s.kind {
	case cue.BoolKind:
		for _, b := range []string{trueName, falseName} {
			if tgt.Unify(w.ctx.CompileString(b)).Validate(cue.Concrete(true)) != nil {
				w.fail(c, NotTotal, RuleComputedNarrowed, fmt.Sprintf("is a bool (%s) that may be %s, which the target schema (%s) does not accept", expr, b, fmtv(tgt)))
				return
			}
		}
	case cue.StringKind:
		if tgt.Subsume(w.ctx.CompileString("string")) != nil {
			w.fail(c, Unknown, RuleUnknownTargetConstraint, fmt.Sprintf("is a string built by %s, and the target schema accepts only some strings (%s); the checker does not reason about string contents: make the target accept any string, or copy a field the target already constrains", expr, fmtv(tgt)))
		}
	case cue.IntKind:
		tiv, exact, ok := intervalOf(tgt)
		if !ok || !exact {
			w.fail(c, Unknown, RuleUnknownTargetConstraint, fmt.Sprintf("is an integer computed by %s, and the target schema constrains it beyond bounds (%s); express the constraint with >=, <= or a sized type such as int32", expr, fmtv(tgt)))
			return
		}
		if s.iv.within(tiv) && s.iv.int64() {
			return
		}
		if !repeated {
			switch {
			case tiv.lo != nil && s.iv.lo != nil && s.iv.lo.Cmp(tiv.lo) < 0:
				w.fail(c, NotTotal, RuleComputedNarrowed, fmt.Sprintf("is %s, which may be as low as %s, where the target schema requires >=%s", expr, s.iv.lo, tiv.lo))
				return
			case tiv.hi != nil && s.iv.hi != nil && s.iv.hi.Cmp(tiv.hi) > 0:
				w.fail(c, NotTotal, RuleComputedNarrowed, fmt.Sprintf("is %s, which may be as high as %s, where the target schema requires <=%s", expr, s.iv.hi, tiv.hi))
				return
			}
		}
		w.fail(c, Unknown, RuleUnknownBounds, fmt.Sprintf("is an integer computed by %s, in %s, which the checker cannot show within int64 and the target's %s%s; bound the fields it reads (int32, >=0 & <=100)", expr, s.iv, fmtv(tgt), repeatedNote(repeated)))
	}
}

func repeatedNote(repeated bool) string {
	if repeated {
		return " (an operand occurs more than once, and the checker treats each occurrence independently)"
	}
	return ""
}

// head is [if cond {then}, els][0]: then when cond holds, otherwise els.
type head struct {
	cond, then, els ast.Expr
}

// divisionArity is the number of arguments of quo, rem, div and mod.
const divisionArity = 2

// headWithFallback is the length of a head's list when it has a fallback
// element: [if c {x}, y].
const headWithFallback = 2

// asHead recognizes the head idiom.
func asHead(x ast.Expr) (head, bool) {
	ix, ok := x.(*ast.IndexExpr)
	if !ok {
		return head{}, false
	}
	if lit, isLit := ix.Index.(*ast.BasicLit); !isLit || lit.Kind != token.INT || lit.Value != "0" {
		return head{}, false
	}
	l, ok := ix.X.(*ast.ListLit)
	if !ok || len(l.Elts) == 0 || len(l.Elts) > 2 {
		return head{}, false
	}
	comp, ok := l.Elts[0].(*ast.Comprehension)
	if !ok || len(comp.Clauses) != 1 {
		return head{}, false
	}
	ifc, ok := comp.Clauses[0].(*ast.IfClause)
	if !ok {
		return head{}, false
	}
	s, ok := comp.Value.(*ast.StructLit)
	if !ok || len(s.Elts) != 1 {
		return head{}, false
	}
	emb, ok := s.Elts[0].(*ast.EmbedDecl)
	if !ok {
		return head{}, false
	}
	h := head{cond: ifc.Condition, then: emb.Expr}
	if len(l.Elts) == headWithFallback {
		h.els = l.Elts[1]
	}
	return h, true
}

// head proves a head: each arm under its pattern, and the whole as one write.
func (w *walker) head(h head, q cue.Path, gs []pattern, pos token.Pos) {
	subj := w.subject(q)
	full := "[if " + text(h.cond) + " {...}, ...][0]"
	w.record(&write{q: q, patterns: gs, kind: computedW, text: full, pos: pos})
	if h.els == nil {
		w.add(Finding{Path: subj, Verdict: Unknown, Rule: RuleUnknownHead,
			Detail: fmt.Sprintf("takes the first element of a list built by if %s with no fallback, so when the condition is false the list is empty and the index fails; add a fallback element: [if c {x}, y][0]", text(h.cond)), Pos: pos})
		return
	}
	g, ok := w.parsePattern(h.cond)
	if !ok {
		w.add(Finding{Path: subj, Verdict: Unknown, Rule: RuleUnknownPattern,
			Detail: fmt.Sprintf("chooses by %s, which is not a pattern: a presence test (p != _|_), an equality (p == c) or a type test ((p & T) != _|_) on an input field, or a conjunction (&&) of these", text(h.cond)), Pos: pos})
		g = []pattern{{root: w.from, kind: opaque, text: text(h.cond)}}
	}
	w.checkPatterns(g, gs, subj, pos)
	var neg []pattern
	if len(g) == 1 && g[0].kind != opaque {
		neg = []pattern{g[0].negated()}
	} else {
		neg = []pattern{{root: w.from, kind: opaque, text: "!(" + text(h.cond) + ")"}}
	}
	w.quiet++
	defer func() { w.quiet-- }()
	for _, arm := range []struct {
		x  ast.Expr
		gs []pattern
	}{{h.then, append(append([]pattern(nil), gs...), g...)}, {h.els, append(append([]pattern(nil), gs...), neg...)}} {
		for _, c := range w.tree.Conjuncts(arm.x) {
			switch c.Expr.(type) {
			case *ast.StructLit, *ast.ListLit:
				if !w.syn.FromInput(c) {
					w.add(Finding{Path: subj, Verdict: Unknown, Rule: RuleUnknownOp,
						Detail: "chooses between arms one of which is a struct or list literal; the checker proves heads whose arms are scalars or copies", Pos: pos})
					continue
				}
			}
			w.conjunct(c, q, arm.gs, pos)
		}
	}
}

// table is T[input.k]: a struct literal indexed by an input path.
type table struct {
	lit   *ast.StructLit
	index ast.Expr
}

// asTable recognizes a table lookup: an index that is not a literal, into
// something that resolves to a single struct literal.
func (w *walker) asTable(x ast.Expr) (table, bool) {
	ix, ok := x.(*ast.IndexExpr)
	if !ok {
		return table{}, false
	}
	if _, isLit := ix.Index.(*ast.BasicLit); isLit {
		return table{}, false
	}
	conjs := w.tree.Conjuncts(ix.X)
	if len(conjs) != 1 || w.syn.FromInput(conjs[0]) {
		return table{}, false
	}
	lit, ok := conjs[0].Expr.(*ast.StructLit)
	if !ok {
		return table{}, false
	}
	return table{lit: lit, index: ix.Index}, true
}

// table proves a table lookup: the index is an input path whose alternatives
// are string constants, each a key of the table, and each entry taken is
// proven as the field's value under the equality that selects it.
func (w *walker) table(t table, q cue.Path, gs []pattern, pos token.Pos) {
	subj := w.subject(q)
	w.record(&write{q: q, patterns: gs, kind: computedW, text: "{...}[" + text(t.index) + "]", pos: pos})
	p, ok := w.pathOf(t.index)
	if !ok {
		w.add(Finding{Path: subj, Verdict: Unknown, Rule: RuleUnknownTableKey,
			Detail: fmt.Sprintf("indexes a table by %s, which is not a field of the input; index by an input field whose values are string constants", text(t.index)), Pos: pos})
		return
	}
	src := w.lookupSource(p, gs)
	switch {
	case !src.declared:
		w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleSourceUndeclared,
			Detail: fmt.Sprintf("indexes a table by %s, which the source schema does not declare", w.inputRef(p)), Pos: pos})
		return
	case !src.present:
		w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleSourceAbsent,
			Detail: fmt.Sprintf("indexes a table by %s, which may be absent: %s; write the field under if %s != _|_", w.inputRef(p), src.why, w.inputRef(p)), Pos: pos})
		return
	}
	entries := map[string]*ast.Field{}
	for _, fd := range cueview.FieldDecls(t.lit.Elts, w.embedded) {
		if name, _, err := ast.LabelName(fd.Field.Label); err == nil {
			entries[name] = fd.Field
		}
	}
	var keys []string
	for _, b := range src.branches {
		if !compat.ConstantTarget(b) || b.Kind() != cue.StringKind {
			w.add(Finding{Path: subj, Verdict: Unknown, Rule: RuleUnknownTableKey,
				Detail: fmt.Sprintf("indexes a table by %s, which may be %s rather than one of a finite set of string constants; the checker needs every possible key", w.inputRef(p), fmtv(b)), Pos: pos})
			return
		}
		k, _ := b.String()
		if _, ok := entries[k]; !ok {
			w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleTableKeyMissing,
				Detail: fmt.Sprintf("indexes a table by %s, which may be %q, for which the table has no entry", w.inputRef(p), k), Pos: pos})
			return
		}
		keys = append(keys, k)
	}
	w.quiet++
	defer func() { w.quiet-- }()
	for _, k := range keys {
		g := pattern{root: w.from, kind: equality, path: p, lit: fmt.Sprintf("%q", k), val: w.ctx.CompileString(fmt.Sprintf("%q", k))}
		for _, c := range w.tree.Conjuncts(entries[k].Value) {
			w.conjunct(c, q, append(append([]pattern(nil), gs...), g), pos)
		}
	}
}
