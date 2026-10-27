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
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/cueview"
	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/lensir"
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
	// RuleSourceConstrained: the lens constrains the field it takes (v0_0: {a:
	// int & <10}, v0_0: {a: *"x" | string}), so what it receives is no longer
	// what the source schema says an instance is.
	RuleSourceConstrained Rule = "source-constrained"
)

// Rules that leave the verdict Unknown.
const (
	// RuleUnknownCopy: compat.Check cannot decide whether the copied value fits
	// the target.
	RuleUnknownCopy Rule = "unknown-copy"
	// RuleUnknownPattern: a comprehension's condition is not a presence test, an
	// equality or a type test on an input field, or a lacuna's condition is
	// neither one of those nor a boolean the computed rules prove.
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
// Total, and, where the lens is Total, its program.
type Result struct {
	Findings []Finding
	// Program is the lens's program (internal/lensir), built as a by-product
	// of the proof: nil unless the lens is Total. It is also nil, with
	// ProgramErr saying why, where the lens is Total but uses a construct the
	// IR does not yet carry.
	Program    *lensir.Program
	ProgramErr error
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
		pinned:  map[string]cue.Value{},
	}
	w.ir = newIRBuilder(w)
	syn, err := cuetil.ParseLens(lens, from, to)
	if err != nil {
		w.add(Finding{Path: to, Verdict: Unknown, Rule: RuleUnknownReference,
			Detail: fmt.Sprintf("could not be read as syntax: %v", err), Pos: w.lensPos(w.toPath())})
		return w.result()
	}
	w.syn, w.tree = syn, syn.Tree()
	w.checkInput()
	w.walkResult()
	w.walkLacunas()
	w.checkCopies()
	w.checkExhaustive()
	w.checkConflicts()
	res := w.result()
	if res.Total() {
		// The program is what the walk proved. Only here is one built: a
		// lens with a finding has no program (the lensir-programs seam).
		if w.ir.err != nil {
			res.ProgramErr = w.ir.err
		} else {
			res.Program = &lensir.Program{Result: w.ir.result(), Lacunas: w.ir.lacunas}
		}
	}
	return res
}

var lacunasPath = cue.MakePath(cue.Str("lacunas"))

type walker struct {
	ctx  *cue.Context
	lens cue.Value
	// from and to name the lens's source and result fields (cuetil.LensParam).
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
	// write is recorded once, for the whole, and for lacuna fields, which are
	// not writes.
	quiet int
	// hidden is set while walking a hidden result field, which is not data: the
	// target owes it nothing and an absent operand leaves it incomplete rather
	// than failing, but it is evaluated, so an evaluation error fails the lens.
	hidden  int
	targets map[string]targetLookup
	// pinned holds the value a lens path must fit where that is not the target
	// schema's: a lacuna's message is a string.
	pinned map[string]cue.Value
	// absentAdvice says what to do about a path that may be absent, where it is
	// read: a result field goes under a presence test, a lacuna's condition
	// gains one.
	absentAdvice func(p cue.Path) string
	// ir collects the lens's program as the walk proves it.
	ir *irBuilder
}

// checkInput refuses a lens that constrains the field it takes. Translate fills
// the field with the instance unified with the source schema, and anything the
// lens adds there (a bound, a default) changes what the lens sees, so that the
// source schema no longer says what its instances are. thema's own constraint
// on the field is known by its position in thema's CUE.
func (w *walker) checkInput() {
	_, args := w.lens.LookupPath(w.fromPath()).Expr()
	for _, a := range args {
		if embedreg.IsKernel(a.Pos()) || isTop(a) {
			continue
		}
		w.add(Finding{Path: w.from, Verdict: NotTotal, Rule: RuleSourceConstrained,
			Detail: fmt.Sprintf("is %s, a constraint on what the lens receives; %s must be _, so that the source schema alone says what the lens takes", fmtv(a), w.from), Pos: a.Pos()})
	}
}

// fromPath and toPath are the lens's source and result fields.
func (w *walker) fromPath() cue.Path { return cue.MakePath(cue.Str(w.from)) }
func (w *walker) toPath() cue.Path   { return cue.MakePath(cue.Str(w.to)) }

