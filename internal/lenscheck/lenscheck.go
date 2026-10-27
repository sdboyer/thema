// Package lenscheck decides whether a thema lens is total: whether it maps
// every valid instance of its source schema to a valid instance of its target
// schema, never failing to evaluate along the way.
//
// A lens is read from its syntax (cuetil.ParseLens, cueview.Tree), which
// depends only on what the lens is, and its schemas are queried through the cue
// API. Each field the lens's result writes carries an obligation, decided by
// what the field is built from:
//
//   - a copy of input data must be present under the patterns enclosing the field,
//     and every instance of the source at that path must be an instance of the
//     target at the field (compat.Check, clause 1);
//   - a literal must be an instance of the target;
//   - a computed expression must be built from a closed set of operations, over
//     operands whose bounds, read through Expr(), show the result within the
//     target;
//   - a comprehension's pattern must be a presence test, an equality or a type
//     test on an input field, so that the walk can refine the source beneath it
//     and show that the arms together cover every instance;
//   - every required target field must be written on every path through the
//     lens, and no path may be written twice with values that could disagree.
//
// Lacunas follow the same rules: conditions are patterns, and the values they
// report must be present when the condition holds.
//
// Verdicts are three-valued: where the walk cannot decide, it says so
// ([Unknown]) rather than guess, and bind treats Unknown as a failure. Each
// finding carries the [Rule] that produced it, documented with what the author
// can do about it in docs/invariants.md, "Lens totality".
package lenscheck

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/cueview"
	"github.com/sdboyer/thema/internal/embedreg"
)

// Verdict is the outcome of a totality check, for one field or overall.
type Verdict int

const (
	// Total means every instance of the source maps to an instance of the target.
	Total Verdict = iota
	// NotTotal means some instance of the source has no result, or a result the
	// target rejects.
	NotTotal
	// Unknown means the checker cannot decide. For binding, Unknown counts as
	// NotTotal, and the rule says what the author can do about it.
	Unknown
)

func (v Verdict) String() string {
	switch v {
	case Total:
		return "total"
	case NotTotal:
		return "not total"
	default:
		return "unknown"
	}
}

// Rule identifies the rule a finding comes from. Rules are stable identifiers
// documented in docs/invariants.md; feedback to authors refers to them.
type Rule string

// Rules that refute totality.
const (
	// RuleCopyNarrowed: a copied value has an instance the target rejects
	// (compat.Check, clause 1, on the source at the copied path against the
	// target at the written one).
	RuleCopyNarrowed Rule = "copy-narrowed"
	// RuleSourceAbsent: a path the lens reads may be absent from the input where
	// it is read: an optional field, a list element past the list's minimum
	// length, or a field beneath _, with no presence test establishing it.
	RuleSourceAbsent Rule = "source-absent"
	// RuleSourceUndeclared: the lens reads a path the source schema does not
	// declare.
	RuleSourceUndeclared Rule = "source-undeclared"
	// RuleTargetUndeclared: the lens writes a path the target schema does not
	// declare.
	RuleTargetUndeclared Rule = "target-undeclared"
	// RulePatternAbsent: an equality pattern tests a path that may be absent, which
	// is an error in CUE rather than false.
	RulePatternAbsent Rule = "pattern-absent"
	// RuleNotAValue: the lens writes a constraint (int, _, _|_) where an instance
	// needs a value.
	RuleNotAValue Rule = "not-a-value"
	// RuleLiteralNotInstance: a literal the lens writes is not an instance of
	// the target at that path.
	RuleLiteralNotInstance Rule = "literal-not-instance"
	// RuleComputedNarrowed: a computed value has a possible result the target
	// rejects: a kind the target lacks, a bool it does not accept, or an integer
	// outside its bounds.
	RuleComputedNarrowed Rule = "computed-narrowed"
	// RuleRequiredNotWritten: a field the target requires is never written on
	// some path through the lens.
	RuleRequiredNotWritten Rule = "required-not-written"
	// RuleWriteConflict: a path is written twice, with values that may disagree,
	// so the result is their unification.
	RuleWriteConflict Rule = "write-conflict"
	// RuleArithOverflow: an integer operation's result may exceed int64.
	RuleArithOverflow Rule = "arith-overflow"
	// RuleDivisionByZero: a divisor may be zero.
	RuleDivisionByZero Rule = "division-by-zero"
	// RuleTableKeyMissing: a table is indexed by a value with no entry.
	RuleTableKeyMissing Rule = "table-key-missing"
)

// Rules that leave the verdict Unknown.
const (
	// RuleUnknownCopy: compat.Check cannot decide whether the copied value fits
	// the target.
	RuleUnknownCopy Rule = "unknown-copy"
	// RuleUnknownPattern: a comprehension's or lacuna's condition is not a presence
	// test, an equality or a type test on an input field.
	RuleUnknownPattern Rule = "unknown-pattern"
	// RuleUnknownExhaustive: a required field is written only under conditions the
	// checker cannot show cover every instance.
	RuleUnknownExhaustive Rule = "unknown-exhaustive"
	// RuleUnknownOp: a computed expression uses an operation outside the
	// provable set.
	RuleUnknownOp Rule = "unknown-op"
	// RuleUnknownDynamic: fields or elements are produced by a for clause, a
	// dynamic label or a list comprehension.
	RuleUnknownDynamic Rule = "unknown-dynamic"
	// RuleUnknownReference: the lens refers to something the checker cannot
	// follow to the input or to a constant.
	RuleUnknownReference Rule = "unknown-reference"
	// RuleUnknownBounds: an integer operand or result has no finite bounds, or
	// the checker's interval cannot be trusted (an operand occurs twice).
	RuleUnknownBounds Rule = "unknown-bounds"
	// RuleUnknownDivisor: a divisor's range includes zero though zero may not be
	// an instance.
	RuleUnknownDivisor Rule = "unknown-divisor"
	// RuleUnknownTargetConstraint: the target constrains a computed value beyond
	// what the checker compares (a regular expression, a validator, alternatives
	// with gaps, a disjunction above the path).
	RuleUnknownTargetConstraint Rule = "unknown-target-constraint"
	// RuleUnknownTableKey: a table is indexed by a value that is not a finite set
	// of string constants.
	RuleUnknownTableKey Rule = "unknown-table-key"
	// RuleUnknownHead: the first element of a comprehension-built list is taken
	// with no fallback element.
	RuleUnknownHead Rule = "unknown-head"
)

