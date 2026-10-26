package compile

import (
	"fmt"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/cueview"
)

// An entry is one mapping in a lens body, after the body's nesting has been
// flattened to full target paths.
type entry struct {
	kind entryKind
	// target is the target path the entry produces. For a drop it is the
	// path the dropped field would have had in the target, used only to
	// check that the target does not require it.
	target cue.Path
	// source is the source path a move, orelse, remap or each reads, or the
	// path a drop drops.
	source cue.Path
	// expr is the author's expression: the reference of a move, the literal
	// of a constant, the whole call of an operation. Nil for derived entries,
	// whose expression is built at emission.
	expr ast.Expr
	// pos is where the entry is positioned: the author's field, or for a
	// derived entry the construct it was derived from.
	pos token.Pos
	// lacuna is the entry's @lacuna attribute, if any.
	lacuna *lacunaAttr
	// comments are the author's comments on the field, carried to the result.
	comments []*ast.CommentGroup
	// derived entries were produced by derivation, not written.
	derived bool

	// fallback is orelse's second argument: a constant, or a reference.
	fallback ast.Expr
	// arms and catchAll are remap's table.
	arms     []arm
	catchAll *arm
	// body is each's element body.
	body *ast.StructLit
}

// An arm is one entry of a remap table.
type arm struct {
	key    string
	value  ast.Expr
	lacuna *lacunaAttr
	pos    token.Pos
}

type entryKind int

const (
	kindMove entryKind = iota
	kindConst
	kindDrop
	kindOrElse
	kindRemap
	kindEach
)

// The operations a lens body may call, and the arguments each takes.
const (
	opDrop   = "drop"
	opOrElse = "orelse"
	opRemap  = "remap"
	opEach   = "each"
	opArity  = 2
)

// The roots references start from: input in a lens body, item in an each body.
const (
	rootInput = "input"
	rootItem  = "item"
)

// A lacunaAttr is a parsed @lacuna(Type, "message") or @lacuna("message").
type lacunaAttr struct {
	typ, msg string
	pos      token.Pos
}

// A rebase says that target fields under target copy, when not mapped, from
// source fields under source: the effect of a nested body embedding a
// reference.
type rebase struct {
	target, source cue.Path
}

// A body is a lens body: its entries and rebases.
type body struct {
	entries []entry
	rebases []rebase
	// claimed holds every target path an authored field named, including
	// fields that were rejected, so that a rejected mapping does not also
	// read as a missing one.
	claimed map[string]bool
}

func (b *body) add(e entry) { b.entries = append(b.entries, e) }

func (b *body) claim(p cue.Path) {
	if b.claimed == nil {
		b.claimed = make(map[string]bool)
	}
	b.claimed[p.String()] = true
}

// dropped reports whether an authored drop drops p.
func (b *body) dropped(p cue.Path) bool {
	for _, en := range b.entries {
		if en.kind == kindDrop && en.source.String() == p.String() {
			return true
		}
	}
	return false
}

// sourceBase returns the source path that target fields under tbase copy
// from: the innermost rebase covering tbase, else the same path.
func (b *body) sourceBase(tbase cue.Path) cue.Path {
	best, bestLen := tbase, -1
	for _, r := range b.rebases {
		if n := len(r.target.Selectors()); n > bestLen && pathHasPrefix(tbase, r.target) {
			best = cue.MakePath(append(append([]cue.Selector{}, r.source.Selectors()...), tbase.Selectors()[n:]...)...)
			bestLen = n
		}
	}
	return best
}

// direction says which way a lens goes, which decides how an unmapped source
// field is treated.
type direction int

const (
	// forward: previous version to this one. A source field the target lacks
	// must be mapped or dropped, so that a forgotten rename is loud.
	forward direction = iota
	// backward: this version to the previous one. A source field the target
	// lacks is a field this version added; it is dropped by derivation.
	backward
)

// An edge compiles one lens between consecutive versions. An each body is
// compiled by a sub-edge over the element types, with item as its root.
type edge struct {
	ctx *cue.Context
	// S and T are the source and target schema values of the lens being
	// compiled; the author's input and result. For an each body, the element
	// values.
	S, T     cue.Value
	sv, tv   thema.SyntacticVersion
	dir      direction
	blockPos token.Pos
	// root is the identifier the author's references start from: the
	// version's parameter (v0_0), or item inside an each body. emitRoot is
	// what the normal form uses for it: input, or item.
	root, emitRoot string
	// each is set for the sub-edge compiling an each body.
	each *eachScope
	errs cerrors.Error
}

// An eachScope is what an each body's sub-edge knows about its list.
type eachScope struct {
	// list is the source list's path, from input.
	list cue.Path
	// target is the target list's path.
	target cue.Path
	// n numbers the body's generated lets within the lens.
	n int
}

func (e *edge) errf(p token.Pos, format string, args ...any) {
	e.errs = cerrors.Append(e.errs, cerrors.Newf(p, format, args...))
}

// parseBody reads an authored lens body into b. tbase is the target path the
// body's fields are under, sbase the source path they copy from.
func (e *edge) parseBody(lit *ast.StructLit, tbase, sbase cue.Path, b *body) {
	for _, d := range lit.Elts {
		switch d := d.(type) {
		case *ast.CommentGroup:
		case *ast.Field:
			e.parseField(d, tbase, sbase, b)
		case *ast.EmbedDecl:
			e.errf(d.Pos(), "a lens body has no embeddings; a nested body may embed %s.<path> to copy from it", e.root)
		case *ast.Comprehension:
			e.errf(d.Pos(), "comprehensions are not allowed in a lens body; a reference to an optional field is guarded for you, and orelse(ref, fallback) decides what to use when it is absent")
		case *ast.LetClause:
			e.errf(d.Pos(), "let is not allowed in a lens body")
		default:
			e.errf(d.Pos(), "a lens body holds only mappings")
		}
	}
}

