// Package compat decides whether one thema schema is backwards compatible with
// another.
//
// A successor schema (new) is backwards compatible with its predecessor (old)
// iff
//
//  1. every instance valid under old is valid under new, and
//  2. for every valid old instance, every path where old fills a default gets
//     the same value under new.
//
// Both are judged on concrete data: what a consumer outside CUE can observe.
// Hidden fields, definitions and lets are not data and are ignored; a
// reference's meaning is the value it refers to.
//
// The check is a structural walk over both values, querying them through the
// cue API ([cue.Value.Fields] with each selector's constraint type,
// [cue.Value.Default], [cue.Value.Expr], [cue.Value.Allows], [cue.Value.Len]).
// CUE's subsumption is used only on leaves, where it is sound. Verdicts are
// three-valued: where the walk cannot decide, it says so ([Unknown]) rather
// than guess. Each verdict carries the [Rule] that produced it, so that the
// rules can be documented and referred to; see docs/invariants.md.
package compat

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"cuelang.org/go/cue"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/cueview"
)

// Verdict is the outcome of a compatibility check, for one path or overall.
type Verdict int

const (
	// Compatible means every predecessor instance is a successor instance, with
	// the same defaults.
	Compatible Verdict = iota
	// Incompatible means some predecessor instance is not a successor instance,
	// or gets a different default.
	Incompatible
	// Unknown means the checker cannot decide. For versioning, Unknown counts
	// as Incompatible, and the rule says what the author can do about it.
	Unknown
)

func (v Verdict) String() string {
	switch v {
	case Compatible:
		return "compatible"
	case Incompatible:
		return "incompatible"
	default:
		return "unknown"
	}
}

// Rule identifies the rule a finding comes from. Rules are stable identifiers
// documented in docs/invariants.md; feedback to authors refers to them.
type Rule string

// Rules that find an incompatibility.
const (
	// RuleFieldAddedRequired: the successor requires a field instances of the
	// predecessor may omit.
	RuleFieldAddedRequired Rule = "field-added-required"
	// RuleFieldRemoved: the predecessor declares a field the successor rejects.
	RuleFieldRemoved Rule = "field-removed"
	// RuleFieldNowRequired: an optional or defaulted field became required.
	RuleFieldNowRequired Rule = "field-now-required"
	// RuleDefaultRemoved: a defaulted field became optional, so an absent field
	// is no longer filled.
	RuleDefaultRemoved Rule = "default-removed"
	// RuleDefaultChanged: a defaulted field fills with a different value.
	RuleDefaultChanged Rule = "default-changed"
	// RuleValueNarrowed: the successor does not accept every value the
	// predecessor does at a path.
	RuleValueNarrowed Rule = "value-narrowed"
	// RuleLengthNarrowed: a list's length bounds narrowed.
	RuleLengthNarrowed Rule = "length-narrowed"
	// RuleLabelsNarrowed: the predecessor accepts labels (through ... or a
	// pattern) the successor rejects.
	RuleLabelsNarrowed Rule = "labels-narrowed"
	// RuleValidatorAdded: the successor adds or tightens a builtin validator
	// the checker knows.
	RuleValidatorAdded Rule = "validator-added"
)

// Rules that leave the verdict Unknown.
const (
	// RuleUnknownCrossField: the successor constrains a field by another
	// field's value, differently from the predecessor.
	RuleUnknownCrossField Rule = "unknown-crossfield"
	// RuleUnknownComprehension: a comprehension over data decides a struct's
	// fields, differently between the versions.
	RuleUnknownComprehension Rule = "unknown-comprehension"
	// RuleUnknownValidator: the successor adds or changes a validator the
	// checker has no rule for.
	RuleUnknownValidator Rule = "unknown-validator"
	// RuleUnknownDisjunction: no single alternative of the successor covers an
	// alternative of the predecessor, though together they may.
	RuleUnknownDisjunction Rule = "unknown-disjunction"
	// RuleUnknownPatterns: pattern constraints differ beyond what the checker
	// compares.
	RuleUnknownPatterns Rule = "unknown-patterns"
)

// Finding is one verdict at one path. Compatible paths produce no finding.
//
// Its text reads as a sentence about the value at Path: "<path> <detail>
// (rule <rule>)". The two schemas are "the prior schema" and "the new
// schema".
type Finding struct {
	Path    cue.Path
	Verdict Verdict
	Rule    Rule
	// Detail says what differs between the schemas at Path, as a predicate
	// whose subject is the value there: "is required in the new schema but
	// not declared in the prior one".
	Detail string
	// Pos is the author's position for the finding: the field in new, or in old
	// for a field new lacks.
	Pos token.Pos
	// ViaResidual: the prior schema accepts the field at Path only through its
	// ... or a pattern constraint, not by name. The new schema claims the name,
	// and whatever it declares narrows what the prior schema already admitted
	// there. It explains to an author why adding a typed optional field under
	// ... is breaking.
	ViaResidual bool
}

func (f Finding) String() string {
	var b strings.Builder
	subject := "the top level"
	if len(f.Path.Selectors()) > 0 {
		subject = f.Path.String()
	}
	fmt.Fprintf(&b, "%s %s (rule %s)", subject, f.Detail, f.Rule)
	return b.String()
}

// Result is the outcome of [Check]: the findings at every path that is not
// Compatible.
type Result struct {
	Findings []Finding
}

// Verdict is the overall verdict: Incompatible if any path is, since one
// counterexample settles it; otherwise Unknown if any path is; otherwise
// Compatible.
func (r Result) Verdict() Verdict {
	v := Compatible
	for _, f := range r.Findings {
		switch f.Verdict {
		case Incompatible:
			return Incompatible
		case Unknown:
			v = Unknown
		}
	}
	return v
}