// isTop reports whether v is _, and nothing more: no default, no constraint.
func isTop(v cue.Value) bool {
	if _, hasDefault := v.Default(); hasDefault {
		return false
	}
	op, _ := v.Expr()
	return op == cue.NoOp && v.IncompleteKind() == cue.TopKind
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
	if w.quiet == 0 && w.hidden == 0 {
		w.writes = append(w.writes, wr)
	}
}

// subject names the lens path q in findings: a path within the result, or a
// lacuna field's path from lacunas.
func (w *walker) subject(q cue.Path) string {
	if cuetil.PathHasPrefix(q, lacunasPath) {
		return q.String()
	}
	return rootRef(w.to, q)
}

// absent says what to do about input path p, which may be absent where it is
// read.
func (w *walker) absent(p cue.Path) string {
	if w.absentAdvice != nil {
		return w.absentAdvice(p)
	}
	return fmt.Sprintf("write the field under if %s != _|_", w.inputRef(p))
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
	return embedreg.AuthorPos(w.lens.LookupPath(p))
}

// resultPos is the author's position for the result field q, or fallback where
// the field is not in the lens's value (declared inside a comprehension).
func (w *walker) resultPos(q cue.Path, fallback token.Pos) token.Pos {
	sels := append([]cue.Selector{cue.Str(w.to)}, q.Selectors()...)
	if p := embedreg.AuthorPos(w.lens.LookupPath(cue.MakePath(sels...))); p.IsValid() {
		return p
	}
	return fallback
}

// walkResult walks the author's conjuncts of the result. thema's own conjunct
// on every result (Lens.result in lineage.cue) is known by its position in
// thema's CUE: the lens is thema's Lens unified with the author's, and the
// kernel's result is a builtin call. The exported syntax the walk reads has no
// positions, but there too the kernel's conjunct is a call, and the author's
// conjuncts are everything else. Where the author wrote a call conjunct too,
// the two cannot be told apart, and neither is proven.
func (w *walker) walkResult() {
	root := cue.MakePath()
	pos := w.resultPos(root, token.NoPos)
	kernel := 0
	_, args := w.lens.Expr()
	for _, a := range args {
		if !embedreg.IsKernel(a.Pos()) {
			continue
		}
		if op, _ := a.LookupPath(w.toPath()).Expr(); op == cue.CallOp {
			kernel++
		}
	}
	var calls, rest []cueview.Conjunct
	for _, c := range w.syn.ResultConjuncts() {
		if _, ok := c.Expr.(*ast.CallExpr); ok && !w.syn.FromInput(c) {
			calls = append(calls, c)
		} else {
			rest = append(rest, c)
		}
	}
	if len(calls) > kernel {
		for _, c := range calls {
			w.add(Finding{Path: w.subject(root), Verdict: Unknown, Rule: RuleUnknownOp,
				Detail: fmt.Sprintf("is constrained by %s, a call the checker does not prove; write %s as a struct", text(c.Expr), w.to), Pos: pos})
		}
	}
	for _, c := range rest {
		w.conjunct(c, root, nil, pos)
	}
}