func (e *edge) parseField(f *ast.Field, tbase, sbase cue.Path, b *body) {
	name, _, err := ast.LabelName(f.Label)
	if err != nil || f.Constraint != token.ILLEGAL {
		e.errf(f.Pos(), "a mapping's key is a plain field name")
		return
	}
	tpath := appendPath(tbase, cue.Str(name))
	spath := appendPath(sbase, cue.Str(name))
	b.claim(tpath)
	lac, ok := e.lacunaAttr(f.Attrs)
	if !ok {
		return
	}
	en := entry{target: tpath, expr: f.Value, pos: f.Pos(), lacuna: lac, comments: ast.Comments(f)}

	switch x := f.Value.(type) {
	case *ast.Ident:
		if x.Name == opDrop {
			en.kind, en.source = kindDrop, spath
			b.add(en)
			return
		}
	case *ast.StructLit:
		base := spath
		var elts []ast.Decl
		for _, d := range x.Elts {
			if emb, ok := d.(*ast.EmbedDecl); ok {
				p, ok := e.refPath(emb.Expr)
				if !ok {
					e.errf(emb.Pos(), "a nested body may embed only %s.<path>, to copy from it", e.root)
					return
				}
				base = p
				continue
			}
			elts = append(elts, d)
		}
		if lac != nil {
			e.errf(lac.pos, "a lacuna attaches to a mapping, not to a nested body")
		}
		b.rebases = append(b.rebases, rebase{target: tpath, source: base})
		e.parseBody(&ast.StructLit{Lbrace: x.Lbrace, Elts: elts, Rbrace: x.Rbrace}, tpath, base, b)
		return
	case *ast.CallExpr:
		if fn, ok := x.Fun.(*ast.Ident); ok {
			switch fn.Name {
			case opOrElse:
				e.parseOrElse(x, &en, b)
				return
			case opRemap:
				e.parseRemap(x, &en, b)
				return
			case opEach:
				e.parseEach(x, &en, b)
				return
			}
		}
	}
	if p, ok := e.refPath(f.Value); ok {
		en.kind, en.source = kindMove, p
		b.add(en)
		return
	}
	if isLiteral(f.Value) {
		en.kind = kindConst
		b.add(en)
		return
	}
	e.errf(f.Value.Pos(), "%s: a mapping is a reference to %s, a constant, a nested body, drop, orelse, remap or each; other expressions are not supported yet", name, e.root)
}

// parseOrElse reads orelse(ref, fallback): ref must refer to an optional
// source field, and fallback must be a constant or a reference the source
// always has.
func (e *edge) parseOrElse(x *ast.CallExpr, en *entry, b *body) {
	if len(x.Args) != opArity {
		e.errf(x.Pos(), "orelse takes two arguments: orelse(%s.<path>, fallback)", e.root)
		return
	}
	p, ok := e.refPath(x.Args[0])
	if !ok {
		e.errf(x.Args[0].Pos(), "orelse's first argument must be a reference to %s; computed expressions are not supported yet", e.root)
		return
	}
	fb := x.Args[1]
	if _, ok := e.refPath(fb); !ok && !isLiteral(fb) {
		e.errf(fb.Pos(), "orelse's fallback must be a constant or a reference to %s", e.root)
		return
	}
	en.kind, en.source, en.fallback = kindOrElse, p, fb
	b.add(*en)
}

// parseRemap reads remap(ref, {key: value, ..., [_]: value}).
func (e *edge) parseRemap(x *ast.CallExpr, en *entry, b *body) {
	if len(x.Args) != opArity {
		e.errf(x.Pos(), "remap takes two arguments: remap(%s.<path>, {...})", e.root)
		return
	}
	p, ok := e.refPath(x.Args[0])
	if !ok {
		e.errf(x.Args[0].Pos(), "remap's first argument must be a reference to %s", e.root)
		return
	}
	table, ok := x.Args[1].(*ast.StructLit)
	if !ok {
		e.errf(x.Args[1].Pos(), "remap's table must be a struct of value: value entries, with an optional [_] arm")
		return
	}
	en.kind, en.source = kindRemap, p
	for _, d := range table.Elts {
		f, ok := d.(*ast.Field)
		if !ok {
			if _, ok := d.(*ast.CommentGroup); ok {
				continue
			}
			e.errf(d.Pos(), "a remap table holds only value: value entries")
			return
		}
		if !isLiteral(f.Value) {
			e.errf(f.Value.Pos(), "a remap arm's value must be a constant")
			return
		}
		lac, lok := e.lacunaAttr(f.Attrs)
		if !lok {
			return
		}
		a := arm{value: f.Value, lacuna: lac, pos: f.Pos()}
		if isCatchAll(f.Label) {
			if en.catchAll != nil {
				e.errf(f.Pos(), "a remap table has at most one [_] arm")
				return
			}
			if lac == nil {
				e.errf(f.Pos(), "the [_] arm collapses every unmapped value into one: declare what is lost with @lacuna(Type, \"message\")")
				return
			}
			en.catchAll = &a
			continue
		}
		key, _, err := ast.LabelName(f.Label)
		if err != nil {
			e.errf(f.Label.Pos(), "a remap key is a string")
			return
		}
		a.key = key
		en.arms = append(en.arms, a)
	}
	b.add(*en)
}

// isCatchAll reports whether a label is [_].
func isCatchAll(l ast.Label) bool {
	list, ok := l.(*ast.ListLit)
	if !ok || len(list.Elts) != 1 {
		return false
	}
	id, ok := list.Elts[0].(*ast.Ident)
	return ok && id.Name == "_"
}

// parseEach reads each(ref, {body}).
func (e *edge) parseEach(x *ast.CallExpr, en *entry, b *body) {
	if len(x.Args) != opArity {
		e.errf(x.Pos(), "each takes two arguments: each(%s.<list>, {...})", e.root)
		return
	}
	p, ok := e.refPath(x.Args[0])
	if !ok {
		e.errf(x.Args[0].Pos(), "each's first argument must be a reference to a list in %s", e.root)
		return
	}
	bodyLit, ok := x.Args[1].(*ast.StructLit)
	if !ok {
		e.errf(x.Args[1].Pos(), "each's body must be a struct of mappings over item")
		return
	}
	if en.lacuna != nil {
		e.errf(en.lacuna.pos, "a lacuna attaches to a mapping inside the each body, not to each")
		return
	}
	en.kind, en.source, en.body = kindEach, p, bodyLit
	b.add(*en)
}

// lacunaAttr reads a field's @lacuna attribute. A second @lacuna, or any
// other attribute, is an error.
func (e *edge) lacunaAttr(attrs []*ast.Attribute) (*lacunaAttr, bool) {
	var out *lacunaAttr
	for _, a := range attrs {
		key, bodyText := a.Split()
		if key != "lacuna" {
			e.errf(a.Pos(), "@%s is not an attribute of a mapping; only @lacuna is", key)
			return nil, false
		}
		if out != nil {
			e.errf(a.Pos(), "a mapping has at most one @lacuna")
			return nil, false
		}
		typ, msg, err := parseLacunaBody(bodyText)
		if err != nil {
			e.errf(a.Pos(), "@lacuna: %v", err)
			return nil, false
		}
		out = &lacunaAttr{typ: typ, msg: msg, pos: a.Pos()}
	}
	return out, true
}