// Compatible reports whether the overall verdict is Compatible.
func (r Result) Compatible() bool {
	return r.Verdict() == Compatible
}

// Instances keeps only the findings about clause 1 of compatibility: whether
// every instance of the predecessor is an instance of the successor. A
// default-removed finding is dropped: it concerns what an absent field means,
// not which instances are accepted. A default-changed finding is kept: an
// instance that omits the field carries the predecessor's default as a
// disjunction (*1 | int), which unified with the successor's (*2 | int) has
// two defaults and no value.
//
// A lens copy is judged by this clause. Translate fills the lens's input with
// the instance unified with the source schema, so a copied field the instance
// omitted arrives as that disjunction, not as the default itself.
func (r Result) Instances() Result {
	var out Result
	for _, f := range r.Findings {
		if f.Rule == RuleDefaultRemoved {
			continue
		}
		out.Findings = append(out.Findings, f)
	}
	return out
}

// Error returns the findings as a CUE error list positioned at the authors'
// fields, or nil if Compatible.
func (r Result) Error() error {
	if len(r.Findings) == 0 {
		return nil
	}
	var errs cerrors.Error
	for _, f := range r.Findings {
		errs = cerrors.Append(errs, cerrors.Newf(f.Pos, "%s", f.String()))
	}
	return errs
}

// Check decides whether new is backwards compatible with old. Both are closed
// recursively first, whatever openness their authors wrote: thema's versioning
// is closed-world, and an author's explicit ... survives closing.
func Check(ctx *cue.Context, pred, succ cue.Value) Result {
	w := &walker{
		ctx:     ctx,
		oldRoot: cuetil.CloseRecursive(ctx, pred),
		newRoot: cuetil.CloseRecursive(ctx, succ),
		top:     ctx.CompileString("_"),
	}
	w.value(cue.MakePath(), w.oldRoot, w.newRoot)
	sort.SliceStable(w.findings, func(i, j int) bool {
		return pathString(w.findings[i].Path) < pathString(w.findings[j].Path)
	})
	return Result{Findings: w.findings}
}

type walker struct {
	ctx              *cue.Context
	oldRoot, newRoot cue.Value
	top              cue.Value
	findings         []Finding
	// stack holds the reference pairs being compared, to end the walk of
	// recursive schemas (next?: #Node) where it reenters itself.
	stack []string
}

func (w *walker) add(f Finding) {
	w.findings = append(w.findings, f)
}

// scratch runs fn against a fresh findings list and returns what it found.
func (w *walker) scratch(fn func()) []Finding {
	saved := w.findings
	w.findings = nil
	fn()
	got := w.findings
	w.findings = saved
	return got
}

func pathString(p cue.Path) string {
	if len(p.Selectors()) == 0 {
		return "<root>"
	}
	return p.String()
}

func posOf(vs ...cue.Value) token.Pos {
	for _, v := range vs {
		if v.Exists() && v.Pos().IsValid() {
			return v.Pos()
		}
	}
	return token.NoPos
}

// value compares the constraint old places at p with the one new places there.
func (w *walker) value(p cue.Path, pred, succ cue.Value) {
	if key, ok := w.refPair(pred, succ); ok {
		for _, s := range w.stack {
			if s == key {
				return
			}
		}
		w.stack = append(w.stack, key)
		defer func() { w.stack = w.stack[:len(w.stack)-1] }()
	}

	// A reference to another data field makes the constraint depend on the
	// instance. Identical in both versions is fine; anything else is beyond
	// the checker.
	orefs := w.dataRefs(pred, w.oldRoot, nil)
	nrefs := w.dataRefs(succ, w.newRoot, nil)
	if len(orefs) > 0 || len(nrefs) > 0 {
		if !w.sameExpr(pred, succ) {
			var detail string
			switch {
			case len(orefs) > 0 && len(nrefs) > 0 && joinPaths(orefs) == joinPaths(nrefs):
				detail = fmt.Sprintf("is constrained by %s in both schemas, differently", joinPaths(orefs))
			case len(orefs) > 0 && len(nrefs) > 0:
				detail = fmt.Sprintf("is constrained by %s in the prior schema and by %s in the new one", joinPaths(orefs), joinPaths(nrefs))
			case len(nrefs) > 0:
				detail = fmt.Sprintf("is constrained by %s in the new schema and not in the prior one", joinPaths(nrefs))
			default:
				detail = fmt.Sprintf("is constrained by %s in the prior schema and not in the new one", joinPaths(orefs))
			}
			w.add(Finding{
				Path: p, Verdict: Unknown, Rule: RuleUnknownCrossField,
				Detail: detail + "; the checker cannot compare constraints between fields",
				Pos:    posOf(succ, pred),
			})
		}
		return
	}

	ob, nb := branches(pred), branches(succ)
	switch {
	case len(nb) == 1:
		// Every old branch must fit the one new constraint.
		for _, o := range ob {
			w.single(p, o, nb[0], succ)
		}
	default:
		// Each old branch must be covered by some new branch. Finding none is
		// not a counterexample: the branches together may still cover it.
		for _, o := range ob {
			covered := false
			for _, n := range nb {
				if len(w.scratch(func() { w.single(p, o, n, succ) })) == 0 {
					covered = true
					break
				}
			}
			if covered {
				continue
			}
			// A concrete old alternative new rejects, or one whose kind no new
			// alternative shares, is a counterexample.
			if o.Validate(cue.Concrete(true)) == nil && succ.Unify(o).Validate() != nil {
				w.add(Finding{
					Path: p, Verdict: Incompatible, Rule: RuleValueNarrowed,
					Detail: fmt.Sprintf("is now %s, which has no alternative for the prior %s", fmtv(succ), fmtv(o)),
					Pos:    posOf(succ, pred),
				})
				continue
			}
			if ok, nk := kindOf(o), kindOf(succ); ok != cue.BottomKind && ok&nk == 0 {
				w.add(Finding{
					Path: p, Verdict: Incompatible, Rule: RuleValueNarrowed,
					Detail: fmt.Sprintf("is now %s, no alternative of which has the kind %s of the prior alternative %s", fmtv(succ), ok, fmtv(o)),
					Pos:    posOf(succ, pred),
				})
				continue
			}
			// A value of the old alternative that new rejects is a
			// counterexample too: string against "a" | "b" admits "".
			if w.rejectsProbe(o, succ, nb) {
				w.add(Finding{
					Path: p, Verdict: Incompatible, Rule: RuleValueNarrowed,
					Detail: fmt.Sprintf("is now %s, which does not accept every value of the prior %s", fmtv(succ), fmtv(o)),
					Pos:    posOf(succ, pred),
				})
				continue
			}
			w.add(Finding{
				Path: p, Verdict: Unknown, Rule: RuleUnknownDisjunction,
				Detail: fmt.Sprintf("is now %s, no single alternative of which covers the prior alternative %s; the checker does not combine alternatives", fmtv(succ), fmtv(o)),
				Pos:    posOf(succ, pred),
			})
		}
	}
}