// Finding is one verdict at one place in the lens. Total paths produce no
// finding.
//
// Its text reads as a sentence about the value at Path: "<path> <detail>
// (rule <rule>)". Path is where in the lens: "result.a", "lacunas[0].condition".
type Finding struct {
	Path    string
	Verdict Verdict
	Rule    Rule
	Detail  string
	// Pos is the author's position for the finding: the result field, the
	// comprehension it is declared in, or the lacuna field.
	Pos token.Pos
}

func (f Finding) String() string {
	return fmt.Sprintf("%s %s (rule %s)", f.Path, f.Detail, f.Rule)
}

// Result is the outcome of [Check]: the findings at every place that is not
// Total.
type Result struct {
	Findings []Finding
}

// Verdict is the overall verdict: NotTotal if any finding is, since one
// counterexample settles it; otherwise Unknown if any is; otherwise Total.
func (r Result) Verdict() Verdict {
	v := Total
	for _, f := range r.Findings {
		switch f.Verdict {
		case NotTotal:
			return NotTotal
		case Unknown:
			v = Unknown
		}
	}
	return v
}

// Total reports whether the overall verdict is Total.
func (r Result) Total() bool {
	return r.Verdict() == Total
}

// Error returns the findings as a CUE error list positioned at the author's
// constructs, or nil if Total.
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

// Check decides whether lens, which maps instances of source to instances of
// target, is total. Both schemas are closed recursively first (an author's ...
// survives closing), as Translate closes a lens's result against its target.
func Check(ctx *cue.Context, lens, source, target cue.Value, from, to string) Result {
	w := &walker{
		ctx:     ctx,
		lens:    lens,
		from:    from,
		to:      to,
		source:  cuetil.CloseRecursive(ctx, source),
		target:  cuetil.CloseRecursive(ctx, target),
		seen:    map[string]bool{},
		targets: map[string]targetLookup{},
	}
	syn, err := cuetil.ParseLens(lens, from, to)
	if err != nil {
		w.add(Finding{Path: to, Verdict: Unknown, Rule: RuleUnknownReference,
			Detail: fmt.Sprintf("could not be read as syntax: %v", err), Pos: w.lensPos(cue.MakePath(cue.Str(to)))})
		return w.result()
	}
	w.syn, w.tree = syn, syn.Tree()
	w.walkResult()
	w.checkCopies()
	w.checkExhaustive()
	w.checkConflicts()
	w.walkLacunas()
	return w.result()
}

type walker struct {
	ctx  *cue.Context
	lens cue.Value
	// from and to name the lens's source and result fields.
	from, to       string
	source, target cue.Value
	syn            *cuetil.LensSyntax
	tree           *cueview.Tree
	findings       []Finding
	seen           map[string]bool
	writes         []*write
	// copies are the copy obligations met during the walk, judged once every
	// write is known: a required field the lens writes beside a copy is for
	// coverage, not for the copy, to prove.
	copies []pendingCopy
	// quiet suppresses recording writes, for the arms of a head or table whose
	// write is recorded once, for the whole.
	quiet   int
	targets map[string]targetLookup
}

func (w *walker) add(f Finding) {
	key := f.Path + "\x00" + string(f.Rule) + "\x00" + f.Detail
	if w.seen[key] {
		return
	}
	w.seen[key] = true
	w.findings = append(w.findings, f)
}

func (w *walker) result() Result {
	sort.SliceStable(w.findings, func(i, j int) bool {
		return w.findings[i].Path < w.findings[j].Path
	})
	return Result{Findings: w.findings}
}

// writeKind is what a write builds its value from.
type writeKind int

const (
	copyW      writeKind = iota // input data, by path
	literalW                    // a scalar literal
	computedW                   // an expression, a head or a table
	compositeW                  // a struct or list literal, whose children are written in turn
)

// A write is one value the lens's result gets at a path, under the patterns of
// the comprehensions it is declared in.
type write struct {
	q        cue.Path
	patterns []pattern
	kind     writeKind
	src      cue.Path // copyW: the source path copied
	text     string   // the expression, for messages and conflicts
	pos      token.Pos
}

func (w *walker) record(wr *write) {
	if w.quiet == 0 {
		w.writes = append(w.writes, wr)
	}
}

// subject names the result path q in findings.
func (w *walker) subject(q cue.Path) string {
	return rootRef(w.to, q)
}

// inputRef names the source path p in findings.
func (w *walker) inputRef(p cue.Path) string {
	return rootRef(w.from, p)
}

// rootRef names the path p beneath the lens field root.
func rootRef(root string, p cue.Path) string {
	if len(p.Selectors()) == 0 {
		return root
	}
	return root + "." + p.String()
}

// lensPos is the author's position for the lens field at p.
func (w *walker) lensPos(p cue.Path) token.Pos {
	return authorPos(w.lens.LookupPath(p))
}

// resultPos is the author's position for the result field q, or fallback where
// the field is not in the lens's value (declared inside a comprehension).
func (w *walker) resultPos(q cue.Path, fallback token.Pos) token.Pos {
	sels := append([]cue.Selector{cue.Str(w.to)}, q.Selectors()...)
	if p := authorPos(w.lens.LookupPath(cue.MakePath(sels...))); p.IsValid() {
		return p
	}
	return fallback
}

// authorPos returns the position of the first conjunct of v that is not in
// thema's own CUE, or v's position if there is none (as bind.go's userPos).
func authorPos(v cue.Value) token.Pos {
	if !v.Exists() {
		return token.NoPos
	}
	_, args := v.Expr()
	for _, a := range args {
		if p := a.Pos(); p.IsValid() && !strings.HasPrefix(p.Filename(), embedreg.Root()+string(filepath.Separator)) {
			return p
		}
	}
	return v.Pos()
}

// kernelResultConstraint is thema's own conjunct on every lens result.
const kernelResultConstraint = "struct.MinFields(0)"

func (w *walker) walkResult() {
	root := cue.MakePath()
	pos := w.resultPos(root, token.NoPos)
	for _, c := range w.syn.ResultConjuncts() {
		if call, ok := c.Expr.(*ast.CallExpr); ok && !w.syn.FromInput(c) && text(call) == kernelResultConstraint {
			continue
		}
		w.conjunct(c, root, nil, pos)
	}
}