// conjunct handles one conjunct of the value written at q, under patterns gs.
func (w *walker) conjunct(c cueview.Conjunct, q cue.Path, gs []pattern, pos token.Pos) {
	if w.hidden > 0 {
		w.hiddenConjunct(c, q, gs, pos)
		return
	}
	if c.Disjunction != nil {
		// Every disjunct is a conjunct of the field; the disjunction is one
		// write, and one finding.
		w.record(&write{q: q, patterns: gs, kind: literalW, text: text(c.Disjunction), pos: pos})
		w.add(Finding{Path: w.subject(q), Verdict: NotTotal, Rule: RuleNotAValue,
			Detail: fmt.Sprintf("is %s, a disjunction rather than a value; a result field must have one concrete value", text(c.Disjunction)), Pos: pos})
		return
	}
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

// hiddenConjunct handles one conjunct of a hidden result field (see hidden): a
// copy must read a declared path, a struct's fields are walked in turn, and a
// computed expression must evaluate.
func (w *walker) hiddenConjunct(c cueview.Conjunct, q cue.Path, gs []pattern, pos token.Pos) {
	if w.syn.FromInput(c) {
		if p, ok := w.sourcePath(c); ok && !w.lookupSource(p, gs).declared {
			w.add(Finding{Path: w.subject(q), Verdict: NotTotal, Rule: RuleSourceUndeclared,
				Detail: fmt.Sprintf("copies %s, which the source schema does not declare", w.inputRef(p)), Pos: pos})
		}
		return
	}
	switch e := c.Expr.(type) {
	case *ast.StructLit:
		w.decls(e.Elts, q, gs, pos)
	case *ast.ListLit:
		for i, x := range e.Elts {
			switch x.(type) {
			case *ast.Comprehension, *ast.Ellipsis:
				return
			}
			for _, ec := range w.tree.Conjuncts(x) {
				w.conjunct(ec, appendPath(q, cue.Index(i)), gs, pos)
			}
		}
	case *ast.BasicLit, *ast.Ident:
	case *ast.BottomLit:
		w.add(Finding{Path: w.subject(q), Verdict: NotTotal, Rule: RuleNotAValue,
			Detail: "is _|_, an error", Pos: pos})
	default:
		if c.Disjunction == nil {
			w.computed(c.Expr, q, gs, pos)
		}
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
			s, err := literal.Unquote(lit.Value)
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
	w.ir.emit(&lensir.Copy{To: w.ir.path(q), From: w.ir.path(p)})
	src := w.lookupSource(p, gs)
	subj := w.subject(q)
	if !src.declared {
		w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleSourceUndeclared,
			Detail: fmt.Sprintf("copies %s, which the source schema does not declare", w.inputRef(p)), Pos: pos})
		return
	}
	if !src.present {
		w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleSourceAbsent,
			Detail: fmt.Sprintf("copies %s, which may be absent: %s; %s", w.inputRef(p), src.why, w.absent(p)), Pos: pos})
	}
	tgt, ok := w.targetAt(q, pos)
	if !ok {
		return
	}
	// An instance that omits a regular field with a default compares as the
	// default, yet Translate fills the input with the instance unified with
	// the source schema, so what is copied is the defaulted disjunction
	// (*1 | int), and in a target with another default it has two defaults
	// and no value. Where the patterns admit the default, the copy may take
	// it. (An optional field an instance omits stays absent, so its default
	// is never copied; and beneath a copied struct, compat sees the defaults:
	// compat.Result.Instances.)
	if !src.optional {
		for _, v := range src.whole {
			d, hasDefault := v.Default()
			if !hasDefault {
				continue
			}
			if admitted, _ := refine([]cue.Value{d}, p, gs); len(admitted) == 0 {
				continue
			}
			if e, ok := tgt.Default(); ok && cuetil.Equal(d, e) != nil {
				w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleCopyNarrowed,
					Detail: fmt.Sprintf("copies %s, which an instance may omit; it then fills with %s in the source schema and with %s in the target schema, and the copy has no single value", w.inputRef(p), fmtv(d), fmtv(e)), Pos: pos})
			}
		}
	}
	w.copies = append(w.copies, pendingCopy{q: q, p: p, branches: src.branches, tgt: tgt, pos: pos})
}

// A pendingCopy is a copy of input p into result q whose alternatives
// (branches: each alternative of the source at p, refined by the patterns the
// copy is written under) await the compatibility judgement against the target
// at q.
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
				if (f.Rule == compat.RuleFieldAddedRequired || f.Rule == compat.RuleFieldNowRequired) && w.writesAt(appendPaths(c.q, f.Path)) {
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
	if f.Rule == compat.RuleDefaultChanged {
		detail += "; an instance omitting the field copies the source's default beside the target's, and the two give it no single value"
	}
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
	w.ir.push()
	w.decls(e.Elts, q, gs, pos)
	w.ir.emit(&lensir.Wrap{To: w.ir.path(q), Kind: lensir.StructWrap, Children: w.ir.pop()})
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
	w.ir.push()
	for i, x := range e.Elts {
		eq := appendPath(q, cue.Index(i))
		for _, c := range w.tree.Conjuncts(x) {
			w.conjunct(c, eq, gs, pos)
		}
	}
	w.ir.emit(&lensir.Wrap{To: w.ir.path(q), Kind: lensir.ListWrap, Len: len(e.Elts), Children: w.ir.pop()})
}

