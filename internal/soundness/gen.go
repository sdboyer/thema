// Package soundness is a spike of the soundness oracles of #324: it generates
// instances of each schema of a bound lineage and runs them through the
// lineage's steps, checking the guarantees binding makes.
//
// The generator builds instances as data (CUE syntax built from literals), and
// decides everything it does on the schema's cue.Value, with its own methods.
// It never decodes a schema into Go.
package soundness

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/cueview"
)

// Limits bound what the generator produces.
type Limits struct {
	// Instances is the most candidates generated for one schema.
	Instances int
	// PerNode is the most alternatives kept for one struct, list or
	// disjunction beneath the root.
	PerNode int
	// PerScalar is the most values kept for one scalar.
	PerScalar int
	// Depth is how deep the generator descends into structs and lists.
	Depth int
}

// DefaultLimits are the limits the corpus run uses.
var DefaultLimits = Limits{Instances: 48, PerNode: 12, PerScalar: 5, Depth: 6}

// WideLimits are about four times DefaultLimits per schema.
var WideLimits = Limits{Instances: 200, PerNode: 32, PerScalar: 10, Depth: 8}

// Candidate is one generated instance, before validation.
type Candidate struct {
	Expr ast.Expr
	// Undeclared is set when the candidate holds a field its schema does not
	// declare: one under ... or matched by a pattern constraint.
	Undeclared bool
	// twin marks the filled twin of another candidate (see Run).
	twin bool
}

// GenStats counts what the generator did for one schema.
type GenStats struct {
	// LeafPruned counts scalar candidates a leaf rejected on its own (a value
	// past a bound, of the wrong kind) and that never reached an instance.
	LeafPruned int
	// Unproducible lists the paths of values the generator could produce no
	// value for, with the schema's text there.
	Unproducible []string
	// Capped counts nodes whose alternatives were cut to the limit.
	Capped int
	// Unfiltered counts scalars no candidate passed on its own, whose
	// candidates were offered to whole-instance validation unfiltered.
	Unfiltered int
}

// Generator generates instances of schemas.
type Generator struct {
	ctx   *cue.Context
	lim   Limits
	stats GenStats
}

// NewGenerator returns a generator over values of ctx.
func NewGenerator(ctx *cue.Context, lim Limits) *Generator {
	return &Generator{ctx: ctx, lim: lim}
}

// alt is one alternative value for a node, as data.
type alt struct {
	x          ast.Expr
	undeclared bool
}

// Generate returns candidate instances of sch, which is a schema's value as
// its author wrote it (not closed), and what the generator did.
func (g *Generator) Generate(sch cue.Value) ([]Candidate, GenStats) {
	g.stats = GenStats{}
	alts := g.values(sch, "", 0, g.lim.Instances)
	out := make([]Candidate, 0, len(alts))
	for _, a := range alts {
		out = append(out, Candidate{Expr: a.x, Undeclared: a.undeclared})
	}
	return out, g.stats
}

func (g *Generator) unproducible(path string, v cue.Value) {
	if path == "" {
		path = "(root)"
	}
	g.stats.Unproducible = append(g.stats.Unproducible, fmt.Sprintf("%s: %v", path, v))
}

// values returns alternatives for v, the base alternative first, at most max
// of them. An empty result means the generator could produce no value.
func (g *Generator) values(v cue.Value, path string, depth, max int) []alt {
	if depth > g.lim.Depth {
		g.unproducible(path, v)
		return nil
	}
	if compat.ConstantTarget(v) {
		if x, ok := data(v); ok {
			return []alt{{x: x}}
		}
	}
	var groups [][]alt
	if d, ok := v.Default(); ok {
		if x, ok := data(d); ok {
			groups = append(groups, []alt{{x: x}})
		} else if d.IncompleteKind()&(cue.StructKind|cue.ListKind) != 0 {
			groups = append(groups, g.values(d, path, depth, max))
		}
	}
	if bs := compat.Branches(v); len(bs) > 1 {
		for _, b := range bs {
			groups = append(groups, g.values(b, path, depth, max))
		}
	} else {
		k := kindOf(v)
		switch {
		case k == cue.StructKind:
			groups = append(groups, g.structs(v, path, depth, max))
		case k == cue.ListKind:
			groups = append(groups, g.lists(v, path, depth, max))
		case k == cue.BottomKind:
		default:
			groups = append(groups, g.scalars(v, k))
		}
	}
	out := g.roundRobin(groups, max)
	if len(out) == 0 {
		g.unproducible(path, v)
	}
	return out
}

