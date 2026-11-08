package soundness

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/cueview"
	"github.com/sdboyer/thema/internal/util"
	"github.com/sdboyer/thema/lacuna"
)

// The oracles.
const (
	// S1: translating a valid instance across any edge gives no Internal code
	// (E0304, E0307, E0308, E09xx) and no panic.
	S1 = "S1"
	// S1x: translating a valid instance failed with a code that is not
	// Internal. Translation never fails (#57), so this is a finding too,
	// though not one S1 names.
	S1x = "S1x"
	// S2a: an instance of X.Y that holds only declared data validates
	// against X.(Y+1).
	S2a = "S2a"
	// S2b: AsSuccessor across a minor step succeeds, and raises no lacuna
	// for an instance holding only declared data.
	S2b = "S2b"
	// S2c: the successor sees what the predecessor sees: every leaf of the
	// instance unified with X.Y, defaults taken, is the same leaf of the
	// instance unified with X.(Y+1).
	S2c = "S2c"
	// S3: every leaf of a step's result equals a leaf of the source instance
	// (as given or with its schema's defaults), a literal in the lens, or the
	// target schema's default or constant at that path, or is named by a
	// lacuna's targetFields. Fields the lens computes are not checked.
	S3 = "S3"
	// S4: translation is a function of the instance's state. Two instances
	// of a schema that are equal once unified with it, defaults taken (a
	// field omitted and the same field holding its default), translate to
	// results that are equal once unified with the target, with the same
	// lacunas. docs/translation.md: "a lost default loses nothing", G2. This
	// is the part of S3 that catches a result invented from absence.
	S4 = "S4"
	// G: the generator or Validate panicked.
	G = "G"
)

// Failure is one oracle violation, with its witness.
type Failure struct {
	Oracle   string
	Corpus   string
	Case     string
	From, To string
	Instance string
	Detail   string
}

// Key groups failures that share a cause: the oracle, the case, the step and
// the detail.
func (f Failure) Key() string {
	return strings.Join([]string{f.Oracle, f.Corpus, f.Case, f.From, f.To, f.Detail}, "\x00")
}

// Stats counts what a run over one lineage did.
type Stats struct {
	Schemas      int
	Candidates   int
	Discarded    int
	Instances    int
	LeafPruned   int
	Capped       int
	Unproducible []string
	Edges        int // adjacent steps with a lens, each direction counted
	MinorSteps   int // forward minor steps
	Translations int
	S4Pairs      int // pairs of distinct instances with the same filled state, compared per target
	S3Leaves     int
	S3Computed   int
	Elapsed      time.Duration
}

// Run generates instances of every schema of lin and runs the oracles over
// them.
func Run(corpus, name string, lin thema.Lineage, lim Limits) (Stats, []Failure) {
	start := time.Now()
	r := &runner{corpus: corpus, name: name, lin: lin, ctx: lin.Runtime().Context()}
	g := NewGenerator(r.ctx, lim)
	all := lin.All()
	r.st.Schemas = len(all)
	for i, sch := range all {
		if i+1 < len(all) {
			next := all[i+1]
			if next.Version()[0] == sch.Version()[0] {
				r.st.MinorSteps++
				r.st.Edges++ // the backward lens
			} else {
				r.st.Edges += 2
			}
		}
		var cands []Candidate
		var gs GenStats
		if perr := catch(func() { cands, gs = g.Generate(schemaOf(sch)) }); perr != nil {
			r.fail(Failure{Oracle: G, From: sch.Version().String(), Detail: "generator panicked: " + perr.Error()})
			continue
		}
		r.st.Candidates += len(cands)
		r.st.LeafPruned += gs.LeafPruned
		r.st.Capped += gs.Capped
		for _, u := range gs.Unproducible {
			r.st.Unproducible = append(r.st.Unproducible, sch.Version().String()+" "+u)
		}
		states := map[string][]translated{}
		for _, c := range cands {
			v := r.ctx.BuildExpr(c.Expr)
			var inst *thema.Instance
			var err error
			if perr := catch(func() { inst, err = sch.Validate(v) }); perr != nil {
				r.fail(Failure{Oracle: G, From: sch.Version().String(), Instance: compact(c.Expr), Detail: "Validate panicked: " + perr.Error()})
				continue
			}
			if err != nil {
				r.st.Discarded++
				continue
			}
			r.st.Instances++
			tr := r.instance(sch, inst, c)
			k := stateKey(filled(r.ctx, sch, inst.Underlying()))
			for _, prev := range states[k] {
				r.s4(sch, prev, tr)
			}
			states[k] = append(states[k], tr)
		}
	}
	r.st.Elapsed = time.Since(start)
	return r.st, r.fails
}