// conjunct handles one conjunct of the value written at q, under patterns gs.
func (w *walker) conjunct(c cueview.Conjunct, q cue.Path, gs []pattern, pos token.Pos) {
	if w.syn.FromInput(c) {
		p, ok := w.sourcePath(c)
		if !ok {
			w.add(Finding{Path: w.subject(q), Verdict: Unknown, Rule: RuleUnknownReference,
				Detail: fmt.Sprintf("copies %s, a selection into the input the checker cannot follow; select fields by name", text(c.Expr)), Pos: pos})
			return
		}
		w.copy(q, p, gs, pos)
		return
	}
	if c.Disjunction != nil {
		w.record(&write{q: q, patterns: gs, kind: literalW, text: text(c.Disjunction), pos: pos})
		w.add(Finding{Path: w.subject(q), Verdict: NotTotal, Rule: RuleNotAValue,
			Detail: fmt.Sprintf("is %s, a disjunction rather than a value; a result field must have one concrete value", text(c.Disjunction)), Pos: pos})
		return
	}
	switch e := c.Expr.(type) {
	case *ast.StructLit:
		w.composite(e, q, gs, pos)
	case *ast.ListLit:
		w.list(e, q, gs, pos)
	case *ast.BasicLit:
		w.literal(e, q, gs, pos)
	case *ast.UnaryExpr:
		if lit, ok := e.X.(*ast.BasicLit); ok && e.Op == token.SUB && lit.Kind != token.STRING {
			w.literal(e, q, gs, pos)
			return
		}
		w.computed(e, q, gs, pos)
	case *ast.Ident:
		if w.predeclared(e) {
			if isBoolName(e.Name) || e.Name == "null" {
				w.literal(e, q, gs, pos)
				return
			}
			w.record(&write{q: q, patterns: gs, kind: literalW, text: e.Name, pos: pos})
			w.add(Finding{Path: w.subject(q), Verdict: NotTotal, Rule: RuleNotAValue,
				Detail: fmt.Sprintf("is %s, a constraint rather than a value; a result field must have a concrete value", e.Name), Pos: pos})
			return
		}
		w.add(Finding{Path: w.subject(q), Verdict: Unknown, Rule: RuleUnknownReference,
			Detail: fmt.Sprintf("is %s, a reference the checker cannot follow to the input or to a constant", e.Name), Pos: pos})
	case *ast.BottomLit:
		w.record(&write{q: q, patterns: gs, kind: literalW, text: "_|_", pos: pos})
		w.add(Finding{Path: w.subject(q), Verdict: NotTotal, Rule: RuleNotAValue,
			Detail: "is _|_, an error, rather than a value", Pos: pos})
	default:
		w.computed(c.Expr, q, gs, pos)
	}
}

// sourcePath returns the path within the input that the copied conjunct c
// reads: the fields the reference passed through within the input declaration
// (input: {b: ...}), and the selectors of c.Expr beyond the identifier that
// resolved there, where c.Expr is a selection the tree could not follow (into
// _, or into a value only a comprehension produces).
func (w *walker) sourcePath(c cueview.Conjunct) (cue.Path, bool) {
	if p, ok := w.chainPath(c.Expr); ok {
		return p, true
	}
	if root, isChain := chainRoot(c.Expr); isChain && !w.predeclared(root) {
		return cue.Path{}, false
	}
	if c.Via == nil {
		return cue.Path{}, false
	}
	return w.viaPath(c.Via), true
}

// viaPath is the path of the field f within the lens's input: the labels of
// the fields from the input declaration down to f.
func (w *walker) viaPath(f *ast.Field) cue.Path {
	var sels []cue.Selector
	for n := ast.Node(f); n != nil; n = w.tree.Parent(n) {
		fld, ok := n.(*ast.Field)
		if !ok {
			continue
		}
		if w.isInputField(fld) {
			break
		}
		if name, _, err := ast.LabelName(fld.Label); err == nil {
			sels = append([]cue.Selector{cue.Str(name)}, sels...)
		}
	}
	return cue.MakePath(sels...)
}

func (w *walker) isInputField(f *ast.Field) bool {
	for _, in := range w.syn.InputFields() {
		if in == f {
			return true
		}
	}
	return false
}

func (w *walker) withinInput(n ast.Node) bool {
	for _, in := range w.syn.InputFields() {
		if w.tree.Within(n, in) {
			return true
		}
	}
	return false
}

// chainRoot returns the identifier a selector chain is rooted at.
func chainRoot(x ast.Expr) (*ast.Ident, bool) {
	for {
		switch e := x.(type) {
		case *ast.SelectorExpr:
			x = e.X
		case *ast.IndexExpr:
			x = e.X
		case *ast.ParenExpr:
			x = e.X
		case *ast.Ident:
			return e, true
		default:
			return nil, false
		}
	}
}

// chainPath resolves a selector chain into the input to the source path it
// reads, following lets and aliases at its root.
func (w *walker) chainPath(x ast.Expr) (cue.Path, bool) {
	switch e := x.(type) {
	case *ast.ParenExpr:
		return w.chainPath(e.X)
	case *ast.SelectorExpr:
		base, ok := w.chainPath(e.X)
		if !ok {
			return cue.Path{}, false
		}
		name, _, err := ast.LabelName(e.Sel)
		if err != nil {
			return cue.Path{}, false
		}
		return appendPath(base, cue.Str(name)), true
	case *ast.IndexExpr:
		base, ok := w.chainPath(e.X)
		if !ok {
			return cue.Path{}, false
		}
		lit, ok := e.Index.(*ast.BasicLit)
		if !ok {
			return cue.Path{}, false
		}
		switch lit.Kind {
		case token.INT:
			i, err := strconv.Atoi(lit.Value)
			if err != nil {
				return cue.Path{}, false
			}
			return appendPath(base, cue.Index(i)), true
		case token.STRING:
			s, err := literalString(lit)
			if err != nil {
				return cue.Path{}, false
			}
			return appendPath(base, cue.Str(s)), true
		}
		return cue.Path{}, false
	case *ast.Ident:
		switch n := e.Node.(type) {
		case *ast.LetClause:
			return w.chainPath(n.Expr)
		case *ast.Alias:
			return w.chainPath(n.Expr)
		}
		if f := w.refField(e); f != nil {
			return w.fieldPath(f)
		}
	}
	return cue.Path{}, false
}