// parseLacunaBody parses `Type, "message"` or `"message"`.
func parseLacunaBody(s string) (typ, msg string, err error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, `"`) {
		typ, s, _ = strings.Cut(s, ",")
		typ = strings.TrimSpace(typ)
		s = strings.TrimSpace(s)
		if !isLacunaType(typ) {
			return "", "", fmt.Errorf("%q is not a lacuna type", typ)
		}
	}
	msg, err = literal.Unquote(s)
	if err != nil {
		return "", "", fmt.Errorf("the message must be a quoted string")
	}
	return typ, msg, nil
}

// lacunaDropped is the type of the lacuna a drop derives.
const lacunaDropped = "DroppedField"

func isLacunaType(s string) bool {
	switch s {
	case "Placeholder", lacunaDropped, "LossyFieldMapping", "ChangedDefault", "PrecisionLoss", "AmbiguousMapping", "Defaulted":
		return true
	}
	return false
}

// refPath returns the path of a reference <root>.a.b, if x is one.
func (e *edge) refPath(x ast.Expr) (cue.Path, bool) {
	root, p, ok := refPathAny(x)
	if !ok || root != e.root {
		return cue.Path{}, false
	}
	return p, true
}

// refPathAny returns the root and path of a reference root.a.b.
func refPathAny(x ast.Expr) (string, cue.Path, bool) {
	var sels []cue.Selector
	for {
		switch s := x.(type) {
		case *ast.SelectorExpr:
			name, _, err := ast.LabelName(s.Sel)
			if err != nil {
				return "", cue.Path{}, false
			}
			sels = append([]cue.Selector{cue.Str(name)}, sels...)
			x = s.X
		case *ast.Ident:
			return s.Name, cue.MakePath(sels...), true
		default:
			return "", cue.Path{}, false
		}
	}
}

// isLiteral reports whether x is a constant: a basic literal, a signed
// number, or a list of constants.
func isLiteral(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.BasicLit:
		return true
	case *ast.UnaryExpr:
		_, ok := x.X.(*ast.BasicLit)
		return ok && (x.Op == token.SUB || x.Op == token.ADD)
	case *ast.ListLit:
		for _, el := range x.Elts {
			if !isLiteral(el) {
				return false
			}
		}
		return true
	}
	return false
}

// A node is one field of the result being built, with the fields beneath it.
type node struct {
	order  []string
	fields map[string]*node
	// For a leaf: the entry producing it.
	leaf *entry
	// guard, if non-empty, is an optional source path the leaf must be
	// guarded on: if <root>.<guard> != _|_ {...}.
	guard cue.Path
	pos   token.Pos
}

func (n *node) child(name string) *node {
	if n.fields == nil {
		n.fields = make(map[string]*node)
	}
	c, ok := n.fields[name]
	if !ok {
		c = &node{}
		n.fields[name] = c
		n.order = append(n.order, name)
	}
	return c
}

func (n *node) at(p cue.Path) *node {
	for _, s := range p.Selectors() {
		n = n.child(s.Unquoted())
	}
	return n
}

func (n *node) has(p cue.Path) *node {
	for _, s := range p.Selectors() {
		c, ok := n.fields[s.Unquoted()]
		if !ok {
			return nil
		}
		n = c
	}
	return n
}

// A compiledLens is the outcome of assembling one lens.
type compiledLens struct {
	// result is the lens's result, lacunas its lacunas, and lets the
	// declarations each bodies need beside them.
	result  *ast.StructLit
	lacunas []ast.Expr
	lets    []ast.Decl
	// effective is every move the lens makes, written or implicit, and every
	// drop. Derivation inverts these.
	effective []entry
	// lets counts the lets generated so far, for naming.
	nlets int
}

// assemble builds the lens for e from b: explicit entries, implicit copies at
// the shallowest path the two schemas share, and the direction's rule for
// unmapped source fields.
func (e *edge) assemble(b *body) *compiledLens {
	out := &compiledLens{}
	e.assembleInto(b, out)
	return out
}

func (e *edge) assembleInto(b *body, out *compiledLens) {
	root := &node{}
	consumed := make(map[string]bool)
	dropped := make(map[string]bool)

	for i := range b.entries {
		en := &b.entries[i]
		switch en.kind {
		case kindDrop:
			if !lookup(e.S, en.source).Exists() {
				e.errf(en.pos, "%s does not declare %s, so there is nothing to drop", e.sv, en.source)
				continue
			}
			if t := lookup(e.T, en.target); t.Exists() && e.required(e.T, en.target) {
				e.errf(en.pos, "%s requires %s; dropping it would leave it absent", e.tv, en.target)
				continue
			}
			dropped[en.source.String()] = true
			consumed[en.source.String()] = true
			out.effective = append(out.effective, *en)
			e.dropLacuna(out, en)
		case kindMove:
			if !lookup(e.S, en.source).Exists() {
				e.errf(en.expr.Pos(), "%s does not declare %s", e.sv, en.source)
				continue
			}
			if !e.place(root, en) {
				continue
			}
			if e.optional(e.S, en.source) {
				if e.required(e.T, en.target) {
					e.errf(en.pos, "%s is optional in %s and %s requires %s: write orelse(%s.%s, <fallback>) to say what to use when it is absent", en.source, e.sv, e.tv, en.target, e.root, en.source)
					continue
				}
				root.has(en.target).guard = en.source
			}
			consumed[en.source.String()] = true
			out.effective = append(out.effective, *en)
		case kindConst:
			if !e.place(root, en) {
				continue
			}
			if !e.instance(en.expr, en.target) {
				continue
			}
		case kindOrElse:
			consumed[en.source.String()] = true
			if p, ok := e.refPath(en.fallback); ok {
				consumed[p.String()] = true
			}
			if !e.checkOrElse(en) || !e.place(root, en) {
				continue
			}
			out.effective = append(out.effective, *en)
		case kindRemap:
			consumed[en.source.String()] = true
			if !e.checkRemap(en) || !e.place(root, en) {
				continue
			}
			out.effective = append(out.effective, *en)
			e.remapLacunas(out, en)
			continue
		case kindEach:
			consumed[en.source.String()] = true
			if !e.place(root, en) || !e.compileEach(en, out) {
				continue
			}
			out.effective = append(out.effective, *en)
			continue
		}
		if en.kind != kindDrop && en.lacuna != nil {
			out.lacunas = append(out.lacunas, e.declaredLacuna(en, e.entryCondition(en)))
		}
	}

	e.fill(root, cue.MakePath(), b, consumed, dropped, out)
	e.unmapped(b, consumed, out)

	out.result = e.emit(root, cue.MakePath())
}