type runner struct {
	corpus, name string
	lin          thema.Lineage
	ctx          *cue.Context
	st           Stats
	fails        []Failure
}

func (r *runner) fail(f Failure) {
	f.Corpus, f.Case = r.corpus, r.name
	r.fails = append(r.fails, f)
}

// translated is an instance and what translating it to each target gave.
type translated struct {
	c   Candidate
	out map[thema.SyntacticVersion]*thema.Instance
	lac map[thema.SyntacticVersion]lacuna.TranslationLacunas
}

// stateKey identifies a filled instance by its leaves, empty containers
// included. Equal keys are confirmed leaf by leaf with Equals in s4.
func stateKey(v cue.Value) string {
	var parts []string
	for _, l := range leaves(v, true) {
		parts = append(parts, fmt.Sprintf("%s=%v", l.path, l.v))
	}
	slices.Sort(parts)
	return strings.Join(parts, "\n")
}

func sameLeaves(a, b []leaf) (string, bool) {
	idx := map[string]cue.Value{}
	for _, l := range b {
		idx[l.path] = l.v
	}
	for _, l := range a {
		v, ok := idx[l.path]
		if !ok {
			return fmt.Sprintf("%s is %v in one result and absent in the other", l.path, l.v), false
		}
		if !v.Equals(l.v) {
			return fmt.Sprintf("%s is %v in one result and %v in the other", l.path, l.v, v), false
		}
		delete(idx, l.path)
	}
	for p, v := range idx {
		return fmt.Sprintf("%s is %v in one result and absent in the other", p, v), false
	}
	return "", true
}

func lacunaKey(lac lacuna.TranslationLacunas) string {
	var parts []string
	for _, l := range lac.AsList() {
		var ps []string
		for _, f := range l.SourceFields {
			ps = append(ps, "s:"+f.Path)
		}
		for _, f := range l.TargetFields {
			ps = append(ps, "t:"+f.Path)
		}
		parts = append(parts, string(l.Type)+"("+strings.Join(ps, ",")+")")
	}
	slices.Sort(parts)
	return strings.Join(parts, " ")
}

func (r *runner) s4(sch thema.Schema, a, b translated) {
	if !sameLeaves2(leaves(filled(r.ctx, sch, r.ctx.BuildExpr(a.c.Expr)), true), leaves(filled(r.ctx, sch, r.ctx.BuildExpr(b.c.Expr)), true)) {
		return // a key collision: the states differ
	}
	for to, ao := range a.out {
		bo, ok := b.out[to]
		if !ok {
			continue
		}
		r.st.S4Pairs++
		tsch := ao.Schema()
		w := fmt.Sprintf("%s and %s", compact(a.c.Expr), compact(b.c.Expr))
		if d, ok := sameLeaves(leaves(filled(r.ctx, tsch, ao.Underlying()), true), leaves(filled(r.ctx, tsch, bo.Underlying()), true)); !ok {
			r.fail(Failure{Oracle: S4, From: sch.Version().String(), To: to.String(), Instance: w, Detail: "results differ: " + d})
			continue
		}
		if ka, kb := lacunaKey(a.lac[to]), lacunaKey(b.lac[to]); ka != kb {
			r.fail(Failure{Oracle: S4, From: sch.Version().String(), To: to.String(), Instance: w, Detail: fmt.Sprintf("lacunas differ: [%s] and [%s]", ka, kb)})
		}
	}
}