// refField returns the field the identifier id refers to, if it refers to a
// field: a reference resolves to the field's value, a postfix alias to the
// field itself. A comprehension variable, whose node is the clause's source,
// is not a field.
func (w *walker) refField(id *ast.Ident) *ast.Field {
	switch n := id.Node.(type) {
	case *ast.Field:
		return n
	case ast.Expr:
		if f, ok := w.tree.Parent(n).(*ast.Field); ok && f.Value == n {
			return f
		}
	}
	return nil
}

// fieldPath is the source path a reference to the field f reads: nothing for
// the input itself, f's path within the input, or what f's value reads.
func (w *walker) fieldPath(f *ast.Field) (cue.Path, bool) {
	if w.isInputField(f) {
		return cue.MakePath(), true
	}
	if w.withinInput(f) {
		return w.viaPath(f), true
	}
	return w.chainPath(f.Value)
}

// predeclared reports whether id names a predeclared identifier (int, true,
// len, quo, ...): one that resolves to nothing in the tree.
func (w *walker) predeclared(id *ast.Ident) bool {
	return id.Node == nil || w.tree.Parent(id.Node) == nil
}

// The predeclared bool values, as written.
const (
	trueName  = "true"
	falseName = "false"
)

// isBoolName reports whether name is one of the predeclared bool values.
func isBoolName(name string) bool {
	return name == trueName || name == falseName
}

// copy proves the copy of input p into result q under patterns gs.
func (w *walker) copy(q, p cue.Path, gs []pattern, pos token.Pos) {
	w.record(&write{q: q, patterns: gs, kind: copyW, src: p, text: w.inputRef(p), pos: pos})
	src := w.lookupSource(p, gs)
	subj := w.subject(q)
	if !src.declared {
		w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleSourceUndeclared,
			Detail: fmt.Sprintf("copies %s, which the source schema does not declare", w.inputRef(p)), Pos: pos})
		return
	}
	if !src.present {
		w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleSourceAbsent,
			Detail: fmt.Sprintf("copies %s, which may be absent: %s; write the field under if %s != _|_", w.inputRef(p), src.why, w.inputRef(p)), Pos: pos})
	}
	tgt, ok := w.targetAt(q, pos)
	if !ok {
		return
	}
	alts := src.whole
	if src.refined {
		alts = src.branches
	}
	w.copies = append(w.copies, pendingCopy{q: q, p: p, branches: alts, tgt: tgt, pos: pos})
}

// A pendingCopy is a copy of input p into result q whose alternatives
// (branches: the value as declared, or as refined by the patterns it is written
// under) await the compatibility judgement against the target at q.
type pendingCopy struct {
	q, p     cue.Path
	branches []cue.Value
	tgt      cue.Value
	pos      token.Pos
}

// checkCopies judges every copy by compat.Check clause 1: each alternative of
// the source at p, closed, must be accepted by the target at q, closed. An
// alternative a pattern has fixed to a constant is an instance, and is checked
// as one.
func (w *walker) checkCopies() {
	for _, c := range w.copies {
		subj := w.subject(c.q)
		for _, b := range c.branches {
			if compat.ConstantTarget(b) {
				if err := c.tgt.Unify(b).Validate(cue.Concrete(true)); err != nil {
					w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleCopyNarrowed,
						Detail: fmt.Sprintf("copies %s, which may be %s, which the target schema does not accept: %s", w.inputRef(c.p), fmtv(b), errMsg(err)), Pos: c.pos})
				}
				continue
			}
			res := compat.Check(w.ctx, b, c.tgt).Instances()
			for _, f := range res.Findings {
				if f.Rule == compat.RuleFieldAddedRequired && w.writesAt(appendPaths(c.q, f.Path)) {
					// The lens writes the field itself; coverage decides.
					continue
				}
				verdict, rule := NotTotal, RuleCopyNarrowed
				if f.Verdict == compat.Unknown {
					verdict, rule = Unknown, RuleUnknownCopy
				}
				w.add(Finding{Path: subj, Verdict: verdict, Rule: rule,
					Detail: fmt.Sprintf("copies %s, %s", w.inputRef(c.p), compatDetail(f)), Pos: c.pos})
			}
		}
	}
}

// compatDetail renders a compat finding about a copied value as a clause about
// it.
func compatDetail(f compat.Finding) string {
	detail := strings.NewReplacer("the prior schema", "the source schema", "the new schema", "the target schema").Replace(f.Detail)
	if len(f.Path.Selectors()) == 0 {
		return fmt.Sprintf("which %s (compat rule %s)", detail, f.Rule)
	}
	return fmt.Sprintf("whose %s %s (compat rule %s)", f.Path, detail, f.Rule)
}

// composite walks a struct literal written at q.
func (w *walker) composite(e *ast.StructLit, q cue.Path, gs []pattern, pos token.Pos) {
	w.record(&write{q: q, patterns: gs, kind: compositeW, text: "{...}", pos: pos})
	if tgt, ok := w.targetAt(q, pos); ok && tgt.IncompleteKind()&cue.StructKind == 0 {
		w.add(Finding{Path: w.subject(q), Verdict: NotTotal, Rule: RuleLiteralNotInstance,
			Detail: fmt.Sprintf("is a struct, where the target schema wants %s", tgt.IncompleteKind()), Pos: pos})
		return
	}
	w.decls(e.Elts, q, gs, pos)
}

