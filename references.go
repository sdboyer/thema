package thema

import (
	"fmt"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/util"
)

// What a schema or a lens may refer to. The rules, their IDs and what an
// author does about each are in docs/invariants.md, References.
//
// A schema may refer only to its own body (anything declared inside it, at any
// depth) and to CUE's builtin packages. A lens may refer only to the instance
// it takes (the field named after its from version, v1_0 for a lens from 1.0),
// its own body and the builtin packages. thema's own package is part of the
// substrate (thema.Lacuna, thema.LacunaTypes), not something an author can
// edit, so it is allowed too.
//
// References are found in the value: each field of the owner is split into its
// expression tree with Expr(), and every node that is a reference reports,
// through ReferencePath(), the root it resolves in and its absolute path there.
// The pair classifies it: a root that is a builtin or thema's package is
// allowed, another package is ref-import, and in the author's own instance the
// path is compared with the lineage's and the owner's: within the owner is its
// own body, under schemas or lenses names another schema or lens, elsewhere in
// the lineage is lineage meta, and outside the lineage is outside. The
// evaluator has already resolved aliases, selector chains and constant index
// expressions, so each arrives as a plain path; an index that depends on the
// instance stays an IndexOp, whose base is classified with the selection
// unknown.
//
// A let is substituted where it is used, so a field using one shows a plain
// value with no ReferencePath; cue.Dereference reaches the let's value, which
// carries the let's expression (its own Expr and ReferencePath) and is walked
// in the using field's place. What a let is bound to is classified, not where
// the let is declared: a let outside the owner bound to a literal is not seen
// (testdata/known/ref-lineage-let-literal).
//
// One construct leaves no trace in the value and is read from the owner's
// syntax instead (references_syntax.go): whatever a comprehension declares or
// tests while the comprehension is pending, which is not part of the value at
// all.

// Labels of the lineage fields the reference rules tell apart.
const (
	labelSchemas    = "schemas"
	labelLenses     = "lenses"
	labelJoinSchema = "joinSchema"
)

// refRule is a rule of the reference policy, named as in docs/invariants.md.
type refRule string

const (
	refSiblingSchema  refRule = "ref-sibling-schema"
	refLineageMeta    refRule = "ref-lineage-meta"
	refOutsideLineage refRule = "ref-outside-lineage"
	refImport         refRule = "ref-import"
	refLensToSchema   refRule = "ref-lens-to-schema"
	refLensToLens     refRule = "ref-lens-to-lens"
	refUnresolved     refRule = "ref-unresolved"
)

// maxRefDepth bounds the descent into one field's value: its expression tree
// and the fields and elements of the structs and lists in it. References are
// classified where they are found and never followed, so the descent is over
// what the author wrote at that field; a value this deep is reported rather
// than descended further.
const maxRefDepth = 64

// selectionOperands is how many operands a selection (SelectorOp) or an index
// expression (IndexOp) has: the value selected from and the label or index.
const selectionOperands = 2

// refOwner is a value whose references are checked: one schema, the
// joinSchema, or one lens.
type refOwner struct {
	// name is how the owner is referred to in errors: "schema 0.1",
	// "joinSchema", "lens 0.1->0.0".
	name string
	// v is the owner within the raw lineage.
	v cue.Value
	// rel is the owner's path relative to the lineage.
	rel cue.Path
	// lens says whether the owner is a lens; otherwise it is a schema or the
	// joinSchema, which follow the schema rules.
	lens bool
	// takes is the name of the field a lens binds the instance it takes to
	// (cuetil.LensParam), for the lens rule's wording.
	takes string
}

// refFinding is one reference that breaks a rule.
type refFinding struct {
	rule refRule
	// field is the path of labels, within the owner, of the field whose value
	// holds the reference, as far as it can be located: labels built by
	// comprehensions or interpolation have no field. A reference reached
	// through a let is located at the field using the let.
	field []string
	// expr is the reference: the path it resolves to, or the text it was
	// written as where only the syntax shows it.
	expr string
	// pkg is the import path of the package referred to (ref-import).
	pkg string
	// pos is where the reference is, if the value knows.
	pos token.Pos
}