// kindOf is v's incomplete kind. A struct or list under a validator that
// needs concrete data (struct.MinFields(1) & {...}, list.MinItems(1) &
// [...int]) reports bottom; its kind is then read from its conjuncts, as
// compat's kindOf does.
func kindOf(v cue.Value) cue.Kind {
	k := v.IncompleteKind()
	if k != cue.BottomKind {
		return k
	}
	if op, args := v.Expr(); op == cue.AndOp {
		for _, a := range args {
			if ak := a.IncompleteKind(); ak != cue.BottomKind && ak != cue.TopKind {
				k |= ak
			}
		}
	}
	return k
}

// roundRobin takes the first alternative of each group, then the second, and
// so on, dropping duplicates, until max are taken.
func (g *Generator) roundRobin(groups [][]alt, max int) []alt {
	var out []alt
	seen := map[string]bool{}
	total := 0
	for _, gr := range groups {
		total += len(gr)
	}
	for i := 0; len(out) < max; i++ {
		any := false
		for _, gr := range groups {
			if i >= len(gr) {
				continue
			}
			any = true
			k := text(gr[i].x)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, gr[i])
			if len(out) == max {
				break
			}
		}
		if !any {
			break
		}
	}
	if len(out) == max && total > max {
		g.stats.Capped++
	}
	return out
}

type field struct {
	label string
	ct    cue.SelectorType
	v     cue.Value
	alts  []alt
}

// names probed for undeclared fields and pattern keys.
var probeKeys = []string{"zz_extra", "k", "key1", "a-b", "x"}

func (g *Generator) structs(v cue.Value, path string, depth, max int) []alt {
	it, err := v.Fields(cue.Optional(true))
	if err != nil {
		return nil
	}
	var fields []field
	declared := map[string]bool{}
	for it.Next() {
		sel := it.Selector()
		label := sel.Unquoted()
		declared[label] = true
		f := field{label: label, ct: sel.ConstraintType(), v: it.Value()}
		f.alts = g.values(f.v, join(path, label), depth+1, g.lim.PerNode)
		if len(f.alts) == 0 && f.ct == cue.RequiredConstraint {
			return nil
		}
		fields = append(fields, f)
	}

	build := func(pick func(i int) (alt, bool), extra ...*ast.Field) alt {
		s := &ast.StructLit{}
		var a alt
		for i := range fields {
			fa, ok := pick(i)
			if !ok {
				continue
			}
			s.Elts = append(s.Elts, &ast.Field{Label: ast.NewString(fields[i].label), Value: fa.x})
			a.undeclared = a.undeclared || fa.undeclared
		}
		for _, e := range extra {
			s.Elts = append(s.Elts, e)
			a.undeclared = true
		}
		a.x = s
		return a
	}
	base := func(i int) (alt, bool) {
		if len(fields[i].alts) == 0 {
			return alt{}, false
		}
		return fields[i].alts[0], true
	}
	minimal := func(i int) (alt, bool) {
		f := fields[i]
		switch f.ct {
		case cue.OptionalConstraint:
			return alt{}, false
		case 0:
			// A regular field that fills by itself can be left out. Asked of
			// the value: a default, or a concrete value. Validate decides the
			// rest.
			if _, ok := f.v.Default(); ok {
				return alt{}, false
			}
			if f.v.Validate(cue.Concrete(true)) == nil {
				return alt{}, false
			}
		}
		return base(i)
	}

	groups := [][]alt{{build(base)}, {build(minimal)}}
	for i, f := range fields {
		var gr []alt
		if f.ct != cue.RequiredConstraint && len(f.alts) > 0 {
			gr = append(gr, build(func(j int) (alt, bool) {
				if j == i {
					return alt{}, false
				}
				return base(j)
			}))
		}
		for _, fa := range f.alts[min(1, len(f.alts)):] {
			gr = append(gr, build(func(j int) (alt, bool) {
				if j == i {
					return fa, true
				}
				return base(j)
			}))
		}
		groups = append(groups, gr)
	}

	// Undeclared data: names the closed schema admits but does not declare.
	closed := cuetil.CloseRecursive(g.ctx, v)
	var pattern, open []string
	for _, k := range probeKeys {
		if declared[k] || !closed.Allows(cue.Str(k)) {
			continue
		}
		kv := g.keyValue(closed, k)
		if kv.IncompleteKind() == cue.BottomKind {
			// Allowed, but refused by a validator on the struct.
			continue
		}
		if kv.IncompleteKind() == cue.TopKind {
			open = append(open, k)
		} else {
			pattern = append(pattern, k)
		}
	}
	var undecl []alt
	if len(open) > 0 {
		k := open[0]
		for _, x := range []ast.Expr{ast.NewString("extra"), ast.NewStruct(&ast.Field{Label: ast.NewString("q"), Value: ast.NewLit(token.INT, "1")})} {
			undecl = append(undecl, build(base, &ast.Field{Label: ast.NewString(k), Value: x}))
		}
	}
	if len(pattern) > 0 {
		var kfs []*ast.Field
		for _, k := range pattern {
			kas := g.values(g.keyValue(closed, k), join(path, k), depth+1, 2)
			if len(kas) == 0 {
				continue
			}
			kfs = append(kfs, &ast.Field{Label: ast.NewString(k), Value: kas[0].x})
			if len(kfs) == 2 {
				break
			}
		}
		for n := 1; n <= len(kfs); n++ {
			undecl = append(undecl, build(base, kfs[:n]...))
		}
	}
	groups = append(groups, undecl)
	return g.roundRobin(groups, max)
}