// literal proves the scalar literal x written at q.
func (w *walker) literal(x ast.Expr, q cue.Path, gs []pattern, pos token.Pos) {
	w.record(&write{q: q, patterns: gs, kind: literalW, text: text(x), pos: pos})
	v := w.ctx.BuildExpr(x)
	w.ir.emit(&lensir.Constant{To: w.ir.path(q), Value: w.ir.literal(v)})
	tgt, ok := w.targetAt(q, pos)
	if !ok {
		return
	}
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
			if strings.HasPrefix(name, "#") {
				// Definitions are not data. A regular field that refers to one
				// is followed to what it refers to.
				continue
			}
			if x.Constraint == token.OPTION || x.Constraint == token.NOT {
				// a?: x and a!: x constrain a field without writing it.
				continue
			}
			cq := appendPath(q, cue.Str(name))
			cpos := w.resultPos(cq, pos)
			if strings.HasPrefix(name, "_") {
				// A hidden field is not data either, but it is evaluated.
				cq = appendPath(q, cue.Hid(name, "_"))
				w.hidden++
				for _, c := range w.tree.Conjuncts(x.Value) {
					w.conjunct(c, cq, gs, cpos)
				}
				w.hidden--
				continue
			}
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
			w.ir.push()
			if s, ok := x.Value.(*ast.StructLit); ok {
				w.decls(s.Elts, q, ngs, cpos)
			} else {
				for _, c := range w.tree.Conjuncts(x.Value) {
					w.conjunct(c, q, ngs, cpos)
				}
			}
			w.ir.branch(ngs[len(gs):])
		case *ast.EmbedDecl:
			for _, c := range w.tree.Conjuncts(x.Expr) {
				w.conjunct(c, q, gs, pos)
			}
		case ast.Expr:
			// An embedding beside a comprehension is exported as a bare
			// expression (cueview, comprehensions golden).
			for _, c := range w.tree.Conjuncts(x) {
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
	if v, ok := w.pinned[key]; ok {
		return targetLookup{v: v, ok: true}
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
	// optional reports that some step of the path need not exist in an
	// instance, whatever the patterns establish: a field an instance omits
	// stays absent, where a regular field with a default is filled.
	optional bool
	why      string // why the path may be absent
}

// source looks up input path p as read under patterns gs: its alternatives,
// whether the source schema declares it, and whether it is present in every
// instance (every step required or defaulted, or established by a presence or
// type test among gs).
func (w *walker) lookupSource(p cue.Path, gs []pattern) sourceLookup {
	cur := []cue.Value{w.source}
	present, anyOptional := true, false
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
		anyOptional = anyOptional || optional
		cur = next
	}
	var leaves []cue.Value
	for _, v := range cur {
		leaves = append(leaves, compat.Branches(v)...)
	}
	branches, refined := refine(leaves, p, gs)
	return sourceLookup{branches: branches, whole: cur, refined: refined, declared: true, present: present, optional: anyOptional, why: why}
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
// cq a value too, where the instance has it. written reports whether cq is
// written at all.
func (w *walker) candidates(gs []pattern, cq cue.Path) (cands [][]pattern, written bool) {
	for _, wr := range w.writes {
		patterns := wr.patterns
		switch {
		case wr.q.String() == cq.String():
		case wr.kind == copyW && cuetil.PathHasPrefix(cq, wr.q):
			var ok bool
			if patterns, ok = w.copyChildPatterns(wr, cuetil.TrimPathPrefix(cq, wr.q)); !ok {
				continue
			}
		default:
			continue
		}
		written = true
		switch {
		case patternsHavePrefix(patterns, gs):
			cands = append(cands, patterns[len(gs):])
		case patternsImplied(gs, patterns):
			cands = append(cands, nil)
		}
	}
	return cands, written
}

// copyChildPatterns returns the patterns under which the copy cw gives the
// field rel beneath it a value: the copy's own, and where the source path may
// be absent, its presence. ok is false where the source does not declare the
// path.
func (w *walker) copyChildPatterns(cw *write, rel cue.Path) ([]pattern, bool) {
	src := w.lookupSource(appendPaths(cw.src, rel), cw.patterns)
	if !src.declared {
		return nil, false
	}
	patterns := cw.patterns
	if !src.present {
		patterns = append(append([]pattern(nil), patterns...), pattern{root: w.from, kind: presence, path: appendPaths(cw.src, rel)})
	}
	return patterns, true
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
			patterns, ok := w.copyChildPatterns(cw, rel)
			if !ok {
				continue
			}
			all = append(all, &write{q: ew.q, patterns: patterns, kind: copyW, src: src, text: w.inputRef(src) + " (through " + w.inputRef(cw.src) + ")", pos: cw.pos})
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
// its message is a string and its type a literal, and the values it reports are
// present where the condition holds. Lacuna fields are not writes, and a path
// they read that may be absent is fixed in the condition.
func (w *walker) walkLacunas() {
	w.quiet++
	defer func() { w.quiet-- }()
	w.absentAdvice = func(p cue.Path) string {
		return fmt.Sprintf("add %s != _|_ to the lacuna's condition", w.inputRef(p))
	}
	defer func() { w.absentAdvice = nil }()
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
			w.ir.beginLacuna()
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
				case "message":
					w.lacunaMessage(lp, fd.Field.Value, gs)
				case "type":
					w.lacunaType(lp, fd.Field.Value)
				}
			}
			w.ir.endLacuna(lp)
		}
	}
}