// rejectsProbe reports whether one of a few probe values is an instance of the
// predecessor alternative pred that succ rejects: a counterexample. For each
// scalar kind pred admits, the probe is the first of a fixed sequence of
// values of that kind ("", "x", "xx", ...; 0, 1, 2, ...; 0.0, 1.0, ...) that
// equals no concrete alternative of succ (alts), so that string against
// "a" | "b", or int against 0 | 1, finds one. A probe pred rejects decides
// nothing: the alternatives may still cover pred together (=~"^(a|b)$"
// against "a" | "b").
func (w *walker) rejectsProbe(pred, succ cue.Value, alts []cue.Value) bool {
	for _, probe := range w.probes(kindOf(pred), alts) {
		if pred.Unify(probe).Validate(cue.Concrete(true)) == nil && succ.Unify(probe).Validate() != nil {
			return true
		}
	}
	return false
}

// probes returns, for each scalar kind in k, a value of that kind that equals
// no concrete alternative in alts; both booleans for bool, and null for null.
func (w *walker) probes(k cue.Kind, alts []cue.Value) []cue.Value {
	taken := map[string]bool{}
	for _, a := range alts {
		if a.Validate(cue.Concrete(true)) == nil {
			taken[fmtv(a)] = true
		}
	}
	// next returns the first of the values gen(0), gen(1), ... that no
	// alternative takes. alts is finite, so the search ends.
	next := func(gen func(i int) any) cue.Value {
		for i := 0; ; i++ {
			v := w.ctx.Encode(gen(i))
			if !taken[fmtv(v)] {
				return v
			}
		}
	}
	var out []cue.Value
	if k&cue.NullKind != 0 {
		out = append(out, w.ctx.CompileString("null"))
	}
	if k&cue.BoolKind != 0 {
		out = append(out, w.ctx.Encode(true), w.ctx.Encode(false))
	}
	if k&cue.IntKind != 0 {
		out = append(out, next(func(i int) any { return i }))
	}
	if k&cue.FloatKind != 0 {
		out = append(out, next(func(i int) any { return float64(i) }))
	}
	if k&cue.StringKind != 0 {
		out = append(out, next(func(i int) any { return strings.Repeat("x", i) }))
	}
	if k&cue.BytesKind != 0 {
		out = append(out, next(func(i int) any { return []byte(strings.Repeat("x", i)) }))
	}
	return out
}

// refPair returns a key for old and new when both are references, so that a
// recursive schema's reentry can be recognized.
func (w *walker) refPair(pred, succ cue.Value) (string, bool) {
	_, op := pred.ReferencePath()
	_, np := succ.ReferencePath()
	if len(op.Selectors()) == 0 || len(np.Selectors()) == 0 {
		return "", false
	}
	return op.String() + " / " + np.String(), true
}

// Branches splits v into its disjunction alternatives, with defaults removed:
// Expr reports *1 | int as the single alternative int, and *"a" | "a" | "b" as
// "a" and "b". Defaults are compared separately, by the field that holds them.
// A reference is split as the value it refers to, since Expr would report
// only the reference; a builtin validator value (time.Time) is kept as is, so
// that it is still read as a validator. A conjunction with a disjunctive
// operand, (A | B) & C, is the alternatives of that operand each unified with
// the rest, A & C and B & C: Expr reports the conjunction, not the
// disjunction inside it. The closing wrapper (cuetil.CloseRecursive) is such a
// conjunction, _ & v, so a schema that is a disjunction at its top is split
// here like one beneath a field; _ says nothing and is not unified in.
func Branches(v cue.Value) []cue.Value {
	return branches(v)
}

// maxBranchesDepth bounds how far branches follows an expression's structure.
const maxBranchesDepth = 16

func branches(v cue.Value) []cue.Value {
	return branchesDepth(v, 0)
}