// keyValue is the value the closed struct gives a field k it does not
// declare: its pattern constraints' values, or _ under ....
func (g *Generator) keyValue(closed cue.Value, k string) cue.Value {
	probe := g.ctx.BuildExpr(ast.NewStruct(&ast.Field{Label: ast.NewString(k), Value: ast.NewIdent("_")}))
	return closed.Unify(probe).LookupPath(cue.MakePath(cue.Str(k)))
}

func (g *Generator) lists(v cue.Value, path string, depth, max int) []alt {
	var prefix [][]alt
	if it, err := v.List(); err == nil {
		i := 0
		for it.Next() {
			as := g.values(it.Value(), fmt.Sprintf("%s[%d]", path, i), depth+1, g.lim.PerNode)
			if len(as) == 0 {
				return nil
			}
			prefix = append(prefix, as)
			i++
		}
	}
	var elems []alt
	elem := v.LookupPath(cue.MakePath(cue.AnyIndex))
	if elem.Exists() {
		elems = g.values(elem, path+"[_]", depth+1, g.lim.PerNode)
	}

	build := func(pre func(i int) alt, tail ...alt) alt {
		l := &ast.ListLit{}
		var a alt
		for i := range prefix {
			e := pre(i)
			l.Elts = append(l.Elts, e.x)
			a.undeclared = a.undeclared || e.undeclared
		}
		for _, e := range tail {
			l.Elts = append(l.Elts, e.x)
			a.undeclared = a.undeclared || e.undeclared
		}
		a.x = l
		return a
	}
	base := func(i int) alt { return prefix[i][0] }

	var groups [][]alt
	if len(elems) == 0 {
		groups = append(groups, []alt{build(base)})
	} else {
		// Lengths: the prefix alone, one more and three more.
		groups = append(groups, []alt{build(base), build(base, elems[0]), build(base, elems[0], elems[0], elems[0])})
		var gr []alt
		for _, e := range elems[1:] {
			gr = append(gr, build(base, e))
		}
		if len(elems) > 1 {
			gr = append(gr, build(base, elems...))
		}
		groups = append(groups, gr)
	}
	for i := range prefix {
		var gr []alt
		for _, e := range prefix[i][1:] {
			gr = append(gr, build(func(j int) alt {
				if j == i {
					return e
				}
				return base(j)
			}))
		}
		groups = append(groups, gr)
	}
	return g.roundRobin(groups, max)
}

// pools of scalar candidates, by kind, tried in order after the values read
// from the schema's bounds.
var (
	intPool = []string{"0", "1", "-1", "2", "7", "10", "100", "-100", "127", "128", "-128", "-129", "255", "256",
		"32767", "32768", "65535", "65536", "2147483647", "2147483648", "-2147483649",
		"4294967296", "9007199254740993", "9223372036854775807", "9223372036854775808", "-9223372036854775809"}
	floatPool  = []string{"0.0", "0.5", "1.5", "-1.5", "3.4028236e38", "1e300"}
	stringPool = []string{"a", "", "abc", "hello world", "x", "A", "0", "a-b",
		"2006-01-02T15:04:05Z", "foo.example.com", "127.0.0.1", "10.0.0.0/8",
		"https://example.com/", "{}", "é", "8.8.8.8", "224.0.0.1", "0.0.0.0", "::", "fe80::1", "ff01::1", "ff02::1"}
)