func sameLeaves2(a, b []leaf) bool {
	_, ok := sameLeaves(a, b)
	return ok
}

func (r *runner) instance(sch thema.Schema, inst *thema.Instance, c Candidate) translated {
	tr := translated{c: c, out: map[thema.SyntacticVersion]*thema.Instance{}, lac: map[thema.SyntacticVersion]lacuna.TranslationLacunas{}}
	from := sch.Version()
	all := r.lin.All()
	var targets []thema.SyntacticVersion
	add := func(s thema.Schema) {
		if s != nil && s.Version() != from && !slices.Contains(targets, s.Version()) {
			targets = append(targets, s.Version())
		}
	}
	add(sch.Predecessor())
	add(sch.Successor())
	add(all[0])
	add(all[len(all)-1])

	for _, to := range targets {
		var out *thema.Instance
		var lac lacuna.TranslationLacunas
		var err error
		r.st.Translations++
		if perr := catch(func() { out, lac, err = inst.Translate(to) }); perr != nil {
			r.fail(Failure{Oracle: S1, From: from.String(), To: to.String(), Instance: compact(c.Expr), Detail: "panic: " + firstLine(perr.Error())})
			continue
		}
		if err != nil {
			oracle := S1x
			if internal(err) {
				oracle = S1
			}
			r.fail(Failure{Oracle: oracle, From: from.String(), To: to.String(), Instance: compact(c.Expr), Detail: describe(err)})
			continue
		}
		tr.out[to], tr.lac[to] = out, lac
		if adjacent(sch, to) {
			r.s3(sch, to, inst, out, lac, c)
		}
	}

	if succ := sch.Successor(); succ != nil && succ.Version()[0] == from[0] {
		r.s2(sch, succ, inst, c)
	}
	return tr
}

func adjacent(sch thema.Schema, to thema.SyntacticVersion) bool {
	if p := sch.Predecessor(); p != nil && p.Version() == to {
		return true
	}
	if s := sch.Successor(); s != nil && s.Version() == to {
		return true
	}
	return false
}

func (r *runner) s2(sch, succ thema.Schema, inst *thema.Instance, c Candidate) {
	from, to := sch.Version().String(), succ.Version().String()
	raw := inst.Underlying()
	if !c.Undeclared {
		if _, err := succ.Validate(raw); err != nil {
			r.fail(Failure{Oracle: S2a, From: from, To: to, Instance: compact(c.Expr), Detail: describe(err)})
		}
	}
	var lac lacuna.TranslationLacunas
	var err error
	if perr := catch(func() { _, lac, err = inst.AsSuccessor() }); perr != nil {
		r.fail(Failure{Oracle: S2b, From: from, To: to, Instance: compact(c.Expr), Detail: "panic: " + firstLine(perr.Error())})
	} else if err != nil {
		r.fail(Failure{Oracle: S2b, From: from, To: to, Instance: compact(c.Expr), Detail: describe(err)})
	} else if l := lac.AsList(); !c.Undeclared && len(l) > 0 {
		r.fail(Failure{Oracle: S2b, From: from, To: to, Instance: compact(c.Expr), Detail: "lacuna on declared data: " + describeLacunas(l)})
	}

	pred := leaves(filled(r.ctx, sch, raw), true)
	next := leaves(filled(r.ctx, succ, raw), true)
	idx := map[string]cue.Value{}
	for _, l := range next {
		idx[l.path] = l.v
	}
	for _, l := range pred {
		nv, ok := idx[l.path]
		switch {
		case !ok:
			r.fail(Failure{Oracle: S2c, From: from, To: to, Instance: compact(c.Expr), Detail: fmt.Sprintf("%s is %v under %s and absent under %s", l.path, l.v, from, to)})
		case !nv.Equals(l.v):
			r.fail(Failure{Oracle: S2c, From: from, To: to, Instance: compact(c.Expr), Detail: fmt.Sprintf("%s is %v under %s and %v under %s", l.path, l.v, from, nv, to)})
		}
	}
}