func branchesDepth(v cue.Value, depth int) []cue.Value {
	if depth > maxBranchesDepth {
		return []cue.Value{v}
	}
	if _, rp := v.ReferencePath(); len(rp.Selectors()) > 0 {
		if _, builtin := packageMember(v); !builtin {
			v = cue.Dereference(v)
		}
	}
	op, args := v.Expr()
	switch op {
	case cue.OrOp:
		var out []cue.Value
		for _, a := range args {
			out = append(out, branchesDepth(a, depth+1)...)
		}
		return out
	case cue.AndOp:
		for i, a := range args {
			alts := branchesDepth(a, depth+1)
			if len(alts) <= 1 {
				continue
			}
			var rest cue.Value
			for j, b := range args {
				if j == i || b.IncompleteKind() == cue.TopKind {
					continue
				}
				if !rest.Exists() {
					rest = b
				} else {
					rest = rest.Unify(b)
				}
			}
			if !rest.Exists() {
				return alts
			}
			out := make([]cue.Value, len(alts))
			for k, alt := range alts {
				out[k] = alt.Unify(rest)
			}
			return out
		}
	case cue.NoOp:
		if len(args) == 1 {
			return []cue.Value{args[0]}
		}
	}
	return []cue.Value{v}
}

// single compares one predecessor alternative with one successor alternative.
// wholeSucc is the successor value the alternative came from, for positions.
func (w *walker) single(p cue.Path, pred, succ, wholeSucc cue.Value) {
	okind, nkind := kindOf(pred), kindOf(succ)
	switch {
	case okind == cue.StructKind && nkind == cue.StructKind:
		w.structs(p, pred, succ)
	case okind == cue.ListKind && nkind == cue.ListKind:
		w.lists(p, pred, succ)
	case okind != cue.BottomKind && okind&nkind == 0:
		w.add(Finding{
			Path: p, Verdict: Incompatible, Rule: RuleValueNarrowed,
			Detail: fmt.Sprintf("was %s and is now %s", okind, nkind),
			Pos:    posOf(wholeSucc),
		})
	default:
		w.leaf(p, pred, succ, wholeSucc)
	}
}

// kindOf is v's incomplete kind, recovered from its operands when an
// incomplete error hides it: a struct with a pending comprehension, or a list
// under a validator that needs concrete elements, reports bottom while its
// fields remain iterable.
func kindOf(v cue.Value) cue.Kind {
	k := v.IncompleteKind()
	if k != cue.BottomKind {
		return k
	}
	op, args := v.Expr()
	if op == cue.AndOp {
		for _, a := range args {
			// The closed wrapper conjoins _ with the value; _ says nothing.
			if ak := kindOf(a); ak != cue.BottomKind && ak != cue.TopKind {
				k |= ak
			}
		}
		if k != cue.BottomKind {
			return k
		}
	}
	if _, err := v.Fields(); err == nil {
		return cue.StructKind
	}
	return cue.BottomKind
}

func isStruct(v cue.Value) bool {
	return kindOf(v) == cue.StructKind
}

// pending reports whether v, a struct, has a comprehension that cannot fire
// without data: its fields then depend on the instance.
func pending(v cue.Value) bool {
	return isStruct(v) && v.Err() != nil
}

// Form is what concrete data can observe about a field: whether it may be
// absent, and if so, what the consumer sees. See docs/invariants.md, "Field
// forms".
type Form int

const (
	// Required means an instance must supply the field.
	Required Form = iota
	// Optional means an instance may omit the field, and it then stays absent.
	Optional
	// Defaulted means an instance may omit the field, and the consumer then sees
	// the value the schema fills in.
	Defaulted
)

func (c Form) String() string {
	return [...]string{"required", "optional", "defaulted"}[c]
}

// FormOf derives the form of the field sel with value v, by the rules of the
// field forms table in docs/invariants.md.
func FormOf(sel cue.Selector, v cue.Value) Form {
	return classify(sel, v).class
}

// fieldClass is the internal name of Form.
type fieldClass = Form

const (
	required  = Required
	optional  = Optional
	defaulted = Defaulted
)

type field struct {
	sel   cue.Selector
	value cue.Value
	class fieldClass
	fill  cue.Value // the value an absent field takes, for defaulted scalars and lists
}

// classify derives a field's class from its selector and value. A regular field
// is defaulted if its value has a default (*1 | int), is a concrete scalar or
// list, or is a struct whose fields all fill in turn; otherwise data must
// supply it, so it is required: a bare a: int behaves as a!: int.
func classify(sel cue.Selector, v cue.Value) field {
	f := field{sel: sel, value: v}
	switch sel.ConstraintType() {
	case cue.RequiredConstraint:
		f.class = required
	case cue.OptionalConstraint:
		f.class = optional
	default:
		f.class = required
		if d, ok := v.Default(); ok && d.Validate(cue.Concrete(true)) == nil {
			f.class, f.fill = defaulted, d
		} else if v.IncompleteKind() == cue.StructKind {
			if fills(v) {
				f.class = defaulted
			}
		} else if v.Validate(cue.Concrete(true)) == nil {
			f.class, f.fill = defaulted, v
		}
	}
	return f
}

// fills reports whether an absent struct field is filled implicitly: every
// field beneath it is optional or fills in turn.
func fills(v cue.Value) bool {
	if pending(v) {
		return false
	}
	it, err := v.Fields(cue.Optional(true))
	if err != nil {
		return false
	}
	for it.Next() {
		if classify(it.Selector(), it.Value()).class == required {
			return false
		}
	}
	return true
}

// structShape is what a struct declares: named fields, pattern constraints, and
// what it allows for labels it does not name.
type structShape struct {
	fields   map[string]field
	order    []string
	patterns []cue.Selector
	patvals  []cue.Value
	// residual is the constraint on a label the struct does not declare: _ for
	// ..., the pattern's value for a single pattern, nonexistent when closed.
	residual cue.Value
}

func shape(v cue.Value) structShape {
	s := structShape{fields: map[string]field{}}
	it, err := v.Fields(cue.Optional(true), cue.Patterns(true))
	if err != nil {
		return s
	}
	for it.Next() {
		sel := it.Selector()
		if sel.ConstraintType() == cue.PatternConstraint {
			s.patterns = append(s.patterns, sel)
			s.patvals = append(s.patvals, it.Value())
			continue
		}
		if !sel.IsString() {
			continue
		}
		name := sel.Unquoted()
		s.fields[name] = classify(sel, it.Value())
		s.order = append(s.order, name)
	}
	s.residual = v.LookupPath(cue.MakePath(cue.AnyString))
	return s
}