// list walks a list literal written at q: each element is written at its index.
func (w *walker) list(e *ast.ListLit, q cue.Path, gs []pattern, pos token.Pos) {
	w.record(&write{q: q, patterns: gs, kind: compositeW, text: "[...]", pos: pos})
	for _, elt := range e.Elts {
		switch elt.(type) {
		case *ast.Comprehension:
			w.add(Finding{Path: w.subject(q), Verdict: Unknown, Rule: RuleUnknownDynamic,
				Detail: "builds list elements with a comprehension, so how many there are and what they hold depends on the instance; write each element", Pos: pos})
			return
		case *ast.Ellipsis:
			w.add(Finding{Path: w.subject(q), Verdict: NotTotal, Rule: RuleNotAValue,
				Detail: "is an open list ([...]), a constraint rather than a value", Pos: pos})
			return
		}
	}
	if tgt, ok := w.targetAt(q, pos); ok && tgt.IncompleteKind() != cue.TopKind {
		if tgt.IncompleteKind()&cue.ListKind == 0 {
			w.add(Finding{Path: w.subject(q), Verdict: NotTotal, Rule: RuleLiteralNotInstance,
				Detail: fmt.Sprintf("is a list, where the target schema wants %s", tgt.IncompleteKind()), Pos: pos})
			return
		}
		shape := w.ctx.CompileString("[" + strings.Repeat("_,", len(e.Elts)) + "]")
		if err := tgt.Unify(shape).Validate(); err != nil {
			w.add(Finding{Path: w.subject(q), Verdict: NotTotal, Rule: RuleLiteralNotInstance,
				Detail: fmt.Sprintf("is a list of %d elements, which the target schema does not accept: %s", len(e.Elts), errMsg(err)), Pos: pos})
			return
		}
	}
	for i, x := range e.Elts {
		eq := appendPath(q, cue.Index(i))
		for _, c := range w.tree.Conjuncts(x) {
			w.conjunct(c, eq, gs, pos)
		}
	}
}

// literal proves the scalar literal x written at q.
func (w *walker) literal(x ast.Expr, q cue.Path, gs []pattern, pos token.Pos) {
	w.record(&write{q: q, patterns: gs, kind: literalW, text: text(x), pos: pos})
	tgt, ok := w.targetAt(q, pos)
	if !ok {
		return
	}
	v := w.ctx.BuildExpr(x)
	if err := tgt.Unify(v).Validate(cue.Concrete(true)); err != nil {
		w.add(Finding{Path: w.subject(q), Verdict: NotTotal, Rule: RuleLiteralNotInstance,
			Detail: fmt.Sprintf("is the constant %s, which the target schema does not accept: %s", text(x), errMsg(err)), Pos: pos})
	}
}

// decls walks the declarations of a struct literal written at q.
func (w *walker) decls(decls []ast.Decl, q cue.Path, gs []pattern, pos token.Pos) {
	for _, d := range decls {
		switch x := d.(type) {
		case *ast.Field:
			name, isIdent, err := ast.LabelName(x.Label)
			if err != nil || (!isIdent && name == "") {
				w.add(Finding{Path: w.subject(q), Verdict: Unknown, Rule: RuleUnknownDynamic,
					Detail: fmt.Sprintf("has a field whose label %s is computed, so which field it is depends on the instance; write the label", text(x.Label)), Pos: pos})
				continue
			}
			if strings.HasPrefix(name, "_") || strings.HasPrefix(name, "#") {
				// Hidden fields and definitions are not data. A regular field that
				// refers to one is followed to what it refers to.
				continue
			}
			if x.Constraint == token.OPTION || x.Constraint == token.NOT {
				// a?: x and a!: x constrain a field without writing it.
				continue
			}
			cq := appendPath(q, cue.Str(name))
			cpos := w.resultPos(cq, pos)
			for _, c := range w.tree.Conjuncts(x.Value) {
				w.conjunct(c, cq, gs, cpos)
			}
		case *ast.Comprehension:
			cpos := pos
			if p := x.Pos(); p.IsValid() {
				cpos = p
			}
			ngs := append([]pattern(nil), gs...)
			walkable := true
			for _, cl := range x.Clauses {
				switch c := cl.(type) {
				case *ast.IfClause:
					g, ok := w.parsePattern(c.Condition)
					if !ok {
						w.add(Finding{Path: w.subject(q), Verdict: Unknown, Rule: RuleUnknownPattern,
							Detail: fmt.Sprintf("has fields under %s, which is not a pattern: a presence test (p != _|_), an equality (p == c) or a type test ((p & T) != _|_) on an input field, or a conjunction (&&) of these", text(c.Condition)), Pos: cpos})
						g = []pattern{{root: w.from, kind: opaque, text: text(c.Condition)}}
					}
					w.checkPatterns(g, ngs, w.subject(q), cpos)
					ngs = append(ngs, g...)
				case *ast.ForClause:
					w.add(Finding{Path: w.subject(q), Verdict: Unknown, Rule: RuleUnknownDynamic,
						Detail: fmt.Sprintf("has fields produced by a for clause over %s, so which fields exist depends on the instance; write each field", text(c.Source)), Pos: cpos})
					walkable = false
				}
			}
			if !walkable {
				continue
			}
			if s, ok := x.Value.(*ast.StructLit); ok {
				w.decls(s.Elts, q, ngs, cpos)
				continue
			}
			for _, c := range w.tree.Conjuncts(x.Value) {
				w.conjunct(c, q, ngs, cpos)
			}
		case *ast.EmbedDecl:
			for _, c := range w.tree.Conjuncts(x.Expr) {
				w.conjunct(c, q, gs, pos)
			}
		}
	}
}

// embedded returns the conjunct expressions of an embedded expression, for
// cueview.FieldDecls.
func (w *walker) embedded(x ast.Expr) []ast.Expr {
	var out []ast.Expr
	for _, c := range w.tree.Conjuncts(x) {
		out = append(out, c.Expr)
	}
	return out
}

// targetLookup is what the target schema has at a result path.
type targetLookup struct {
	v  cue.Value
	ok bool
	// finding is reported where the path is written.
	finding *Finding
}

// targetAt returns the target schema's value at result path q, reporting at pos
// a path the target does not declare, or one beneath a disjunction, which the
// checker does not descend into.
func (w *walker) targetAt(q cue.Path, pos token.Pos) (cue.Value, bool) {
	l := w.lookupTarget(q)
	if l.finding != nil {
		f := *l.finding
		f.Pos = pos
		w.add(f)
	}
	return l.v, l.ok
}

func (w *walker) lookupTarget(q cue.Path) targetLookup {
	key := q.String()
	if l, ok := w.targets[key]; ok {
		return l
	}
	cur := w.target
	sels := q.Selectors()
	for i, sel := range sels {
		if cur.IncompleteKind() == cue.TopKind {
			// _ declares everything beneath it.
			break
		}
		if alts := compat.Branches(cur); len(alts) > 1 {
			prefix := cue.MakePath(sels[:i]...)
			l := targetLookup{finding: &Finding{Path: w.subject(q), Verdict: Unknown, Rule: RuleUnknownTargetConstraint,
				Detail: fmt.Sprintf("is written beneath %s, which is a disjunction in the target schema; the checker descends only into a single struct or list, so write %s as one value", w.subject(prefix), w.subject(prefix))}}
			w.targets[key] = l
			return l
		}
		next, _, ok := cuetil.DeclaredChild(w.ctx, cur, sel)
		if !ok {
			l := targetLookup{finding: &Finding{Path: w.subject(q), Verdict: NotTotal, Rule: RuleTargetUndeclared,
				Detail: "is not declared by the target schema"}}
			w.targets[key] = l
			return l
		}
		cur = next
	}
	l := targetLookup{v: cur, ok: true}
	w.targets[key] = l
	return l
}