func (r *runner) s3(sch thema.Schema, to thema.SyntacticVersion, inst, out *thema.Instance, lac lacuna.TranslationLacunas, c Candidate) {
	from := sch.Version()
	target := out.Schema()

	var sources []cue.Value
	for _, l := range leaves(inst.Underlying(), false) {
		sources = append(sources, l.v)
	}
	for _, l := range leaves(filled(r.ctx, sch, inst.Underlying()), true) {
		sources = append(sources, l.v)
	}

	var ov *originView
	var literals []cue.Value
	hasLens := from[0] != to[0] || to.Less(from)
	if hasLens {
		lid := fmt.Sprintf("%d.%d->%d.%d", from[0], from[1], to[0], to[1])
		lens := r.lin.Underlying().LookupPath(cue.MakePath(cue.Str("lenses"), cue.Str(lid)))
		ls, err := cuetil.ParseLens(lens, cuetil.LensParam(from[0], from[1]), cuetil.LensParam(to[0], to[1]))
		if err != nil {
			r.fail(Failure{Oracle: S3, From: from.String(), To: to.String(), Detail: "lens syntax unreadable: " + err.Error()})
			return
		}
		ov = &originView{ls: ls}
		literals = lensLiterals(r.ctx, ls)
	}

	var named []string
	for _, l := range lac.AsList() {
		for _, f := range l.TargetFields {
			named = append(named, f.Path)
		}
	}

	for _, l := range leaves(out.Underlying(), false) {
		if l.empty {
			continue
		}
		r.st.S3Leaves++
		if ov != nil && ov.at(l.sels) == originComputed {
			r.st.S3Computed++
			continue
		}
		if containsEqual(sources, l.v) || containsEqual(literals, l.v) {
			continue
		}
		if sv, ok := schemaAt(r.ctx, schemaOf(target), l.sels); ok {
			if d, ok := sv.Default(); ok && d.Validate(cue.Concrete(true)) == nil && d.Equals(l.v) {
				continue
			}
			if sv.Validate(cue.Concrete(true)) == nil && sv.Equals(l.v) {
				continue
			}
		}
		if covered(named, l.path) {
			continue
		}
		origin := "carried or filled (the lens does not write it)"
		if ov != nil {
			origin = ov.at(l.sels).String()
		}
		r.fail(Failure{Oracle: S3, From: from.String(), To: to.String(), Instance: compact(c.Expr),
			Detail: fmt.Sprintf("result %s is %v, which is no source value, lens literal or target default, and no lacuna names it (%s)", l.path, l.v, origin)})
	}
}

// schemaOf is a schema's value as Validate uses it: the author's schema
// unified with the lineage's joinSchema, as open as the author wrote it.
func schemaOf(sch thema.Schema) cue.Value {
	return sch.Underlying().LookupPath(cue.MakePath(cue.Hid("_schema", util.ThemaPackageID)))
}

// filled is data unified with its schema, closed as Validate closes it: the
// value whose defaults a consumer of the instance sees. Closing matters: an
// open disjunction of structs stays ambiguous where the closed one resolves.
func filled(ctx *cue.Context, sch thema.Schema, data cue.Value) cue.Value {
	return cuetil.CloseRecursive(ctx, schemaOf(sch)).Unify(data)
}

func covered(named []string, path string) bool {
	for _, n := range named {
		if n == path || strings.HasPrefix(path, n+".") || strings.HasPrefix(path, n+"[") {
			return true
		}
	}
	return false
}

func containsEqual(vs []cue.Value, v cue.Value) bool {
	for _, x := range vs {
		if x.Equals(v) {
			return true
		}
	}
	return false
}

// leaf is a concrete scalar of a value, at its path.
type leaf struct {
	path string
	sels []cue.Selector
	v    cue.Value
	// empty marks an empty struct or list, recorded so that a state with
	// one differs from a state without it (docs/translation.md E1).
	empty bool
}