// matches reports whether label is accepted by one of s's patterns.
func (w *walker) matches(s structShape, label string) (cue.Value, bool) {
	for i, pat := range s.patterns {
		if pat.Pattern().Unify(w.ctx.Encode(label)).Err() == nil {
			return s.patvals[i], true
		}
	}
	return cue.Value{}, false
}

func (w *walker) structs(p cue.Path, pred, succ cue.Value) {
	if pending(pred) || pending(succ) {
		if !w.sameSyntax(pred, succ) {
			w.add(Finding{
				Path: p, Verdict: Unknown, Rule: RuleUnknownComprehension,
				Detail: "has fields decided by a comprehension over data, which differs between the schemas; the checker cannot evaluate it",
				Pos:    posOf(succ, pred),
			})
		}
		return
	}
	w.validators(p, pred, succ)

	os, ns := shape(pred), shape(succ)

	for _, name := range os.order {
		of := os.fields[name]
		fp := appendPath(p, cue.Str(name))
		nf, declared := ns.fields[name]
		if !declared {
			// new does not name the field: it is accepted only through a pattern
			// or ..., and then never filled.
			nv, ok := w.matches(ns, name)
			if !ok {
				nv, ok = ns.residual, ns.residual.Exists()
			}
			if !ok {
				w.add(Finding{
					Path: fp, Verdict: Incompatible, Rule: RuleFieldRemoved,
					Detail: "is declared in the prior schema and not accepted by the new one",
					Pos:    posOf(of.value),
				})
				continue
			}
			nf = field{sel: cue.Str(name).Optional(), value: nv, class: optional}
		}
		w.classes(fp, of, nf)
		w.value(fp, of.value, nf.value)
	}

	for _, name := range ns.order {
		nf := ns.fields[name]
		if _, declared := os.fields[name]; declared {
			continue
		}
		fp := appendPath(p, cue.Str(name))
		// old may have accepted the label through a pattern or ...
		ov, allowed := w.matches(os, name)
		if !allowed {
			ov, allowed = os.residual, os.residual.Exists()
		}
		if nf.class == required {
			w.add(Finding{
				Path: fp, Verdict: Incompatible, Rule: RuleFieldAddedRequired,
				Detail: "is required in the new schema but not declared in the prior one",
				Pos:    posOf(nf.value),
			})
			continue
		}
		if allowed {
			n := len(w.findings)
			w.value(fp, ov, nf.value)
			for i := n; i < len(w.findings); i++ {
				w.findings[i].ViaResidual = true
			}
		}
	}

	w.residuals(p, pred, succ, os, ns)
}

// classes compares what data can observe about a field's presence.
func (w *walker) classes(fp cue.Path, of, nf field) {
	if of.sel.ConstraintType() == nf.sel.ConstraintType() && composite(of.value) && composite(nf.value) {
		// Both bare: whether an absent struct or list fills is decided by what is
		// inside it, and the walk reports that at the field responsible.
		return
	}
	switch {
	case of.class == nf.class:
		if of.class == defaulted && of.fill.Exists() && nf.fill.Exists() && cuetil.Equal(of.fill, nf.fill) != nil {
			w.add(Finding{
				Path: fp, Verdict: Incompatible, Rule: RuleDefaultChanged,
				Detail: fmt.Sprintf("fills with %s when absent, where the prior schema filled it with %s", fmtv(nf.fill), fmtv(of.fill)),
				Pos:    posOf(nf.value),
			})
		}
		// Struct fills are compared field by field, by the walk.
	case nf.class == required:
		w.add(Finding{
			Path: fp, Verdict: Incompatible, Rule: RuleFieldNowRequired,
			Detail: fmt.Sprintf("was %s and is now required", of.class),
			Pos:    posOf(nf.value),
		})
	case of.class == defaulted && nf.class == optional:
		w.add(Finding{
			Path: fp, Verdict: Incompatible, Rule: RuleDefaultRemoved,
			Detail: "was filled when absent and is now left absent",
			Pos:    posOf(nf.value),
		})
	}
	// required → optional/defaulted and optional → defaulted are compatible:
	// no old instance omits a required field, and an absent optional field
	// behaves like one the schema never declared.
}

func composite(v cue.Value) bool {
	k := kindOf(v)
	return k == cue.StructKind || k == cue.ListKind
}