func (g *Generator) scalars(v cue.Value, k cue.Kind) []alt {
	var cands []ast.Expr
	bounds, patterns := readBounds(v, 0)
	for _, b := range bounds {
		cands = append(cands, b...)
	}
	for _, p := range patterns {
		cands = append(cands, ast.NewString(p))
	}
	if k&cue.BoolKind != 0 {
		cands = append(cands, ast.NewBool(true), ast.NewBool(false))
	}
	if k&cue.NullKind != 0 {
		cands = append(cands, ast.NewNull())
	}
	if k&cue.IntKind != 0 {
		for _, s := range intPool {
			cands = append(cands, intLit(s))
		}
	}
	if k&cue.FloatKind != 0 {
		for _, s := range floatPool {
			cands = append(cands, &ast.BasicLit{Kind: token.FLOAT, Value: s})
		}
	}
	if k&cue.StringKind != 0 {
		for _, s := range stringPool {
			cands = append(cands, ast.NewString(s))
		}
	}
	if k&cue.BytesKind != 0 {
		cands = append(cands, &ast.BasicLit{Kind: token.STRING, Value: "''"}, &ast.BasicLit{Kind: token.STRING, Value: "'a'"})
	}
	if k == cue.TopKind {
		cands = append(cands, ast.NewStruct(), ast.NewList())
	}
	var out []alt
	seen := map[string]bool{}
	for _, c := range cands {
		key := text(c)
		if seen[key] {
			continue
		}
		seen[key] = true
		// Decided on the value: the candidate is kept when unifying it with
		// the schema is no conflict. A value the schema leaves incomplete
		// (a reference to another field) is kept, and the whole instance's
		// validation decides it.
		if err := v.Unify(g.ctx.BuildExpr(c)).Validate(); err != nil {
			g.stats.LeafPruned++
			continue
		}
		out = append(out, alt{x: c})
		if len(out) == g.lim.PerScalar {
			break
		}
	}
	if len(out) == 0 {
		// Nothing passed on its own: the value may depend on other fields
		// (b: >=a). Offer the first candidates anyway, and let the whole
		// instance's validation decide.
		g.stats.Unfiltered++
		for _, c := range cands {
			if v.Unify(g.ctx.BuildExpr(c)).IncompleteKind() == cue.BottomKind {
				continue
			}
			out = append(out, alt{x: c})
			if len(out) == g.lim.PerScalar {
				break
			}
		}
	}
	return out
}

func intLit(s string) ast.Expr {
	if strings.HasPrefix(s, "-") {
		return &ast.UnaryExpr{Op: token.SUB, X: &ast.BasicLit{Kind: token.INT, Value: s[1:]}}
	}
	return &ast.BasicLit{Kind: token.INT, Value: s}
}

// readBounds reads the numeric bounds of v and the literal text of its regular
// expressions, from its expression. For each bound b it returns b-1, b and b+1
// (for a float bound, b and b±0.5), grouped by bound. It reads only what Expr
// shows; a bound it misses is covered only by the pools.
func readBounds(v cue.Value, depth int) (bounds [][]ast.Expr, patterns []string) {
	if depth > 8 {
		return nil, nil
	}
	op, args := v.Expr()
	switch op {
	case cue.AndOp, cue.OrOp:
		for _, a := range args {
			b, p := readBounds(a, depth+1)
			bounds = append(bounds, b...)
			patterns = append(patterns, p...)
		}
	case cue.LessThanOp, cue.LessThanEqualOp, cue.GreaterThanOp, cue.GreaterThanEqualOp, cue.NotEqualOp:
		if len(args) != 1 {
			return
		}
		a := args[0]
		switch a.Kind() {
		case cue.IntKind:
			var n big.Int
			if _, err := a.Int(&n); err != nil {
				return
			}
			var grp []ast.Expr
			for _, d := range []int64{-1, 0, 1} {
				m := new(big.Int).Add(&n, big.NewInt(d))
				grp = append(grp, intLit(m.String()))
			}
			bounds = append(bounds, grp)
		case cue.FloatKind:
			f, err := a.Float64()
			if err != nil {
				return
			}
			var grp []ast.Expr
			for _, d := range []float64{-0.5, 0, 0.5} {
				s := strconv.FormatFloat(f+d, 'g', -1, 64)
				if !strings.ContainsAny(s, ".e") {
					s += ".0"
				}
				if strings.HasPrefix(s, "-") {
					grp = append(grp, &ast.UnaryExpr{Op: token.SUB, X: &ast.BasicLit{Kind: token.FLOAT, Value: s[1:]}})
				} else {
					grp = append(grp, &ast.BasicLit{Kind: token.FLOAT, Value: s})
				}
			}
			bounds = append(bounds, grp)
		case cue.StringKind:
			s, _ := a.String()
			patterns = append(patterns, s)
		}
	case cue.RegexMatchOp:
		if len(args) == 1 {
			if s, err := args[0].String(); err == nil {
				patterns = append(patterns, strings.Trim(s, "^$"))
			}
		}
	}
	return
}

// data returns a concrete scalar or a concrete list or struct as data.
func data(v cue.Value) (ast.Expr, bool) {
	n, err := cueview.Data(v)
	if err != nil {
		return nil, false
	}
	x, ok := n.(ast.Expr)
	return x, ok
}

func text(x ast.Expr) string {
	s, err := cueview.Text(x)
	if err != nil {
		return fmt.Sprintf("%#v", x)
	}
	return s
}

func join(path, label string) string {
	if path == "" {
		return label
	}
	return path + "." + label
}