// instance checks that a constant is an instance of the target field.
func (e *edge) instance(x ast.Expr, tpath cue.Path) bool {
	lit := e.ctx.BuildExpr(x)
	if err := lookup(e.T, tpath).Unify(lit).Validate(); err != nil {
		e.errf(x.Pos(), "%s is not an instance of %s's %s: %s", exprText(x), e.tv, tpath, oneLine(cerrors.Details(err, nil)))
		return false
	}
	return true
}

// checkOrElse checks an orelse entry: its reference is to an optional source
// field, and its fallback is total for the target.
func (e *edge) checkOrElse(en *entry) bool {
	if !lookup(e.S, en.source).Exists() {
		e.errf(en.expr.Pos(), "%s does not declare %s", e.sv, en.source)
		return false
	}
	if !e.optional(e.S, en.source) {
		e.errf(en.expr.Pos(), "%s always has %s, so orelse's fallback could never apply: write %s: %s.%s", e.sv, en.source, en.target, e.root, en.source)
		return false
	}
	if p, ok := e.refPath(en.fallback); ok {
		if !lookup(e.S, p).Exists() {
			e.errf(en.fallback.Pos(), "%s does not declare %s", e.sv, p)
			return false
		}
		if e.optional(e.S, p) {
			e.errf(en.fallback.Pos(), "%s is optional in %s, so it cannot be orelse's fallback: the fallback must always exist", p, e.sv)
			return false
		}
		return true
	}
	return e.instance(en.fallback, en.target)
}

// checkRemap checks a remap entry: every arm value is an instance of the
// target, and every value the source may hold reaches an arm, the catch-all,
// or passes through to a target that accepts it.
func (e *edge) checkRemap(en *entry) bool {
	if !lookup(e.S, en.source).Exists() {
		e.errf(en.expr.Pos(), "%s does not declare %s", e.sv, en.source)
		return false
	}
	seen := make(map[string]bool)
	for _, a := range en.arms {
		if seen[a.key] {
			e.errf(a.pos, "remap maps %q twice", a.key)
			return false
		}
		seen[a.key] = true
		if !e.instance(a.value, en.target) {
			return false
		}
	}
	if en.catchAll != nil && !e.instance(en.catchAll.value, en.target) {
		return false
	}
	if en.catchAll != nil {
		return true
	}
	// Without a catch-all, every source value must be keyed or pass through.
	sv, tv := lookup(e.S, en.source), lookup(e.T, en.target)
	alts, finite := literalAlternatives(sv)
	if !finite {
		if err := tv.Subsume(sv, cue.Raw()); err != nil {
			e.errf(en.expr.Pos(), "remap of %s is not total: %s's %s may hold values that no arm maps and %s's %s does not accept; add a [_] arm", en.source, e.sv, en.source, e.tv, en.target)
			return false
		}
		return true
	}
	for _, a := range alts {
		if s, err := a.String(); err == nil && seen[s] {
			continue
		}
		if tv.Unify(a).Validate() != nil {
			e.errf(en.expr.Pos(), "remap of %s is not total: %s's %s may be %s, which no arm maps and %s's %s does not accept; add an arm for it, or a [_] arm", en.source, e.sv, en.source, fmtv(a), e.tv, en.target)
			return false
		}
	}
	return true
}

// literalAlternatives returns the alternatives of v if it is a finite
// disjunction of concrete values (or one concrete value).
func literalAlternatives(v cue.Value) ([]cue.Value, bool) {
	if d, ok := v.Default(); ok {
		_ = d
	}
	op, args := v.Expr()
	if op != cue.OrOp {
		if v.Validate(cue.Concrete(true)) == nil && v.IncompleteKind()&(cue.StructKind|cue.ListKind) == 0 {
			if _, hasDefault := v.Default(); !hasDefault {
				return []cue.Value{v}, true
			}
		}
		return nil, false
	}
	var out []cue.Value
	for _, a := range args {
		sub, ok := literalAlternatives(a)
		if !ok {
			return nil, false
		}
		out = append(out, sub...)
	}
	return out, true
}

// passthrough reports whether a remap needs a passthrough branch: some
// source value that no arm keys may be accepted by the target.
func (e *edge) passthrough(en *entry) bool {
	sv, tv := lookup(e.S, en.source), lookup(e.T, en.target)
	keyed := make(map[string]bool)
	for _, a := range en.arms {
		keyed[a.key] = true
	}
	alts, finite := literalAlternatives(sv)
	if !finite {
		return true
	}
	for _, a := range alts {
		if s, err := a.String(); err == nil && keyed[s] {
			continue
		}
		if tv.Unify(a).Validate() == nil {
			return true
		}
	}
	return false
}

// targetType returns the target field's constraint as syntax, for a remap's
// passthrough test. Only a self-contained type expression can be restated:
// one with references into the schema cannot (#62, Q7).
func (e *edge) targetType(en *entry) (ast.Expr, bool) {
	n, err := cueview.Evaluated(lookup(e.T, en.target))
	if err != nil {
		return nil, false
	}
	x, ok := n.(ast.Expr)
	if !ok {
		return nil, false
	}
	selfContained := true
	ast.Walk(x, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			if !isTypeIdent(n.Name) {
				selfContained = false
			}
		case *ast.SelectorExpr, *ast.CallExpr, *ast.StructLit, *ast.Comprehension:
			selfContained = false
		}
		return selfContained
	}, nil)
	return x, selfContained
}

func isTypeIdent(name string) bool {
	switch name {
	case "_", "bool", "string", "bytes", "number", "int", "float", "null", "true", "false",
		"int8", "int16", "int32", "int64", "int128", "uint", "uint8", "uint16", "uint32", "uint64", "uint128",
		"float32", "float64", "rune", "byte":
		return true
	}
	return false
}