// sourceLookup is what the source schema has at an input path, refined by the
// patterns it is read under.
type sourceLookup struct {
	// branches are the alternatives the value may be, each non-disjunctive,
	// as refined by the patterns it is read under.
	branches []cue.Value
	// whole is the value as declared, one per alternative of its parents,
	// and refined reports whether a pattern narrowed it (so branches is the
	// finer account).
	whole    []cue.Value
	refined  bool
	declared bool
	present  bool
	why      string // why the path may be absent
}

// source looks up input path p as read under patterns gs: its alternatives,
// whether the source schema declares it, and whether it is present in every
// instance (every step required or defaulted, or established by a presence or
// type test among gs).
func (w *walker) lookupSource(p cue.Path, gs []pattern) sourceLookup {
	cur := []cue.Value{w.source}
	present := true
	var why string
	sels := p.Selectors()
	for i, sel := range sels {
		prefix := cue.MakePath(sels[:i+1]...)
		var next []cue.Value
		optional, declaredIn, alts := false, 0, 0
		var reason string
		for _, v := range cur {
			for _, alt := range compat.Branches(v) {
				alts++
				if alt.IncompleteKind() == cue.TopKind {
					next = append(next, alt)
					declaredIn++
					optional = true
					reason = fmt.Sprintf("%s is _ in the source schema, which need not hold %s", w.inputRef(cue.MakePath(sels[:i]...)), sel)
					continue
				}
				child, csel, ok := cuetil.DeclaredChild(w.ctx, alt, sel)
				if !ok {
					continue
				}
				declaredIn++
				next = append(next, child)
				if r, ok := presentIn(alt, sel, csel); !ok {
					optional = true
					reason = fmt.Sprintf("%s %s", w.inputRef(prefix), r)
				}
			}
		}
		if declaredIn == 0 {
			return sourceLookup{}
		}
		if declaredIn < alts {
			optional = true
			reason = fmt.Sprintf("%s is declared by only some alternatives of %s", w.inputRef(prefix), w.inputRef(cue.MakePath(sels[:i]...)))
		}
		if optional && !establishes(gs, prefix) && present {
			present, why = false, reason
		}
		cur = next
	}
	var leaves []cue.Value
	for _, v := range cur {
		leaves = append(leaves, compat.Branches(v)...)
	}
	branches, refined := refine(leaves, p, gs)
	return sourceLookup{branches: branches, whole: cur, refined: refined, declared: true, present: present, why: why}
}

// presentIn reports whether the child sel, declared by parent with selector
// csel, is present in every instance of parent, and why not if not.
func presentIn(parent cue.Value, sel, csel cue.Selector) (string, bool) {
	if sel.Type().LabelType() == cue.IndexLabel {
		iv, _, ok := intervalOf(parent.Len())
		if ok && iv.lo != nil && iv.lo.Int64() > int64(sel.Index()) {
			return "", true
		}
		return fmt.Sprintf("is an element the list need not have (its length is %s)", fmtv(parent.Len())), false
	}
	switch csel.ConstraintType() {
	case cue.OptionalConstraint:
		return "is optional in the source schema", false
	case cue.PatternConstraint:
		return fmt.Sprintf("is accepted by the source schema only through the pattern constraint %s, so an instance need not have it", csel), false
	}
	return "", true
}

// writesAt reports whether the lens writes result path q explicitly.
func (w *walker) writesAt(q cue.Path) bool {
	key := q.String()
	for _, wr := range w.writes {
		if wr.q.String() == key {
			return true
		}
	}
	return false
}

// checkExhaustive checks that every required target field is written on every
// path through the lens: at the root, and beneath every struct the lens
// writes, under the patterns it writes it.
func (w *walker) checkExhaustive() {
	type parent struct {
		q        cue.Path
		patterns []pattern
		pos      token.Pos
		isCopy   bool
	}
	parents := []parent{{q: cue.MakePath(), pos: w.resultPos(cue.MakePath(), token.NoPos)}}
	for _, wr := range w.writes {
		switch wr.kind {
		case compositeW:
			parents = append(parents, parent{q: wr.q, patterns: wr.patterns, pos: wr.pos})
		case copyW:
			parents = append(parents, parent{q: wr.q, patterns: wr.patterns, pos: wr.pos, isCopy: true})
		}
	}
	done := map[string]bool{}
	for _, p := range parents {
		key := fmt.Sprintf("%s|%s|%v", p.q, patternsKey(p.patterns), p.isCopy)
		if done[key] {
			continue
		}
		done[key] = true
		l := w.lookupTarget(p.q)
		if !l.ok || len(compat.Branches(l.v)) > 1 || l.v.IncompleteKind() != cue.StructKind {
			continue
		}
		iter, err := l.v.Fields(cue.Optional(true))
		if err != nil {
			continue
		}
		for iter.Next() {
			sel := iter.Selector()
			if !sel.IsString() || compat.FormOf(sel, iter.Value()) != compat.Required {
				continue
			}
			cq := appendPath(p.q, cue.Str(sel.Unquoted()))
			if p.isCopy && !w.writesAt(cq) {
				// compat judged the copy, including this field.
				continue
			}
			cands, written := w.candidates(p.patterns, cq)
			switch {
			case !written:
				w.add(Finding{Path: w.subject(cq), Verdict: NotTotal, Rule: RuleRequiredNotWritten,
					Detail: "is required by the target schema and never written" + underPatterns(p.patterns), Pos: p.pos})
			case w.covers(cands):
			default:
				w.add(Finding{Path: w.subject(cq), Verdict: Unknown, Rule: RuleUnknownExhaustive,
					Detail: fmt.Sprintf("is required by the target schema, and is written only under %s, which the checker cannot show cover every instance%s; write it unconditionally, under a condition and its negation, or under type tests covering every alternative of the tested field", describePatternSets(cands), underPatterns(p.patterns)), Pos: p.pos})
			}
		}
	}
}