// String renders the finding in the house format:
//
//	<path> <what is wrong> (rule <name>)
func (f refFinding) String(o refOwner) string {
	subject := o.name
	if len(f.field) > 0 {
		subject = o.name + ": " + strings.Join(f.field, ".")
	}
	var what string
	switch f.rule {
	case refSiblingSchema:
		what = "a schema in the lineage"
	case refLensToSchema:
		what = "a schema"
	case refLensToLens:
		what = "another lens"
	case refLineageMeta:
		what = "which is declared in the lineage outside the " + o.kind()
	case refOutsideLineage:
		what = "which is declared outside the lineage"
	case refImport:
		what = fmt.Sprintf("in package %q, which is not one of CUE's builtin packages", f.pkg)
	case refUnresolved:
		return fmt.Sprintf("%s refers to %s, which thema cannot resolve statically; write the reference with constant labels, or declare what it needs inside the %s (rule %s)", subject, f.expr, o.kind(), f.rule)
	}
	return fmt.Sprintf("%s refers to %s, %s; %s (rule %s)", subject, f.expr, what, o.may(), f.rule)
}

func (o refOwner) kind() string {
	switch {
	case o.lens:
		return "lens"
	case o.name == labelJoinSchema:
		return labelJoinSchema
	}
	return "schema"
}

func (o refOwner) may() string {
	if o.lens {
		return "a lens may refer only to " + o.takes + ", the instance it takes, its own body and CUE's builtin packages"
	}
	return "a " + o.kind() + " may refer only to its own body and CUE's builtin packages"
}

// refChecker checks the owners of one lineage.
type refChecker struct {
	ctx *cue.Context
	// lin is the lineage as bound: the author's value, with thema.Lineage
	// unified in where the author did so.
	lin cue.Value
	// files are the files the author declared the lineage in: the filenames
	// of the positions of its conjuncts and of its top-level fields, outside
	// thema's own CUE.
	files map[string]bool
	// predeclared caches which unresolved identifiers CUE predeclares (int,
	// string, len, self, ...), asked of the evaluator; see references_syntax.go.
	predeclared map[string]bool
}

func newRefChecker(ctx *cue.Context, lin cue.Value) *refChecker {
	rc := &refChecker{ctx: ctx, lin: lin, files: make(map[string]bool), predeclared: make(map[string]bool)}
	for _, f := range declaringFiles(lin) {
		rc.files[f] = true
	}
	return rc
}

// declaringFiles returns the files a value is declared in: the filenames of
// the positions of its conjuncts and of its top-level fields' conjuncts,
// outside thema's own CUE. A value thema unified in Go has no position of its
// own, but its conjuncts and fields keep theirs.
func declaringFiles(v cue.Value) []string {
	var files []string
	note := func(v cue.Value) {
		for _, c := range cuetil.AppendSplit(v, cue.AndOp, nil) {
			if pos := c.Pos(); pos.IsValid() && !embedreg.IsKernel(pos) {
				files = append(files, pos.Filename())
			}
		}
	}
	note(v)
	if iter, err := v.Fields(); err == nil {
		for iter.Next() {
			note(iter.Value())
		}
	}
	return files
}

// check returns the references in o that break a rule: those the value shows,
// in field order, then those only the syntax shows (pending comprehensions).
func (rc *refChecker) check(o refOwner) []refFinding {
	w := &refWalker{rc: rc, owner: o, seen: make(map[string]bool)}
	w.walkValue(o.v, 0)
	w.walkSyntax()
	return w.findings
}

// isAuthorRoot reports whether root, the value a reference resolves in, is the
// author's own instance: the one that holds the lineage at its path, declared
// in the lineage's files. Each package is a root of its own, declared in its
// own files, so a reference into another package resolves in a root whose
// value at the lineage's path, if any, is declared elsewhere. The lineage
// value itself cannot be compared with root: it may be a value thema unified
// in Go from several declarations, and a value's position tells where its
// first conjunct is, not what it is. The files are matched two ways, as a root
// knows them differently by how it was built: an instance loaded from files
// lists them (BuildInstance), and one built from syntax (BuildFile) has only
// the positions its nodes carry.
func (rc *refChecker) isAuthorRoot(root cue.Value) bool {
	at := root.LookupPath(rc.lin.Path())
	if !at.Exists() {
		return false
	}
	if len(rc.files) == 0 {
		// A lineage with no positions: nothing more to compare.
		return true
	}
	if bi := root.BuildInstance(); bi != nil {
		for _, f := range bi.Files {
			if rc.files[f.Filename] {
				return true
			}
		}
	}
	for _, f := range declaringFiles(at) {
		if rc.files[f] {
			return true
		}
	}
	return false
}