// residuals compares what the structs accept for labels they do not name:
// nothing when closed, every label with ..., or the labels a pattern matches.
// Allows(AnyString) reports whether every label is accepted.
func (w *walker) residuals(p cue.Path, pred, succ cue.Value, os, ns structShape) {
	oldOpen, newOpen := pred.Allows(cue.AnyString), succ.Allows(cue.AnyString)
	if !oldOpen && len(os.patterns) == 0 {
		return
	}
	anyp := appendPath(p, cue.AnyString)
	if !newOpen && len(ns.patterns) == 0 {
		if oldOpen || w.anyLabel(os) {
			w.add(Finding{
				Path: p, Verdict: Incompatible, Rule: RuleLabelsNarrowed,
				Detail: "accepts any field name in the prior schema and is closed in the new one",
				Pos:    posOf(succ, pred),
			})
			return
		}
		// A pattern's labels may all be declared by new; the checker does not
		// enumerate them.
		w.add(Finding{
			Path: anyp, Verdict: Unknown, Rule: RuleUnknownPatterns,
			Detail: fmt.Sprintf("accepts field names matching %s in the prior schema, and the new one declares no pattern; the checker does not enumerate the names the new schema declares instead", patternsString(os.patterns)),
			Pos:    posOf(succ, pred),
		})
		return
	}
	if len(os.patterns) > 1 || len(ns.patterns) > 1 {
		if !w.samePatterns(os, ns) {
			w.add(Finding{
				Path: p, Verdict: Unknown, Rule: RuleUnknownPatterns,
				Detail: fmt.Sprintf("has several pattern constraints, %s in the prior schema and %s in the new one, which the checker compares only for identity", patternsString(os.patterns), patternsString(ns.patterns)),
				Pos:    posOf(succ, pred),
			})
		}
		return
	}
	// At most one pattern each; ... counts as the pattern string. new must
	// accept every label old does, and every value old does for it.
	anyLabel := w.ctx.CompileString("string")
	oldPat, oldVal := anyLabel, os.residual
	if len(os.patterns) == 1 {
		oldPat, oldVal = os.patterns[0].Pattern(), os.patvals[0]
	}
	newPat, newVal := anyLabel, ns.residual
	if len(ns.patterns) == 1 {
		newPat, newVal = ns.patterns[0].Pattern(), ns.patvals[0]
	}
	if err := newPat.Subsume(oldPat, cue.Raw()); err != nil {
		if oldOpen {
			w.add(Finding{
				Path: p, Verdict: Incompatible, Rule: RuleLabelsNarrowed,
				Detail: fmt.Sprintf("accepts any field name in the prior schema and only names matching %s in the new one", patternsString(ns.patterns)),
				Pos:    posOf(succ, pred),
			})
			return
		}
		w.add(Finding{
			Path: anyp, Verdict: Unknown, Rule: RuleUnknownPatterns,
			Detail: fmt.Sprintf("has the pattern %s in the new schema, which the checker cannot show covers the prior pattern %s", patternsString(ns.patterns), patternsString(os.patterns)),
			Pos:    posOf(succ, pred),
		})
		return
	}
	if oldVal.Exists() && newVal.Exists() {
		w.value(anyp, oldVal, newVal)
	}
}

// anyLabel reports whether one of s's patterns matches every label.
func (w *walker) anyLabel(s structShape) bool {
	for _, pat := range s.patterns {
		if pat.Pattern().Subsume(w.ctx.CompileString("string"), cue.Raw()) == nil {
			return true
		}
	}
	return false
}

func (w *walker) samePatterns(os, ns structShape) bool {
	if len(os.patterns) != len(ns.patterns) {
		return false
	}
	for i := range os.patterns {
		if cuetil.Equal(os.patterns[i].Pattern(), ns.patterns[i].Pattern()) != nil || !w.sameExpr(os.patvals[i], ns.patvals[i]) {
			return false
		}
	}
	return true
}

func patternsString(pats []cue.Selector) string {
	var ss []string
	for _, p := range pats {
		ss = append(ss, p.String())
	}
	return strings.Join(ss, ", ")
}

func (w *walker) lists(p cue.Path, pred, succ cue.Value) {
	w.validators(p, pred, succ)
	olen, nlen := w.listLen(pred), w.listLen(succ)
	if err := nlen.Subsume(olen, cue.Raw()); err != nil {
		w.add(Finding{
			Path: p, Verdict: Incompatible, Rule: RuleLengthNarrowed,
			Detail: fmt.Sprintf("had length %s and now has length %s", fmtv(olen), fmtv(nlen)),
			Pos:    posOf(succ),
		})
	}
	it, err := pred.Fields(cue.Patterns(true))
	if err != nil {
		return
	}
	anyOld, anyNew := pred.LookupPath(cue.MakePath(cue.AnyIndex)), succ.LookupPath(cue.MakePath(cue.AnyIndex))
	for it.Next() {
		sel, ov := it.Selector(), it.Value()
		var nv cue.Value
		if sel.ConstraintType() == cue.PatternConstraint {
			// The open part of each list is read the same way on both sides: a
			// pattern's value from Fields is not closed as a looked-up one is.
			sel, ov, nv = cue.AnyIndex, anyOld, anyNew
		} else {
			nv = succ.LookupPath(cue.MakePath(sel))
			if !nv.Exists() {
				nv = anyNew
			}
		}
		if !nv.Exists() {
			// new is shorter; the length check reports it.
			continue
		}
		w.value(appendPath(p, sel), ov, nv)
	}
}

// leaf compares two scalar constraints, or constraints of different kinds.
func (w *walker) leaf(p cue.Path, pred, succ, wholeSucc cue.Value) {
	if w.sameExpr(pred, succ) {
		return
	}
	if succ.Validate(cue.Concrete(true)) == nil && pred.Validate(cue.Concrete(true)) == nil {
		if w.equal(pred, succ) != nil {
			w.add(Finding{
				Path: p, Verdict: Incompatible, Rule: RuleValueNarrowed,
				Detail: fmt.Sprintf("was %s and is now %s", fmtv(pred), fmtv(succ)),
				Pos:    posOf(wholeSucc),
			})
		}
		return
	}
	oplain, _ := w.splitValidators(pred)
	nplain, _ := w.splitValidators(succ)
	if err := nplain.Subsume(oplain, cue.Raw()); err != nil {
		w.add(Finding{
			Path: p, Verdict: Incompatible, Rule: RuleValueNarrowed,
			Detail: fmt.Sprintf("is now %s, which does not accept every value of the prior %s", fmtv(succ), fmtv(pred)),
			Pos:    posOf(wholeSucc),
		})
	}
	w.validators(p, pred, succ)
}

// listLen is v's length constraint. A value that is only a validator
// (list.Contains(1)) has no list to take a length from and admits any length,
// as its plain part is top.
func (w *walker) listLen(v cue.Value) cue.Value {
	if n := v.Len(); n.Err() == nil {
		return n
	}
	return w.ctx.CompileString("[...]").Len()
}