// leaves returns the concrete scalars of v's regular fields and list elements,
// and its empty structs and lists.
// With defaults, each value's default is taken first: the value a consumer of
// the instance sees.
func leaves(v cue.Value, defaults bool) []leaf {
	var out []leaf
	var walk func(v cue.Value, sels []cue.Selector, depth int)
	walk = func(v cue.Value, sels []cue.Selector, depth int) {
		if depth > 32 {
			return
		}
		if defaults {
			v, _ = v.Default()
		}
		switch v.IncompleteKind() {
		case cue.StructKind:
			it, err := v.Fields()
			if err != nil {
				return
			}
			n := 0
			for it.Next() {
				n++
				walk(it.Value(), append(slices.Clip(sels), cue.Str(it.Selector().Unquoted())), depth+1)
			}
			if n == 0 && len(sels) > 0 {
				out = append(out, leaf{path: cue.MakePath(sels...).String(), sels: sels, v: v, empty: true})
			}
		case cue.ListKind:
			it, err := v.List()
			if err != nil {
				return
			}
			i := 0
			for ; it.Next(); i++ {
				walk(it.Value(), append(slices.Clip(sels), cue.Index(i)), depth+1)
			}
			if i == 0 && len(sels) > 0 {
				out = append(out, leaf{path: cue.MakePath(sels...).String(), sels: sels, v: v, empty: true})
			}
		default:
			if v.Validate(cue.Concrete(true)) == nil {
				out = append(out, leaf{path: cue.MakePath(sels...).String(), sels: sels, v: v})
			}
		}
	}
	walk(v, nil, 0)
	return out
}

// schemaAt is the schema's value at the data path sels: a declared field of
// any form, a pattern's value, or a list's element.
func schemaAt(ctx *cue.Context, sch cue.Value, sels []cue.Selector) (cue.Value, bool) {
	v := sch
	for _, s := range sels {
		if s.Type() == cue.IndexLabel {
			if e := v.LookupPath(cue.MakePath(s)); e.Exists() {
				v = e
				continue
			}
			v = v.LookupPath(cue.MakePath(cue.AnyIndex))
			if !v.Exists() {
				return v, false
			}
			continue
		}
		name := s.Unquoted()
		found := false
		if it, err := v.Fields(cue.Optional(true)); err == nil {
			for it.Next() {
				if it.Selector().Unquoted() == name {
					v, found = it.Value(), true
					break
				}
			}
		}
		if !found {
			closed := cuetil.CloseRecursive(ctx, v)
			if !closed.Allows(cue.Str(name)) {
				return v, false
			}
			v = (&Generator{ctx: ctx}).keyValue(closed, name)
		}
	}
	return v, v.Exists()
}

// lensLiterals returns every basic literal written anywhere in the lens, as
// values.
func lensLiterals(ctx *cue.Context, ls *cuetil.LensSyntax) []cue.Value {
	var out []cue.Value
	seen := map[string]bool{}
	for _, c := range ls.Tree().Root() {
		ast.Walk(c.Expr, func(n ast.Node) bool {
			if b, ok := n.(*ast.BasicLit); ok && !seen[b.Value] {
				seen[b.Value] = true
				if v := ctx.BuildExpr(b); v.Err() == nil {
					out = append(out, v)
				}
			}
			return true
		}, nil)
	}
	// A negative literal is written as a unary minus over a basic literal.
	for _, c := range ls.Tree().Root() {
		ast.Walk(c.Expr, func(n ast.Node) bool {
			if u, ok := n.(*ast.UnaryExpr); ok {
				if _, ok := u.X.(*ast.BasicLit); ok {
					if v := ctx.BuildExpr(u); v.Err() == nil {
						out = append(out, v)
					}
				}
			}
			return true
		}, nil)
	}
	return out
}

type origin int

const (
	originUnwritten origin = iota
	originCopied
	originConstant
	originComputed
)