func underPatterns(gs []pattern) string {
	if len(gs) == 0 {
		return ""
	}
	return " under " + patternsString(gs)
}

// candidates returns, for each write that gives result path cq a value where
// the enclosing write's patterns gs hold, the patterns it still needs beyond gs.
// A copy at an ancestor of cq whose source declares the rest of the path gives
// cq a value too. written reports whether cq is written at all.
func (w *walker) candidates(gs []pattern, cq cue.Path) (cands [][]pattern, written bool) {
	for _, wr := range w.writes {
		var covers bool
		switch {
		case wr.q.String() == cq.String():
			covers = true
		case wr.kind == copyW && cuetil.PathHasPrefix(cq, wr.q):
			rel := cuetil.TrimPathPrefix(cq, wr.q)
			covers = w.lookupSource(appendPaths(wr.src, rel), nil).declared
		}
		if !covers {
			continue
		}
		written = true
		switch {
		case patternsHavePrefix(wr.patterns, gs):
			cands = append(cands, wr.patterns[len(gs):])
		case patternsImplied(gs, wr.patterns):
			cands = append(cands, nil)
		}
	}
	return cands, written
}

// checkConflicts reports a path written twice with values that may disagree,
// whose result is their unification: a copy and a constant, two copies of
// different paths, a constant and a computed value. Two struct literals merge,
// and a copy of a struct beside a struct literal is judged by the children
// they share.
func (w *walker) checkConflicts() {
	all := append([]*write(nil), w.writes...)
	// Children a copy gives implicitly, where the lens also writes them.
	for _, cw := range w.writes {
		if cw.kind != copyW {
			continue
		}
		for _, ew := range w.writes {
			if ew.q.String() == cw.q.String() || !cuetil.PathHasPrefix(ew.q, cw.q) {
				continue
			}
			rel := cuetil.TrimPathPrefix(ew.q, cw.q)
			src := appendPaths(cw.src, rel)
			if !w.lookupSource(src, nil).declared {
				continue
			}
			all = append(all, &write{q: ew.q, patterns: cw.patterns, kind: copyW, src: src, text: w.inputRef(src) + " (through " + w.inputRef(cw.src) + ")", pos: cw.pos})
		}
	}
	byPath := map[string][]*write{}
	var order []string
	for _, wr := range all {
		k := wr.q.String()
		if _, ok := byPath[k]; !ok {
			order = append(order, k)
		}
		byPath[k] = append(byPath[k], wr)
	}
	for _, k := range order {
		ws := byPath[k]
		for i := 0; i < len(ws); i++ {
			for j := i + 1; j < len(ws); j++ {
				a, b := ws[i], ws[j]
				switch {
				case a.kind == compositeW && b.kind == compositeW:
					continue
				case (a.kind == compositeW && b.kind == copyW) || (a.kind == copyW && b.kind == compositeW):
					continue
				case a.kind == copyW && b.kind == copyW && a.src.String() == b.src.String():
					continue
				case a.kind == b.kind && a.text == b.text:
					continue
				case patternsDisjoint(a.patterns, b.patterns):
					continue
				}
				w.add(Finding{Path: w.subject(a.q), Verdict: NotTotal, Rule: RuleWriteConflict,
					Detail: fmt.Sprintf("is written as %s and as %s, and the two unify, so one may reject the other's value; write the field once", a.text, b.text), Pos: b.pos})
			}
		}
	}
}

// walkLacunas checks each lacuna the lens declares: its condition is a pattern,
// and the values it reports are present where the condition holds.
func (w *walker) walkLacunas() {
	for _, c := range w.syn.LacunaConjuncts() {
		l, ok := c.Expr.(*ast.ListLit)
		if !ok {
			continue
		}
		for i, x := range l.Elts {
			switch x.(type) {
			case *ast.Ellipsis, *ast.Comprehension:
				// thema's [...Lacuna]; a conditional list is rejected by bind.
				continue
			}
			lp := cue.MakePath(cue.Str("lacunas"), cue.Index(i))
			var fields []cueview.FieldDecl
			for _, lc := range w.tree.Conjuncts(x) {
				if s, ok := lc.Expr.(*ast.StructLit); ok {
					fields = append(fields, cueview.FieldDecls(s.Elts, w.embedded)...)
				}
			}
			var gs []pattern
			for _, fd := range fields {
				if name, _, err := ast.LabelName(fd.Field.Label); err == nil && name == "condition" {
					gs = append(gs, w.lacunaCondition(lp, fd.Field.Value)...)
				}
			}
			for _, fd := range fields {
				name, _, err := ast.LabelName(fd.Field.Label)
				if err != nil {
					continue
				}
				switch name {
				case "sourceFields", "targetFields":
					w.lacunaRefs(lp, name, fd.Field.Value, gs)
				}
			}
		}
	}
}

// lacunaCondition parses a lacuna's condition as patterns.
func (w *walker) lacunaCondition(lp cue.Path, x ast.Expr) []pattern {
	cp := appendPath(lp, cue.Str("condition"))
	subj := cp.String()
	pos := w.lensPos(cp)
	var gs []pattern
	for _, c := range w.tree.Conjuncts(x) {
		switch e := c.Expr.(type) {
		case *ast.Ident:
			if w.predeclared(e) && (e.Name == "bool" || isBoolName(e.Name)) {
				continue
			}
		case *ast.BasicLit:
			continue
		}
		g, ok := w.parsePattern(c.Expr)
		if !ok {
			w.add(Finding{Path: subj, Verdict: Unknown, Rule: RuleUnknownPattern,
				Detail: fmt.Sprintf("is %s, which is not a pattern: a presence test (p != _|_), an equality (p == c) or a type test ((p & T) != _|_) on an input field, or a conjunction (&&) of these", text(c.Expr)), Pos: pos})
			g = []pattern{{root: w.from, kind: opaque, text: text(c.Expr)}}
		}
		w.checkPatterns(g, nil, subj, pos)
		gs = append(gs, g...)
	}
	return gs
}