// A Validator is a validator applied to a value: a builtin call such as
// list.MaxItems(2), or a builtin validator value such as time.Time. Name is
// pkg.Name, or the name of a predeclared builtin.
type Validator struct {
	Name  string
	Args  []cue.Value
	Value cue.Value
}

// SplitValidators separates v into its plain constraints, unified, and its
// validators, as the compatibility rules read them. v is one alternative, not
// a disjunction. The plain part keeps v's kind, and its kind and length are
// v's own where a validator that needs concrete data (list.MinItems on an open
// list) leaves v itself incomplete.
func SplitValidators(ctx *cue.Context, v cue.Value) (cue.Value, []Validator) {
	w := &walker{ctx: ctx, top: ctx.CompileString("_")}
	plain, calls := w.splitValidators(v)
	out := make([]Validator, 0, len(calls))
	for _, c := range calls {
		out = append(out, Validator{Name: c.name, Args: c.args, Value: c.v})
	}
	return plain, out
}

// splitValidators separates v into its plain constraints, unified, and its
// validator calls. The plain part keeps v's kind, so a value that is only a
// validator (time.Time) still constrains its kind.
func (w *walker) splitValidators(v cue.Value) (plain cue.Value, calls []call) {
	plain = w.top
	if kv, ok := w.scalarKinds(v.IncompleteKind()); ok {
		plain = kv
	}
	var rec func(v cue.Value)
	rec = func(v cue.Value) {
		if c, ok := asCall(v); ok {
			if c.name != "close" {
				calls = append(calls, c)
				return
			}
			rec(c.args[0])
			return
		}
		op, args := v.Expr()
		if op == cue.AndOp {
			for _, a := range args {
				rec(a)
			}
			return
		}
		plain = plain.Unify(v)
	}
	rec(v)
	return plain, calls
}

// scalarKinds returns a value admitting exactly the scalar kinds in k, so that
// a value that is only a validator (time.Time) still constrains its kind.
func (w *walker) scalarKinds(k cue.Kind) (cue.Value, bool) {
	var alts []string
	for _, sk := range []struct {
		k    cue.Kind
		name string
	}{
		{cue.NullKind, "null"}, {cue.BoolKind, "bool"}, {cue.NumberKind, "number"},
		{cue.IntKind, "int"}, {cue.FloatKind, "float"}, {cue.StringKind, "string"}, {cue.BytesKind, "bytes"},
	} {
		if k&sk.k == sk.k {
			alts = append(alts, sk.name)
			k &^= sk.k
		}
	}
	if len(alts) == 0 {
		return cue.Value{}, false
	}
	return w.ctx.CompileString(strings.Join(alts, " | ")), true
}

// call is a validator applied to a value: a builtin call such as
// strings.MinRunes(1), or a builtin validator value such as time.Time.
type call struct {
	name string
	args []cue.Value
	v    cue.Value
}

func asCall(v cue.Value) (call, bool) {
	op, args := v.Expr()
	switch op {
	case cue.CallOp:
		// CUE has no user-defined functions: a callee is a member of a builtin
		// package or a predeclared builtin such as matchIf.
		if name, ok := packageMember(args[0]); ok {
			return call{name: name, args: args[1:], v: v}, true
		}
		return call{name: fmt.Sprint(args[0]), args: args[1:], v: v}, true
	case cue.SelectorOp:
		// time.Time is a validator value, not a call.
		if name, ok := packageMember(v); ok {
			return call{name: name, v: v}, true
		}
	}
	return call{}, false
}

// packageMember names v as pkg.Name when it selects a member of a builtin
// package.
func packageMember(v cue.Value) (string, bool) {
	op, args := v.Expr()
	if op != cue.SelectorOp || len(args) != 2 {
		return "", false
	}
	bi := args[0].BuildInstance()
	if bi == nil {
		if root, _ := args[0].ReferencePath(); root.Exists() {
			bi = root.BuildInstance()
		}
	}
	if bi == nil || !IsBuiltinPackage(bi.ImportPath) {
		return "", false
	}
	name, err := args[1].String()
	if err != nil {
		return "", false
	}
	return bi.ImportPath + "." + name, true
}

// IsBuiltinPackage reports whether an import path names one of CUE's builtin
// packages: their first element has no dot, unlike a module path.
func IsBuiltinPackage(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return first != "" && !strings.Contains(first, ".")
}

// validators compares the validator calls on new with those on old. A call on
// old that new drops widens the value. A call on new must be implied by one on
// old: the same call, or a weaker one by the rules in validatorTable. A call
// the table does not know is beyond the checker unless identical.
func (w *walker) validators(p cue.Path, pred, succ cue.Value) {
	_, ocalls := w.splitValidators(pred)
	_, ncalls := w.splitValidators(succ)
	for _, nc := range ncalls {
		implied, counterpart := false, false
		for _, oc := range ocalls {
			if oc.name != nc.name {
				continue
			}
			counterpart = true
			if w.sameExpr(oc.v, nc.v) {
				implied = true
				break
			}
			if rule, ok := validatorTable[nc.name]; ok && rule.implied(nc.args, oc.args) {
				implied = true
				break
			}
		}
		if implied {
			continue
		}
		if _, known := validatorTable[nc.name]; known {
			detail := fmt.Sprintf("must now satisfy %s, which the prior schema did not require", fmtv(nc.v))
			if counterpart {
				detail = fmt.Sprintf("must now satisfy %s, where the prior schema required %s", fmtv(nc.v), fmtv(counterpartOf(ocalls, nc.name).v))
			}
			w.add(Finding{
				Path: p, Verdict: Incompatible, Rule: RuleValidatorAdded,
				Detail: detail,
				Pos:    posOf(succ),
			})
			continue
		}
		w.add(Finding{
			Path: p, Verdict: Unknown, Rule: RuleUnknownValidator,
			Detail: fmt.Sprintf("must now satisfy %s, for which the checker has no rule", fmtv(nc.v)),
			Pos:    posOf(succ),
		})
	}
}