// relToLineage returns p, an absolute path in the author's instance under the
// lineage, relative to the lineage.
func (rc *refChecker) relToLineage(p cue.Path) cue.Path {
	return cue.MakePath(p.Selectors()[len(rc.lin.Path().Selectors()):]...)
}

// refWalker walks one owner's fields, classifying each reference.
type refWalker struct {
	rc    *refChecker
	owner refOwner
	// field is the path of labels of the field being visited.
	field    []string
	findings []refFinding
	// seen dedupes findings: the same reference can be reached from the value
	// and from the syntax.
	seen map[string]bool
	// usePos, when valid, positions findings at the field using a let, while
	// the let's value is walked.
	usePos token.Pos
}

func (w *refWalker) add(rule refRule, expr, pkg string, field []string, pos token.Pos) {
	if !pos.IsValid() || embedreg.IsKernel(pos) {
		pos = refPos(w.owner, field)
	}
	key := string(rule) + "\x00" + expr + "\x00" + pos.String()
	if w.seen[key] {
		return
	}
	w.seen[key] = true
	w.findings = append(w.findings, refFinding{
		rule:  rule,
		field: append([]string(nil), field...),
		expr:  expr,
		pkg:   pkg,
		pos:   pos,
	})
}

// walkValue classifies the references in v, a value within the owner, and
// descends into its expression and contents.
func (w *refWalker) walkValue(v cue.Value, depth int) {
	if depth > maxRefDepth {
		w.add(refUnresolved, fmt.Sprintf("a value nested more than %d deep", maxRefDepth), "", w.field, v.Pos())
		return
	}
	if r, ok := w.resolve(v); ok {
		w.reference(v, r, false)
		return
	}
	if d, ok := letValue(v); ok {
		// v uses a let (n: L, or a selection into one, n: L.x): the let's
		// value stands for v, positioned at v.
		saved := w.usePos
		if !saved.IsValid() {
			w.usePos = v.Pos()
		}
		w.walkValue(d, depth+1)
		w.usePos = saved
		return
	}
	op, args := v.Expr()
	switch op {
	case cue.NoOp:
		if len(args) == 1 && args[0] != v {
			w.walkValue(args[0], depth+1)
			return
		}
		if kernelPos(v) {
			// thema's own conjunct: the default of a lens's lacunas, say.
			return
		}
		w.walkContents(v, depth)
	case cue.AndOp:
		// Each conjunct on its own: a reference is classified, a literal
		// descended. thema's own conjuncts (schema & _, a lens & Lens) are
		// skipped; the merged value would mix them with the author's.
		for _, a := range args {
			if kernelPos(a) {
				continue
			}
			w.walkValue(a, depth+1)
		}
	case cue.IndexOp:
		if len(args) != selectionOperands {
			break
		}
		// A selection the evaluator could not resolve: the index depends on
		// the instance (lin[a], _table[v0_0.kind]). The base is classified
		// with the label unknown; the index is an expression of its own.
		w.walkValue(args[1], depth+1)
		if r, ok := w.resolve(args[0]); ok {
			w.reference(args[0], r, !args[1].IsConcrete())
			return
		}
		w.walkValue(args[0], depth+1)
		return
	}
	if op != cue.NoOp && op != cue.AndOp {
		for _, a := range args {
			w.walkValue(a, depth+1)
		}
	}
}

// walkContents descends into the fields of a struct and the elements of a list.
func (w *refWalker) walkContents(v cue.Value, depth int) {
	if iter, err := v.Fields(cue.Optional(true), cue.Hidden(true), cue.Definitions(true), cue.Patterns(true)); err == nil {
		for iter.Next() {
			w.field = append(w.field, selName(iter.Selector()))
			w.walkValue(iter.Value(), depth+1)
			w.field = w.field[:len(w.field)-1]
		}
	}
	if v.IncompleteKind() != cue.ListKind {
		return
	}
	if iter, err := v.List(); err == nil {
		for i := 0; iter.Next(); i++ {
			w.field = append(w.field, strconv.Itoa(i))
			w.walkValue(iter.Value(), depth+1)
			w.field = w.field[:len(w.field)-1]
		}
	}
	if elem := v.LookupPath(cue.MakePath(cue.AnyIndex)); elem.Exists() {
		w.field = append(w.field, "[...]")
		w.walkValue(elem, depth+1)
		w.field = w.field[:len(w.field)-1]
	}
}