// compileEach compiles an each body as a sub-edge over the element types.
func (e *edge) compileEach(en *entry, out *compiledLens) bool {
	sl, tl := lookup(e.S, en.source), lookup(e.T, en.target)
	if !sl.Exists() {
		e.errf(en.expr.Pos(), "%s does not declare %s", e.sv, en.source)
		return false
	}
	if sl.IncompleteKind() != cue.ListKind || tl.IncompleteKind() != cue.ListKind {
		e.errf(en.expr.Pos(), "each maps a list to a list; %s's %s and %s's %s must both be lists", e.sv, en.source, e.tv, en.target)
		return false
	}
	selem, telem := sl.LookupPath(cue.MakePath(cue.AnyIndex)), tl.LookupPath(cue.MakePath(cue.AnyIndex))
	if !selem.Exists() || !telem.Exists() {
		e.errf(en.expr.Pos(), "each maps the elements of open lists ([...T]); a fixed-length list is not supported")
		return false
	}
	if e.each != nil {
		e.errf(en.expr.Pos(), "each inside each is not supported yet")
		return false
	}
	sub := &edge{ctx: e.ctx, S: selem, T: telem, sv: e.sv, tv: e.tv, dir: e.dir, blockPos: en.pos, root: rootItem, emitRoot: rootItem,
		each: &eachScope{list: en.source, target: en.target, n: out.nlets}}
	b := &body{}
	sub.parseBody(en.body, cue.MakePath(), cue.MakePath(), b)
	subOut := &compiledLens{nlets: out.nlets}
	sub.assembleInto(b, subOut)
	e.errs = cerrors.Append(e.errs, sub.errs)
	if sub.errs != nil {
		return false
	}
	out.lacunas = append(out.lacunas, subOut.lacunas...)
	out.lets = append(out.lets, subOut.lets...)
	out.nlets = subOut.nlets
	en.body = subOut.result
	return true
}

// place puts an explicit entry into the result tree, checking the target
// declares its path and nothing else produces it.
func (e *edge) place(root *node, en *entry) bool {
	if !lookup(e.T, en.target).Exists() {
		e.errf(en.pos, "%s does not declare %s", e.tv, en.target)
		return false
	}
	n := root.has(en.target)
	if n != nil && (n.leaf != nil || len(n.fields) > 0) {
		e.errf(en.pos, "%s is mapped twice", en.target)
		return false
	}
	for p := en.target; len(p.Selectors()) > 1; {
		p = cue.MakePath(p.Selectors()[:len(p.Selectors())-1]...)
		if a := root.has(p); a != nil && a.leaf != nil {
			e.errf(en.pos, "%s is mapped twice: %s is already mapped whole", en.target, p)
			return false
		}
	}
	n = root.at(en.target)
	n.leaf = en
	n.pos = en.pos
	return true
}

// fill adds implicit copies under tbase: for each field the target declares
// there that nothing produces, a copy from the source if it declares the
// field too, descending where the source's struct has fields the target's
// lacks. A required target field nothing produces is an error.
func (e *edge) fill(n *node, tbase cue.Path, b *body, consumed, dropped map[string]bool, out *compiledLens) {
	sbase := b.sourceBase(tbase)
	for _, name := range e.fields(e.T, tbase) {
		tpath := appendPath(tbase, cue.Str(name))
		if c, ok := n.fields[name]; ok {
			if c.leaf == nil {
				e.fill(c, tpath, b, consumed, dropped, out)
			}
			continue
		}
		spath := appendPath(sbase, cue.Str(name))
		if sv := lookup(e.S, spath); sv.Exists() && !dropped[spath.String()] {
			tv := lookup(e.T, tpath)
			if isStruct(sv) && isStruct(tv) && !e.covers(sv, tv) {
				c := n.child(name)
				c.pos = e.blockPos
				e.fill(c, tpath, b, consumed, dropped, out)
				continue
			}
			en := entry{kind: kindMove, target: tpath, source: spath, pos: e.blockPos, derived: true}
			if e.optional(e.S, spath) && e.required(e.T, tpath) {
				e.errf(lookup(e.T, tpath).Pos(), "%s is optional in %s and required in %s: write %s: orelse(%s.%s, <fallback>) to say what to use when it is absent", spath, e.sv, e.tv, tpath, e.root, spath)
				continue
			}
			c := n.child(name)
			c.leaf = &en
			c.pos = en.pos
			if e.optional(e.S, spath) {
				c.guard = spath
			}
			consumed[spath.String()] = true
			out.effective = append(out.effective, en)
			continue
		}
		if e.required(e.T, tpath) && !b.claimed[tpath.String()] {
			e.errf(lookup(e.T, tpath).Pos(), "%s requires %s, and nothing produces it: map it in %s", e.tv, tpath, e.dir)
		}
	}
}

// unmapped applies the direction's rule to every source field the target does
// not declare and nothing consumed.
func (e *edge) unmapped(b *body, consumed map[string]bool, out *compiledLens) {
	for _, spath := range e.leaves(e.S, cue.MakePath()) {
		if consumedUnder(consumed, spath) || lookup(e.T, spath).Exists() && !e.rebased(b, spath) {
			continue
		}
		switch e.dir {
		case forward:
			e.errf(e.blockPos, "%s declares %s and %s does not, and nothing maps it: write <field>: %s.%s to move it, or %s: drop", e.sv, spath, e.tv, e.root, spath, spath)
		case backward:
			en := entry{kind: kindDrop, source: spath, target: spath, pos: lookup(e.S, spath).Pos(), derived: true}
			out.effective = append(out.effective, en)
			e.dropLacuna(out, &en)
		}
	}
}

// rebased reports whether spath lies under a rebase's source, in which case
// its counterpart in the target is elsewhere.
func (e *edge) rebased(b *body, spath cue.Path) bool {
	for _, r := range b.rebases {
		if pathHasPrefix(spath, r.source) {
			return true
		}
	}
	return false
}

func consumedUnder(consumed map[string]bool, p cue.Path) bool {
	sels := p.Selectors()
	for i := len(sels); i > 0; i-- {
		if consumed[cue.MakePath(sels[:i]...).String()] {
			return true
		}
	}
	return false
}

// emit builds the result struct from the tree, in the target's field order.
func (e *edge) emit(n *node, tbase cue.Path) *ast.StructLit {
	s := &ast.StructLit{}
	for _, name := range e.order(n, tbase) {
		c := n.fields[name]
		if c.leaf == nil {
			s.Elts = append(s.Elts, at(c.pos, &ast.Field{Label: at(c.pos, ast.NewIdent(name)), Value: e.emit(c, appendPath(tbase, cue.Str(name)))}))
			continue
		}
		s.Elts = append(s.Elts, e.emitLeaf(name, c)...)
	}
	return s
}