func counterpartOf(calls []call, name string) call {
	for _, c := range calls {
		if c.name == name {
			return c
		}
	}
	return call{}
}

// dataRefs collects the fields of the instance that v's expression refers to:
// references whose target lies under root, is a regular field, and is not
// concrete. A reference to a definition or hidden field is an alias and is
// followed; one to a concrete value is a constant; one leaving the schema
// cannot be set by data.
func (w *walker) dataRefs(v, root cue.Value, acc []cue.Path) []cue.Path {
	if rr, rp := v.ReferencePath(); rr.Exists() && len(rp.Selectors()) > 0 {
		if cuetil.PathHasPrefix(rp, root.Path()) {
			sels := rp.Selectors()
			last := sels[len(sels)-1]
			if last.LabelType() == cue.StringLabel && !constantTarget(cue.Dereference(v)) {
				acc = append(acc, cuetil.TrimPathPrefix(rp, root.Path()))
			}
		}
		return acc
	}
	op, args := v.Expr()
	switch op {
	case cue.NoOp:
		return acc
	case cue.CallOp:
		args = args[1:]
	}
	for _, a := range args {
		acc = w.dataRefs(a, root, acc)
	}
	return acc
}

// ConstantTarget reports whether a value is a constant for data: a scalar with
// exactly one instance. A default makes a value concrete without fixing it
// (lim: *5 | int accepts lim: 10), and an open list or struct is concrete but
// accepts more, so neither is a constant. This is the reference for "concrete
// is not constant" (AGENTS.md).
func ConstantTarget(d cue.Value) bool {
	return constantTarget(d)
}

func constantTarget(d cue.Value) bool {
	if _, hasDefault := d.Default(); hasDefault {
		return false
	}
	if d.IncompleteKind()&(cue.StructKind|cue.ListKind) != 0 {
		return false
	}
	return d.Validate(cue.Concrete(true)) == nil
}

// sameExpr reports whether old and new are the same expression: the same
// operators over the same operands, with references to the same paths within
// their own schemas. It decides Unknown cases in favor of Compatible when
// nothing changed.
func (w *walker) sameExpr(pred, succ cue.Value) bool {
	or, op := pred.ReferencePath()
	nr, np := succ.ReferencePath()
	if or.Exists() != nr.Exists() {
		return false
	}
	if or.Exists() && len(op.Selectors()) > 0 {
		oin, nin := cuetil.PathHasPrefix(op, w.oldRoot.Path()), cuetil.PathHasPrefix(np, w.newRoot.Path())
		if oin != nin {
			return false
		}
		if oin {
			return cuetil.TrimPathPrefix(op, w.oldRoot.Path()).String() == cuetil.TrimPathPrefix(np, w.newRoot.Path()).String()
		}
		// Both leave their schemas (a builtin, or a value outside): compare
		// where they point and what they are.
		if op.String() != np.String() {
			return false
		}
	}
	oop, oargs := pred.Expr()
	nop, nargs := succ.Expr()
	if oop != nop || len(oargs) != len(nargs) {
		return false
	}
	if oop == cue.NoOp {
		switch pred.IncompleteKind() {
		case cue.StructKind, cue.ListKind:
			return w.sameSyntax(pred, succ)
		}
		return w.equal(pred, succ) == nil
	}
	for i := range oargs {
		if !w.sameExpr(oargs[i], nargs[i]) {
			return false
		}
	}
	return true
}

// equal reports whether pred and succ subsume each other, as values detached
// from their fields. As of CUE v0.18, Subsume reports the value of an optional
// or required field as not an instance of a value from anywhere else, such as
// an operand of a disjunction: t!: "a" against the "a" of t!: "a" | "b".
// Unifying with top yields the same value, detached. Whether a field is
// optional or required is compared by classes, not here.
func (w *walker) equal(pred, succ cue.Value) error {
	return cuetil.Equal(w.top.Unify(pred), w.top.Unify(succ))
}

// sameSyntax compares two values by their evaluated syntax: the only view that
// keeps a pending comprehension. It depends only on what the values are, except
// for the "//cue:path:" comments naming where the generated lets for references
// that leave the value point, which differ between two versions of a schema by
// construction and are dropped.
func (w *walker) sameSyntax(pred, succ cue.Value) bool {
	ot, ok1 := evaluatedText(pred)
	nt, ok2 := evaluatedText(succ)
	return ok1 && ok2 && ot == nt
}

var cuePathComment = regexp.MustCompile(`(?m)^\s*//cue:path: .*\n`)

func evaluatedText(v cue.Value) (string, bool) {
	n, err := cueview.Evaluated(v)
	if err != nil {
		return "", false
	}
	t, err := cueview.Text(n)
	if err != nil {
		return "", false
	}
	return cuePathComment.ReplaceAllString(t, ""), true
}

func joinPaths(ps []cue.Path) string {
	var ss []string
	for _, p := range ps {
		ss = append(ss, p.String())
	}
	return strings.Join(ss, ", ")
}

// fmtv formats a value for a message, on one line.
func fmtv(v cue.Value) string {
	return strings.Join(strings.Fields(fmt.Sprint(v)), " ")
}

func appendPath(p cue.Path, sel cue.Selector) cue.Path {
	return cue.MakePath(append(append([]cue.Selector(nil), p.Selectors()...), sel)...)
}