// lacunaMessage checks a lacuna's message as a value that must be a string
// where the condition holds: a copy, a literal or a computation, judged as a
// result field would be against a target of string.
func (w *walker) lacunaMessage(lp cue.Path, x ast.Expr, gs []pattern) {
	mp := appendPath(lp, cue.Str("message"))
	w.pinned[mp.String()] = w.ctx.CompileString("string")
	pos := w.lensPos(mp)
	w.ir.push()
	for _, c := range w.tree.Conjuncts(x) {
		w.conjunct(c, mp, gs, pos)
	}
	w.ir.lacuna.Message = w.ir.value(w.ir.pop())
}

// lacunaType checks that a lacuna's type is written as a literal, or as a
// reference into thema (thema.LacunaTypes.X), which is not data either.
func (w *walker) lacunaType(lp cue.Path, x ast.Expr) {
	tp := appendPath(lp, cue.Str("type"))
	for _, c := range w.tree.Conjuncts(x) {
		if lit, ok := c.Expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			continue
		}
		if root, ok := chainRoot(c.Expr); ok {
			if _, imported := root.Node.(*ast.ImportSpec); imported {
				continue
			}
		}
		w.add(Finding{Path: tp.String(), Verdict: NotTotal, Rule: RuleNotAValue,
			Detail: fmt.Sprintf("is %s, which is not a literal; write a lacuna's type as one of thema's lacuna types, as a string or as thema.LacunaTypes.<name>", text(c.Expr)), Pos: w.lensPos(tp)})
	}
}

// lacunaCondition parses a lacuna's condition: patterns, or a guard.
func (w *walker) lacunaCondition(lp cue.Path, x ast.Expr) []pattern {
	cp := appendPath(lp, cue.Str("condition"))
	subj := cp.String()
	pos := w.lensPos(cp)
	var gs []pattern
	var when []lensir.Expr
	never := false
	for _, c := range w.tree.Conjuncts(x) {
		switch e := c.Expr.(type) {
		case *ast.Ident:
			if w.predeclared(e) && (e.Name == "bool" || isBoolName(e.Name)) {
				never = never || e.Name == falseName
				continue
			}
		case *ast.BasicLit:
			continue
		}
		g, ok := w.parsePattern(c.Expr)
		if !ok {
			if x := w.guard(c.Expr, subj, pos); x != nil {
				when = append(when, x)
			}
			g = []pattern{{root: w.from, kind: opaque, text: text(c.Expr)}}
		} else {
			when = append(when, &lensir.Match{Tests: w.ir.tests(g)})
		}
		w.checkPatterns(g, nil, subj, pos)
		gs = append(gs, g...)
	}
	w.ir.condition(when, never)
	return gs
}