// lacunaRefs checks the values a lacuna's sourceFields or targetFields report.
func (w *walker) lacunaRefs(lp cue.Path, name string, x ast.Expr, gs []pattern) {
	for _, c := range w.tree.Conjuncts(x) {
		l, ok := c.Expr.(*ast.ListLit)
		if !ok {
			continue
		}
		for j, ex := range l.Elts {
			rp := appendPath(appendPath(lp, cue.Str(name)), cue.Index(j))
			for _, rc := range w.tree.Conjuncts(ex) {
				s, ok := rc.Expr.(*ast.StructLit)
				if !ok {
					continue
				}
				for _, fd := range cueview.FieldDecls(s.Elts, w.embedded) {
					if fname, _, err := ast.LabelName(fd.Field.Label); err == nil && fname == "value" {
						vp := appendPath(rp, cue.Str("value"))
						w.lacunaValue(vp.String(), fd.Field.Value, gs, w.lensPos(vp))
					}
				}
			}
		}
	}
}

// lacunaValue checks that the value a lacuna reports is present when the
// lacuna's condition (gs) holds: input paths it reads are present, and a
// result field it refers to is written under the condition.
func (w *walker) lacunaValue(subj string, x ast.Expr, gs []pattern, pos token.Pos) {
	if r, ok := w.resultRef(x); ok {
		// The field is present when the lacuna applies if its writes cover
		// every instance, whatever the condition, or cover the condition.
		all, written := w.candidates(nil, r)
		under, _ := w.candidates(gs, r)
		switch {
		case !written:
			w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleSourceAbsent,
				Detail: fmt.Sprintf("refers to %s, which the lens never writes", w.subject(r)), Pos: pos})
		case w.covers(all), w.covers(under):
		default:
			w.add(Finding{Path: subj, Verdict: Unknown, Rule: RuleUnknownExhaustive,
				Detail: fmt.Sprintf("refers to %s, which is written only under %s: neither covering every instance nor implied by the lacuna's condition%s", w.subject(r), describePatternSets(all), underPatterns(gs)), Pos: pos})
		}
		return
	}
	for _, p := range w.inputRefs(x) {
		src := w.lookupSource(p, gs)
		switch {
		case !src.declared:
			w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleSourceUndeclared,
				Detail: fmt.Sprintf("reads %s, which the source schema does not declare", w.inputRef(p)), Pos: pos})
		case !src.present:
			w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleSourceAbsent,
				Detail: fmt.Sprintf("reads %s, which may be absent when the lacuna applies: %s; add %s != _|_ to the condition", w.inputRef(p), src.why, w.inputRef(p)), Pos: pos})
		}
	}
}

// resultRef returns the result path x refers to, if x is a selection into the
// lens's result.
func (w *walker) resultRef(x ast.Expr) (cue.Path, bool) {
	root, ok := chainRoot(x)
	if !ok {
		return cue.Path{}, false
	}
	f := w.refField(root)
	if f == nil {
		return cue.Path{}, false
	}
	isResult := false
	for _, rf := range w.syn.ResultFields() {
		if rf == f {
			isResult = true
		}
	}
	if !isResult {
		return cue.Path{}, false
	}
	var sels []cue.Selector
	for x != root {
		switch e := x.(type) {
		case *ast.ParenExpr:
			x = e.X
		case *ast.SelectorExpr:
			name, _, err := ast.LabelName(e.Sel)
			if err != nil {
				return cue.Path{}, false
			}
			sels = append([]cue.Selector{cue.Str(name)}, sels...)
			x = e.X
		case *ast.IndexExpr:
			lit, ok := e.Index.(*ast.BasicLit)
			if !ok || lit.Kind != token.INT {
				return cue.Path{}, false
			}
			i, err := strconv.Atoi(lit.Value)
			if err != nil {
				return cue.Path{}, false
			}
			sels = append([]cue.Selector{cue.Index(i)}, sels...)
			x = e.X
		default:
			return cue.Path{}, false
		}
	}
	return cue.MakePath(sels...), true
}

// inputRefs returns the input paths the expression x reads.
func (w *walker) inputRefs(x ast.Expr) []cue.Path {
	var out []cue.Path
	ast.Walk(x, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.SelectorExpr, *ast.IndexExpr, *ast.Ident:
			found := false
			for _, c := range w.tree.Conjuncts(n.(ast.Expr)) {
				if w.syn.FromInput(c) {
					if p, ok := w.sourcePath(c); ok {
						out = append(out, p)
					}
					found = true
				}
			}
			if found {
				return false
			}
		}
		return true
	}, nil)
	return out
}

// pathOf returns the input path the expression x reads, if x is exactly a
// read of one input path.
func (w *walker) pathOf(x ast.Expr) (cue.Path, bool) {
	conjs := w.tree.Conjuncts(x)
	if len(conjs) == 0 {
		return cue.Path{}, false
	}
	var p cue.Path
	for i, c := range conjs {
		if !w.syn.FromInput(c) {
			return cue.Path{}, false
		}
		cp, ok := w.sourcePath(c)
		if !ok || (i > 0 && cp.String() != p.String()) {
			return cue.Path{}, false
		}
		p = cp
	}
	return p, true
}

var exportComment = regexp.MustCompile(`\s*// explicit error \(_\|_ literal\) in source`)

// text renders a syntax node for a message, on one line, without the comments
// the exporter attaches.
func text(n ast.Node) string {
	b, err := format.Node(n, format.Simplify())
	if err != nil {
		return fmt.Sprintf("<%T>", n)
	}
	s := exportComment.ReplaceAllString(string(b), "")
	return strings.Join(strings.Fields(s), " ")
}

// errMsg renders the first error in err without its position or path, which
// name the checker's closed copy of the target rather than anything the
// author wrote.
func errMsg(err error) string {
	errs := cerrors.Errors(err)
	if len(errs) == 0 {
		return err.Error()
	}
	f, args := errs[0].Msg()
	return fmt.Sprintf(f, args...)
}

// fmtv formats a value for a message, on one line.
func fmtv(v cue.Value) string {
	return strings.Join(strings.Fields(fmt.Sprint(v)), " ")
}

func appendPath(p cue.Path, sel cue.Selector) cue.Path {
	return cue.MakePath(append(append([]cue.Selector(nil), p.Selectors()...), sel)...)
}

func appendPaths(p, q cue.Path) cue.Path {
	return cue.MakePath(append(append([]cue.Selector(nil), p.Selectors()...), q.Selectors()...)...)
}

func literalString(lit *ast.BasicLit) (string, error) {
	var s string
	_, err := fmt.Sscanf(lit.Value, "%q", &s)
	return s, err
}