// emitLeaf renders one produced field: a plain field, a guarded field, or
// the branches of an orelse or remap.
func (e *edge) emitLeaf(name string, c *node) []ast.Decl {
	en, p := c.leaf, c.pos
	field := func(val ast.Expr) *ast.Field {
		f := at(p, &ast.Field{Label: at(p, ast.NewIdent(name)), Value: val})
		for _, cg := range en.comments {
			ast.AddComment(f, cg)
		}
		return f
	}
	branch := func(cond ast.Expr, val ast.Expr) ast.Decl {
		return at(p, &ast.Comprehension{
			Clauses: []ast.Clause{at(p, &ast.IfClause{Condition: cond})},
			Value:   at(p, &ast.StructLit{Elts: []ast.Decl{field(val)}}),
		})
	}
	switch en.kind {
	case kindOrElse:
		fallback := en.fallback
		if fp, ok := e.refPath(en.fallback); ok {
			fallback = at(en.fallback.Pos(), e.ref(fp))
		}
		return []ast.Decl{
			branch(e.present(p, en.source), at(p, e.ref(en.source))),
			branch(e.absent(p, en.source), fallback),
		}
	case kindRemap:
		var out []ast.Decl
		var keys []ast.Expr
		for _, a := range en.arms {
			keys = append(keys, at(p, ast.NewString(a.key)))
			out = append(out, branch(e.matches(p, en.source, at(p, ast.NewString(a.key))), a.value))
		}
		var unkeyed ast.Expr
		if len(keys) > 0 {
			unkeyed = e.notMatches(p, en.source, at(p, &ast.ParenExpr{X: ast.NewBinExpr(token.OR, keys...)}))
		}
		pass := e.passthrough(en)
		if pass || en.catchAll != nil {
			ttype, ok := e.targetType(en)
			if !ok {
				e.errf(en.expr.Pos(), "remap of %s: %s's %s is not a self-contained type, so values cannot be tested against it for the passthrough; map every value with an arm", en.source, e.tv, en.target)
				return nil
			}
			accepts := e.matches(p, en.source, at(p, &ast.ParenExpr{X: ttype}))
			if pass {
				out = append(out, branch(and(unkeyed, accepts), at(p, e.ref(en.source))))
			}
			if en.catchAll != nil {
				rejects := e.notMatches(p, en.source, at(p, &ast.ParenExpr{X: ttype}))
				out = append(out, branch(and(unkeyed, rejects), en.catchAll.value))
			}
		}
		return out
	case kindEach:
		list := at(p, &ast.ListLit{Elts: []ast.Expr{at(p, &ast.Comprehension{
			Clauses: []ast.Clause{at(p, &ast.ForClause{Value: at(p, ast.NewIdent(rootItem)), Source: at(p, e.ref(en.source))})},
			Value:   en.body,
		})}})
		return []ast.Decl{field(list)}
	}
	var val ast.Expr
	switch {
	case en.kind == kindMove && en.expr != nil:
		// The author's reference, rebuilt from the normal form's root at the
		// author's position.
		val = at(en.expr.Pos(), e.ref(en.source))
	case en.expr != nil:
		val = en.expr
	default:
		val = at(p, e.ref(en.source))
	}
	if len(c.guard.Selectors()) > 0 {
		return []ast.Decl{branch(e.present(p, c.guard), val)}
	}
	return []ast.Decl{field(val)}
}

// Condition builders. Each is positioned at p.

func (e *edge) present(p token.Pos, path cue.Path) ast.Expr {
	return at(p, ast.NewBinExpr(token.NEQ, at(p, e.ref(path)), at(p, &ast.BottomLit{})))
}

func (e *edge) absent(p token.Pos, path cue.Path) ast.Expr {
	return at(p, ast.NewBinExpr(token.EQL, at(p, e.ref(path)), at(p, &ast.BottomLit{})))
}

// matches is (<root>.<path> & x) != _|_.
func (e *edge) matches(p token.Pos, path cue.Path, x ast.Expr) ast.Expr {
	return at(p, ast.NewBinExpr(token.NEQ, at(p, &ast.ParenExpr{X: ast.NewBinExpr(token.AND, at(p, e.ref(path)), x)}), at(p, &ast.BottomLit{})))
}

// notMatches is (<root>.<path> & x) == _|_.
func (e *edge) notMatches(p token.Pos, path cue.Path, x ast.Expr) ast.Expr {
	return at(p, ast.NewBinExpr(token.EQL, at(p, &ast.ParenExpr{X: ast.NewBinExpr(token.AND, at(p, e.ref(path)), x)}), at(p, &ast.BottomLit{})))
}

func and(a, b ast.Expr) ast.Expr {
	if a == nil {
		return b
	}
	return ast.NewBinExpr(token.LAND, a, b)
}

// entryCondition is the condition under which an entry's declared lacuna
// applies: the fallback branch for orelse, nil (always) otherwise.
func (e *edge) entryCondition(en *entry) ast.Expr {
	if en.kind == kindOrElse {
		return e.absent(en.lacuna.pos, en.source)
	}
	return nil
}

// order returns n's fields in the target schema's order, then any the target
// does not declare in insertion order (place has already rejected those).
func (e *edge) order(n *node, tbase cue.Path) []string {
	var out []string
	seen := make(map[string]bool)
	for _, name := range e.fields(e.T, tbase) {
		if _, ok := n.fields[name]; ok {
			out = append(out, name)
			seen[name] = true
		}
	}
	for _, name := range n.order {
		if !seen[name] {
			out = append(out, name)
		}
	}
	return out
}