// guard proves a lacuna condition outside the pattern forms: a boolean the
// computed-expression rules prove to evaluate for every instance. A guard
// establishes nothing about the source, so it is kept as an opaque pattern,
// under which the lacuna's values must be present for every instance. It
// returns the guard as the program's expression, where it is proven.
func (w *walker) guard(x ast.Expr, subj string, pos token.Pos) lensir.Expr {
	c := newExprCtx(subj, nil, pos)
	sh, ok := w.shape(x, c)
	switch {
	case !ok, sh.dead:
		// The rules reported what is outside the provable set.
	case sh.kind != cue.BoolKind:
		w.add(Finding{Path: subj, Verdict: Unknown, Rule: RuleUnknownPattern,
			Detail: fmt.Sprintf("is %s, which is neither a pattern (a presence test, an equality or a type test on an input field, or a conjunction of these) nor a boolean: it is %s", text(x), sh), Pos: pos})
	default:
		return w.ir.expr(x, c)
	}
	return nil
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
						w.ir.fieldRef(name, rp, w.lacunaValue(vp.String(), fd.Field.Value, gs, w.lensPos(vp)))
					}
				}
			}
		}
	}
}

// lacunaValue checks that the value a lacuna reports evaluates when the
// lacuna's condition (gs) holds: input paths it reads are present, a result
// field it refers to is written under the condition, and a computation is
// over the provable operations. It returns the value as the program's
// expression.
func (w *walker) lacunaValue(subj string, x ast.Expr, gs []pattern, pos token.Pos) lensir.Expr {
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
		return &lensir.ResultRef{Path: w.ir.path(r)}
	}
	if p, isPath := w.pathOf(x); isPath {
		w.lacunaInputs(subj, x, gs, pos)
		return &lensir.Ref{Path: w.ir.path(p)}
	}
	switch e := x.(type) {
	case *ast.BasicLit:
		return w.ir.literal(w.ctx.BuildExpr(e))
	case *ast.StructLit, *ast.ListLit:
		w.lacunaInputs(subj, x, gs, pos)
		w.ir.failf("%s is a struct or list, which the IR carries only as a result field", subj)
		return nil
	}
	// A computation: it must evaluate, whatever it is worth.
	c := newExprCtx(subj, gs, pos)
	if _, ok := w.shape(x, c); !ok {
		return nil
	}
	return w.ir.expr(x, c)
}

// lacunaInputs checks that the input paths the lacuna value x reads are
// present when the lacuna's condition (gs) holds.
func (w *walker) lacunaInputs(subj string, x ast.Expr, gs []pattern, pos token.Pos) {
	for _, p := range w.inputRefs(x) {
		src := w.lookupSource(p, gs)
		switch {
		case !src.declared:
			w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleSourceUndeclared,
				Detail: fmt.Sprintf("reads %s, which the source schema does not declare", w.inputRef(p)), Pos: pos})
		case !src.present:
			w.add(Finding{Path: subj, Verdict: NotTotal, Rule: RuleSourceAbsent,
				Detail: fmt.Sprintf("reads %s, which may be absent when the lacuna applies: %s; %s", w.inputRef(p), src.why, w.absent(p)), Pos: pos})
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

// text renders a syntax node for a message, on one line, without the comments
// the exporter attaches (it annotates a comparison with _|_). The comments are
// removed from the node: the walk reads structure, never comments.
func text(n ast.Node) string {
	ast.Walk(n, func(n ast.Node) bool {
		ast.SetComments(n, nil)
		return true
	}, nil)
	b, err := format.Node(n, format.Simplify())
	if err != nil {
		return fmt.Sprintf("<%T>", n)
	}
	return strings.Join(strings.Fields(string(b)), " ")
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