func (o origin) String() string {
	return [...]string{"the lens does not write it", "the lens copies it", "the lens writes it as a constant", "the lens computes it"}[o]
}

// originView reads, from a lens's syntax, where the field at a result path
// comes from. It follows cuetil.LensSyntax's classification (Result), which
// is not exported per path: a conjunct from the lens's input is copied, a
// basic literal is constant, a builtin call or a type is neutral, a struct
// literal is followed into, and anything else is computed. A builtin call is
// neutral on the way down (thema's struct.MinFields(0) on the result) and
// computed at the leaf. A path into a list
// is computed unless the list is copied.
type originView struct {
	ls *cuetil.LensSyntax
}

func (o *originView) at(sels []cue.Selector) origin {
	conjs := o.ls.ResultConjuncts()
	for _, s := range sels {
		var next []cueview.Conjunct
		for _, c := range conjs {
			if o.ls.FromInput(c) {
				return originCopied
			}
			switch e := c.Expr.(type) {
			case *ast.StructLit:
				if s.Type() == cue.IndexLabel {
					continue
				}
				for _, fd := range cueview.FieldDecls(e.Elts, o.embedded) {
					name, _, err := ast.LabelName(fd.Field.Label)
					if err == nil && name == s.Unquoted() {
						next = append(next, o.ls.Tree().Conjuncts(fd.Field.Value)...)
					}
				}
			case *ast.CallExpr, *ast.Ident, *ast.BasicLit:
			default:
				return originComputed
			}
		}
		if len(next) == 0 {
			return originUnwritten
		}
		conjs = next
	}
	res := originUnwritten
	for _, c := range conjs {
		switch {
		case o.ls.FromInput(c):
			if res < originCopied {
				res = originCopied
			}
		case isBasic(c.Expr):
			if res < originConstant {
				res = originConstant
			}
		default:
			if _, ok := c.Expr.(*ast.Ident); ok {
				continue
			}
			return originComputed
		}
	}
	return res
}

func isBasic(x ast.Expr) bool {
	switch e := x.(type) {
	case *ast.BasicLit:
		return true
	case *ast.UnaryExpr:
		_, ok := e.X.(*ast.BasicLit)
		return ok
	}
	return false
}

func (o *originView) embedded(x ast.Expr) []ast.Expr {
	var out []ast.Expr
	for _, c := range o.ls.Tree().Conjuncts(x) {
		if !o.ls.FromInput(c) {
			out = append(out, c.Expr)
		}
	}
	return out
}

// internal reports whether err carries a code that is declared Internal, or
// an E09xx code.
func internal(err error) bool {
	ds := diag.Diagnostics(err)
	if len(ds) == 0 {
		return true // an error without a code is E0903's case
	}
	for _, d := range ds {
		if strings.HasPrefix(string(d.Code), "E09") {
			return true
		}
		if ci, ok := diag.Lookup(d.Code); ok && ci.Internal != "" {
			return true
		}
	}
	return false
}

func describe(err error) string {
	ds := diag.Diagnostics(err)
	if len(ds) == 0 {
		return "uncoded: " + firstLine(err.Error())
	}
	var parts []string
	for _, d := range ds {
		parts = append(parts, string(d.Code)+" "+d.Headline)
	}
	slices.Sort(parts)
	return strings.Join(slices.Compact(parts), "; ")
}

func describeLacunas(ls []lacuna.Lacuna) string {
	var parts []string
	for _, l := range ls {
		var paths []string
		for _, f := range l.SourceFields {
			paths = append(paths, f.Path)
		}
		parts = append(parts, fmt.Sprintf("%s(%s)", l.Type, strings.Join(paths, ",")))
	}
	return strings.Join(parts, " ")
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

func compact(x ast.Expr) string {
	return strings.Join(strings.Fields(text(x)), " ")
}

type panicErr struct{ v any }

func (p panicErr) Error() string { return fmt.Sprint(p.v) }

func catch(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicErr{r}
		}
	}()
	f()
	return nil
}