// dropLacuna adds the derived DroppedField lacuna for a drop, its message
// refined by the drop's @lacuna if that has no type or the same type. A
// @lacuna of another type adds a declared lacuna as well. Inside an each
// body, the lacuna is aggregated over the list's elements.
func (e *edge) dropLacuna(out *compiledLens, en *entry) {
	msg := e.dropMessage(en.source)
	if en.lacuna != nil && (en.lacuna.typ == "" || en.lacuna.typ == lacunaDropped) {
		msg = en.lacuna.msg
	} else if en.lacuna != nil {
		out.lacunas = append(out.lacunas, e.declaredLacuna(en, nil))
	}
	p := en.pos
	elts := []ast.Decl{at(p, &ast.Attribute{Text: "@thema(derived)"})}
	if e.each != nil {
		// let _d_<n> = [for i, item in input.<list> if item.<p> != _|_ {path: "<list>[\(i)].<p>", value: item.<p>}]
		name := fmt.Sprintf("_d_%d", out.nlets)
		out.nlets++
		clauses := []ast.Clause{at(p, &ast.ForClause{Key: at(p, ast.NewIdent("i")), Value: at(p, ast.NewIdent(rootItem)), Source: at(p, ref(rootInput, e.each.list))})}
		if e.optional(e.S, en.source) {
			clauses = append(clauses, at(p, &ast.IfClause{Condition: e.present(p, en.source)}))
		}
		pathLit := at(p, &ast.Interpolation{Elts: []ast.Expr{
			at(p, ast.NewLit(token.STRING, strconv.Quote(e.each.list.String() + "[")[:len(strconv.Quote(e.each.list.String()+"["))-1]+`\(`)),
			at(p, ast.NewIdent("i")),
			at(p, ast.NewLit(token.STRING, `)].`+en.source.String()+`"`)),
		}})
		comp := at(p, &ast.Comprehension{Clauses: clauses, Value: at(p, &ast.StructLit{Elts: []ast.Decl{
			at(p, &ast.Field{Label: at(p, ast.NewIdent("path")), Value: pathLit}),
			at(p, &ast.Field{Label: at(p, ast.NewIdent("value")), Value: at(p, e.ref(en.source))}),
		}})})
		out.lets = append(out.lets, at(p, &ast.LetClause{Ident: at(p, ast.NewIdent(name)), Expr: at(p, &ast.ListLit{Elts: []ast.Expr{comp}})}))
		elts = append(elts,
			at(p, &ast.Field{Label: at(p, ast.NewIdent("condition")), Value: at(p, ast.NewBinExpr(token.GTR, at(p, &ast.CallExpr{Fun: at(p, ast.NewIdent("len")), Args: []ast.Expr{at(p, ast.NewIdent(name))}}), at(p, ast.NewLit(token.INT, "0"))))}),
			at(p, &ast.Field{Label: at(p, ast.NewIdent("sourceFields")), Value: at(p, ast.NewIdent(name))}),
		)
	} else {
		if e.optional(e.S, en.source) {
			elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("condition")), Value: e.present(p, en.source)}))
		}
		elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("sourceFields")), Value: at(p, ast.NewList(e.fieldRef(p, en.source, e.emitRoot)))}))
	}
	elts = append(elts,
		at(p, &ast.Field{Label: at(p, ast.NewIdent("message")), Value: at(p, ast.NewString(msg))}),
		at(p, &ast.Field{Label: at(p, ast.NewIdent("type")), Value: at(p, ast.NewString(lacunaDropped))}),
	)
	out.lacunas = append(out.lacunas, at(p, &ast.StructLit{Elts: elts}))
}

// dropMessage is the derived lacuna's default message for dropping p.
func (e *edge) dropMessage(p cue.Path) string {
	if e.each != nil {
		return fmt.Sprintf("%s[].%s has no equivalent in %s and was dropped", e.each.list, p, e.tv)
	}
	return fmt.Sprintf("%s has no equivalent in %s and was dropped", p, e.tv)
}

// remapLacunas adds the lacuna declared on each arm, conditioned on the arm.
func (e *edge) remapLacunas(out *compiledLens, en *entry) {
	for _, a := range en.arms {
		if a.lacuna != nil {
			out.lacunas = append(out.lacunas, e.lacunaFor(en, a.lacuna, e.matches(a.lacuna.pos, en.source, at(a.lacuna.pos, ast.NewString(a.key)))))
		}
	}
	if en.catchAll != nil {
		p := en.catchAll.lacuna.pos
		ttype, ok := e.targetType(en)
		if !ok {
			return // emitLeaf reports it
		}
		var keys []ast.Expr
		for _, a := range en.arms {
			keys = append(keys, at(p, ast.NewString(a.key)))
		}
		var unkeyed ast.Expr
		if len(keys) > 0 {
			unkeyed = e.notMatches(p, en.source, at(p, &ast.ParenExpr{X: ast.NewBinExpr(token.OR, keys...)}))
		}
		out.lacunas = append(out.lacunas, e.lacunaFor(en, en.catchAll.lacuna, and(unkeyed, e.notMatches(p, en.source, at(p, &ast.ParenExpr{X: ttype})))))
	}
	if en.lacuna != nil {
		e.errf(en.lacuna.pos, "a lacuna on a remap attaches to one of its arms, not to the mapping")
	}
}

// declaredLacuna builds the lacuna an entry's @lacuna declares, applying
// under cond (nil for always).
func (e *edge) declaredLacuna(en *entry, cond ast.Expr) ast.Expr {
	return e.lacunaFor(en, en.lacuna, cond)
}

func (e *edge) lacunaFor(en *entry, lac *lacunaAttr, cond ast.Expr) ast.Expr {
	p := lac.pos
	if lac.typ == "" {
		e.errf(p, "a lacuna on a mapping needs a type: @lacuna(Type, %q)", lac.msg)
	}
	var elts []ast.Decl
	if cond != nil {
		elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("condition")), Value: cond}))
	}
	if en.kind == kindDrop {
		elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("sourceFields")), Value: at(p, ast.NewList(e.fieldRef(p, en.source, e.emitRoot)))}))
	} else {
		if len(en.source.Selectors()) > 0 {
			src := e.fieldRef(p, en.source, e.emitRoot)
			if e.optional(e.S, en.source) {
				src = at(p, &ast.Comprehension{Clauses: []ast.Clause{at(p, &ast.IfClause{Condition: e.present(p, en.source)})}, Value: src})
			}
			elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("sourceFields")), Value: at(p, ast.NewList(src))}))
		}
		elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("targetFields")), Value: at(p, ast.NewList(e.fieldRef(p, en.target, "result")))}))
	}
	elts = append(elts,
		at(p, &ast.Field{Label: at(p, ast.NewIdent("message")), Value: at(p, ast.NewString(lac.msg))}),
		at(p, &ast.Field{Label: at(p, ast.NewIdent("type")), Value: at(p, ast.NewString(lac.typ))}),
	)
	return at(p, &ast.StructLit{Elts: elts})
}

// fieldRef builds {path: "<p>", value: <root>.<p>}.
func (e *edge) fieldRef(pos token.Pos, p cue.Path, root string) ast.Expr {
	return at(pos, &ast.StructLit{Elts: []ast.Decl{
		at(pos, &ast.Field{Label: at(pos, ast.NewIdent("path")), Value: at(pos, ast.NewString(p.String()))}),
		at(pos, &ast.Field{Label: at(pos, ast.NewIdent("value")), Value: at(pos, ref(root, p))}),
	}})
}