// resolved is what a reference denotes: a path in a root. The root is the
// author's instance, a package, or unknown: a value reached by dereferencing
// a struct the evaluator holds (the base of an embedded reference), which is
// the author's, since a package would have a build instance of its own.
type resolved struct {
	root cue.Value
	// pkg is the import path of the package root is, if it is one.
	pkg string
	// author says root is the author's instance.
	author bool
	path   cue.Path
}

// resolve returns what v denotes, if v is a reference. ReferencePath answers
// for most: a field, an alias, a chain of selections and constant indexes,
// resolved by the evaluator to one absolute path. It is empty for a reference
// embedded in a struct (schema: {#Ref}), which Expr shows as a selection
// whose base is the enclosing struct; the chain is then walked, as the base is
// a reference itself or a value Dereference reaches.
func (w *refWalker) resolve(v cue.Value) (resolved, bool) {
	if root, p := v.ReferencePath(); isRefPath(p) {
		return w.rooted(root, p), true
	}
	op, args := v.Expr()
	switch op {
	case cue.SelectorOp:
		if len(args) != selectionOperands {
			return resolved{}, false
		}
		label, err := args[1].String()
		if err != nil {
			return resolved{}, false
		}
		base, ok := w.resolve(args[0])
		if !ok {
			return resolved{}, false
		}
		base.path = appendLabel(base.path, label)
		return base, true
	case cue.NoOp:
		if bi := v.BuildInstance(); bi != nil {
			// A package, the base of a selection of one of its members.
			return resolved{root: v, pkg: bi.ImportPath}, true
		}
		d := cue.Dereference(v)
		if d == v || d.Err() != nil || !d.Exists() || d.Path().Err() != nil {
			return resolved{}, false
		}
		if len(d.Path().Selectors()) == 0 {
			return w.rooted(d, cue.Path{}), true
		}
		return resolved{author: true, path: d.Path()}, true
	}
	return resolved{}, false
}

// rooted classifies root, the value a reference resolves in.
func (w *refWalker) rooted(root cue.Value, p cue.Path) resolved {
	r := resolved{root: root, path: p}
	if bi := root.BuildInstance(); bi != nil {
		r.pkg = bi.ImportPath
	}
	r.author = w.rc.isAuthorRoot(root)
	return r
}

// pathText renders a path as an author writes it: a label that is not an
// identifier is indexed (schemas["1"]["0"]), where cue.Path writes it as a
// quoted selector (schemas."1"."0").
func pathText(p cue.Path) string {
	var b strings.Builder
	for i, sel := range p.Selectors() {
		s := sel.String()
		switch {
		case sel.Type() == cue.IndexLabel || strings.HasPrefix(s, `"`):
			b.WriteString("[" + s + "]")
		case sel.Type()&cue.PatternConstraint != 0 || i == 0:
			b.WriteString(s)
		default:
			b.WriteString("." + s)
		}
	}
	return b.String()
}

// appendLabel extends p by the label a selection names, as the author would
// write it: a definition, a hidden field or a regular one.
func appendLabel(p cue.Path, label string) cue.Path {
	var sel cue.Selector
	switch {
	case strings.HasPrefix(label, "#"):
		sel = cue.Def(label)
	case strings.HasPrefix(label, "_"):
		sel = cue.Hid(label, "_")
	default:
		sel = cue.Str(label)
	}
	return cue.MakePath(append(p.Selectors(), sel)...)
}

// letValue returns the value of the let that v uses, if it uses one. The
// evaluator substitutes a let where it is used, so v's Expr is the let's value
// and its ReferencePath is empty, but Dereference still reaches the let's
// value: one that has no path of its own (Path() is in error: a let binding is
// not addressable) and that carries the let's expression. For a selection into
// a let (L.x), it is the field selected.
func letValue(v cue.Value) (cue.Value, bool) {
	d := cue.Dereference(v)
	if d == v || !d.Exists() || d.Path().Err() == nil {
		return cue.Value{}, false
	}
	return d, true
}

// reference classifies a reference. dynamic says the reference selects
// something further from what it resolves to by a label only an instance
// decides.
func (w *refWalker) reference(v cue.Value, r resolved, dynamic bool) {
	p := r.path
	expr := pathText(p)
	if dynamic {
		expr += "[...]"
	}
	pos := v.Pos()
	if w.usePos.IsValid() {
		pos = w.usePos
	}
	if r.pkg != "" && (compat.IsBuiltinPackage(r.pkg) || isThemaPackage(r.pkg)) {
		return
	}
	if !r.author {
		if r.pkg != "" {
			w.add(refImport, expr, r.pkg, w.field, pos)
			return
		}
		w.add(refOutsideLineage, expr, "", w.field, pos)
		return
	}
	if !cuetil.PathHasPrefix(p, w.rc.lin.Path()) {
		w.add(refOutsideLineage, expr, "", w.field, pos)
		return
	}
	rel := w.rc.relToLineage(p)
	if dynamic && cuetil.PathHasPrefix(w.owner.rel, rel) {
		// Selecting by an unknown label from the lineage, or from a struct
		// between it and the owner: the owner itself or a sibling, no telling.
		w.add(refUnresolved, expr, "", w.field, pos)
		return
	}
	if cuetil.PathHasPrefix(rel, w.owner.rel) {
		return
	}
	w.inLineage(rel.Selectors(), dynamic, expr, w.field, pos)
}

// inLineage classifies a reference into the lineage, outside the owner, by the
// labels it selects from the lineage. dynamic adds one label an instance
// decides.
func (w *refWalker) inLineage(sels []cue.Selector, dynamic bool, expr string, field []string, pos token.Pos) {
	n := len(sels)
	if dynamic {
		n++
	}
	head := ""
	if len(sels) > 0 {
		head = selName(sels[0])
	}
	switch head {
	case labelSchemas:
		// A bare lin.schemas is the lineage's collection of schemas
		// (len(lin.schemas)); anything selected from it is a schema.
		switch {
		case n == 1:
			w.add(refLineageMeta, expr, "", field, pos)
		case w.owner.lens:
			w.add(refLensToSchema, expr, "", field, pos)
		default:
			w.add(refSiblingSchema, expr, "", field, pos)
		}
	case labelLenses:
		if n > 1 && w.owner.lens {
			w.add(refLensToLens, expr, "", field, pos)
		} else {
			w.add(refLineageMeta, expr, "", field, pos)
		}
	default:
		w.add(refLineageMeta, expr, "", field, pos)
	}
}

// isRefPath reports whether p, from ReferencePath, names a value: a reference
// the evaluator could not resolve (an index that depends on the instance)
// reports a path in error.
func isRefPath(p cue.Path) bool {
	return len(p.Selectors()) > 0 && p.Err() == nil
}

// kernelPos reports whether v is positioned in thema's own CUE.
func kernelPos(v cue.Value) bool {
	pos := v.Pos()
	return pos.IsValid() && embedreg.IsKernel(pos)
}

// isThemaPackage reports whether an import path names thema's own CUE package,
// with or without its major version suffix.
func isThemaPackage(path string) bool {
	mod, _, _ := strings.Cut(util.ThemaModulePath, "@")
	path, _, _ = strings.Cut(path, "@")
	return path == mod
}

// selName is the name an identifier would have to carry to select sel.
func selName(sel cue.Selector) string {
	if sel.IsString() {
		return sel.Unquoted()
	}
	return sel.String()
}

// fieldByName returns the field of v with the given label as written (a
// regular, optional, required, hidden or definition field), or a value that
// does not exist.
func fieldByName(v cue.Value, name string) cue.Value {
	iter, err := v.Fields(cue.Optional(true), cue.Hidden(true), cue.Definitions(true))
	if err != nil {
		return cue.Value{}
	}
	for iter.Next() {
		if selName(iter.Selector()) == name {
			return iter.Value()
		}
	}
	return cue.Value{}
}

// refPos positions a finding at the author's line: the field holding the
// reference where it exists in the value, else the nearest enclosing field
// that does (a field built by a pending comprehension exists only once data
// arrives), else the owner.
func refPos(o refOwner, field []string) token.Pos {
	for i := len(field); i >= 0; i-- {
		v := o.v
		for _, name := range field[:i] {
			v = fieldByName(v, name)
		}
		if v.Exists() {
			return embedreg.AuthorPos(v)
		}
	}
	return embedreg.AuthorPos(o.v)
}