// ref builds <root>.<p>, with the edge's root.
func (e *edge) ref(p cue.Path) ast.Expr { return ref(e.emitRoot, p) }

func ref(root string, p cue.Path) ast.Expr {
	var names []string
	for _, s := range p.Selectors() {
		names = append(names, s.Unquoted())
	}
	return ast.NewSel(ast.NewIdent(root), names...)
}

// derive builds the backward body implied by a forward lens: the inverse of
// every single-use move. Authored backward entries override derived ones at
// the same target path, or beneath it; an authored drop of a move's target
// suppresses its inverse.
func (e *edge) derive(fwd *compiledLens, authored *body) *body {
	uses := make(map[string]int)
	for _, en := range fwd.effective {
		if en.kind == kindMove {
			uses[en.source.String()]++
		}
	}
	b := &body{}
	for _, en := range fwd.effective {
		if en.kind != kindMove || uses[en.source.String()] != 1 || en.source.String() == en.target.String() {
			continue
		}
		if authored.covers(en.source) || authored.dropped(en.target) {
			continue
		}
		b.add(entry{kind: kindMove, target: en.source, source: en.target, pos: en.pos, derived: true})
	}
	b.entries = append(b.entries, authored.entries...)
	b.rebases = authored.rebases
	b.claimed = authored.claimed
	return b
}

// covers reports whether an authored entry or nested body produces p or a
// path above it.
func (b *body) covers(p cue.Path) bool {
	for _, en := range b.entries {
		if en.kind != kindDrop && pathHasPrefix(p, en.target) {
			return true
		}
	}
	for _, r := range b.rebases {
		if pathHasPrefix(p, r.target) {
			return true
		}
	}
	return false
}

// Schema queries. These ask the cue.Value, never a model of it.

// fields returns the names of the regular fields v declares at p, in order.
func (e *edge) fields(v cue.Value, p cue.Path) []string {
	iter, err := lookup(v, p).Fields(cue.Optional(true))
	if err != nil {
		return nil
	}
	var out []string
	for iter.Next() {
		if sel := iter.Selector(); sel.IsString() {
			out = append(out, sel.Unquoted())
		}
	}
	return out
}

// leaves returns every non-struct path v declares under p, and every empty
// struct, depth first.
func (e *edge) leaves(v cue.Value, p cue.Path) []cue.Path {
	var out []cue.Path
	for _, name := range e.fields(v, p) {
		fp := appendPath(p, cue.Str(name))
		if isStruct(lookup(v, fp)) {
			if sub := e.leaves(v, fp); len(sub) > 0 {
				out = append(out, sub...)
				continue
			}
		}
		out = append(out, fp)
	}
	return out
}

// covers reports whether the target declares every path the source declares
// under it, so that the source's struct can be copied whole.
func (e *edge) covers(sv, tv cue.Value) bool {
	for _, name := range e.fields(sv, cue.MakePath()) {
		t := lookup(tv, cue.MakePath(cue.Str(name)))
		if !t.Exists() {
			return false
		}
		s := lookup(sv, cue.MakePath(cue.Str(name)))
		if isStruct(s) && isStruct(t) && !e.covers(s, t) {
			return false
		}
	}
	return true
}

// optional reports whether any field along p in v is optional, so that an
// instance may lack the value at p.
func (e *edge) optional(v cue.Value, p cue.Path) bool {
	sels := p.Selectors()
	for i := range sels {
		parent := lookup(v, cue.MakePath(sels[:i]...))
		iter, err := parent.Fields(cue.Optional(true))
		if err != nil {
			return false
		}
		for iter.Next() {
			if sel := iter.Selector(); sel.IsString() && sel.Unquoted() == sels[i].Unquoted() {
				if sel.ConstraintType() == cue.OptionalConstraint {
					return true
				}
				break
			}
		}
	}
	return false
}

// required reports whether an instance of v must supply p: it is not optional,
// and the value there is not complete without data (a default, a constant or a
// struct whose fields all fill count as complete).
func (e *edge) required(v cue.Value, p cue.Path) bool {
	if e.optional(v, p) {
		return false
	}
	return lookup(v, p).Validate(cue.Concrete(true)) != nil
}

// lookup returns the value v declares at p, whatever the form of the fields
// along it: a plain selector finds only regular fields, an optional one finds
// regular, optional and required fields (see TestLookupPathFieldForms).
func lookup(v cue.Value, p cue.Path) cue.Value {
	sels := p.Selectors()
	out := make([]cue.Selector, len(sels))
	for i, s := range sels {
		if s.IsString() {
			s = s.Optional()
		}
		out[i] = s
	}
	return v.LookupPath(cue.MakePath(out...))
}

func isStruct(v cue.Value) bool {
	return v.Exists() && v.IncompleteKind() == cue.StructKind
}

func appendPath(p cue.Path, sel cue.Selector) cue.Path {
	return cue.MakePath(append(append([]cue.Selector{}, p.Selectors()...), sel)...)
}

func pathHasPrefix(p, prefix cue.Path) bool {
	ps, qs := p.Selectors(), prefix.Selectors()
	if len(qs) > len(ps) {
		return false
	}
	for i := range qs {
		if ps[i].String() != qs[i].String() {
			return false
		}
	}
	return true
}

// fmtv renders a value for a message.
func fmtv(v cue.Value) string {
	return fmt.Sprint(v)
}

func oneLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return strings.TrimSuffix(strings.TrimSpace(l), ":")
}

func (d direction) String() string {
	if d == forward {
		return fieldForward
	}
	return fieldBackward
}

// at positions n at p, as the spike established: a struct literal's Lbrace
// directly, anything else through ast.SetPos. The position's relative part
// (newline, blank line) describes the authored construct's layout, not the
// synthesized node's, so it is replaced: a field starts a line, anything else
// carries no layout.
func at[T ast.Node](p token.Pos, n T) T {
	if f := p.File(); f != nil {
		rel := token.NoRelPos
		if _, ok := any(n).(*ast.Field); ok {
			rel = token.Newline
		}
		p = f.Pos(p.Offset(), rel)
	}
	if s, ok := any(n).(*ast.StructLit); ok {
		s.Lbrace = p
		return n
	}
	ast.SetPos(n, p)
	return n
}
