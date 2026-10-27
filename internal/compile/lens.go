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
	"github.com/sdboyer/thema/lacuna"
)

// An entry is one mapping in a lens body, after the body's nesting has been
// flattened to full target paths.
type entry struct {
	kind entryKind
	// target is the target path the entry produces. For a drop it is the
	// path the dropped field would have had in the target, used only to
	// check that the target does not require it.
	target cue.Path
	// source is the source path a move, orelse, match or each reads, or the
	// path a drop drops.
	source cue.Path
	// expr is the author's expression: the reference of a move, the literal
	// of a constant, the whole call of an operation. Nil for derived entries,
	// whose expression is built at emission.
	expr ast.Expr
	// pos is where the entry is positioned: the author's field, or for a
	// derived entry the construct it was derived from.
	pos token.Pos
	// comments are the author's comments on the field, carried to the result.
	comments []*ast.CommentGroup
	// derived entries were produced by derivation, not written.
	derived bool
	// ok is set once the entry has passed its checks and been placed.
	ok bool

	// fallback is orelse's second argument: a constant, or a reference.
	fallback ast.Expr
	// arms and catchAll are match's arms.
	arms     []arm
	catchAll *arm
	// body is each's element body.
	body *ast.StructLit
}

// An arm is one arm of a match: a value and the constant it maps to.
type arm struct {
	key   string
	value ast.Expr
	pos   token.Pos
}

type entryKind int

const (
	kindMove entryKind = iota
	kindConst
	kindDrop
	kindOrElse
	kindMatch
	kindEach
)

// The operations a lens body may call, and the arguments each takes.
const (
	opDrop   = "drop"
	opOrElse = "orelse"
	opMatch  = "match"
	opEach   = "each"
	opArity  = 2
)

// The roots references start from: input in a lens body, item in an each body.
const (
	rootItem = "item"
)

// The fields of a lens block beside its two versions, and of a lacuna.
const (
	fieldLacunas = "lacunas"
	fieldType    = "type"
	fieldMessage = "message"
	fieldWhen    = "when"
)

// A lacunaDecl is one entry of a lens's lacunas field: a lacuna declared on
// the mapping keyed path, which is the mapping's key in the lens body.
type lacunaDecl struct {
	path     cue.Path
	typ, msg string
	// when is the author's condition; nil means the one the mapping implies.
	when ast.Expr
	pos  token.Pos
	// used is set once a mapping has taken the declaration; emit is the
	// lacuna built for it, when the mapping builds it early (a drop).
	used bool
	emit ast.Expr
}

// A lacunaTable holds a lens's declared lacunas in authored order, keyed by
// full target path. An each body's sub-edge shares its lens's table.
type lacunaTable struct {
	decls []*lacunaDecl
}

// at returns the declarations keyed p.
func (t *lacunaTable) at(p cue.Path) []*lacunaDecl {
	var out []*lacunaDecl
	for _, d := range t.decls {
		if d.path.String() == p.String() {
			out = append(out, d)
		}
	}
	return out
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
	S, T   cue.Value
	sv, tv thema.SyntacticVersion
	dir    direction
	// file is the version file the lens belongs to (the newer version's), and
	// blockPos the position of its block there, or of the file when the
	// author wrote none.
	file     string
	written  bool
	blockPos token.Pos
	// root is the identifier the author's references start from, which is
	// also the normal form's field for the instance the lens takes: the
	// version's parameter (v0_0), or item inside an each body.
	root string
	// lacunas is the lens's declared lacunas.
	lacunas *lacunaTable
	// each is set for the sub-edge compiling an each body.
	each *eachScope
	errs cerrors.Error
}

// key is the lacunas key of an entry of e: its target path, under the list's
// path inside an each body.
func (e *edge) key(en *entry) cue.Path {
	if e.each == nil {
		return en.target
	}
	return joinPath(e.each.target, en.target)
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
			e.errf(d.Pos(), "comprehensions are not allowed in a lens body; a reference to an optional field is copied only when present, and orelse(ref, fallback) decides what to use when it is absent")
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
	if !e.noAttrs(f) {
		return
	}
	en := entry{target: tpath, expr: f.Value, pos: f.Pos(), comments: ast.Comments(f)}

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
		b.rebases = append(b.rebases, rebase{target: tpath, source: base})
		e.parseBody(&ast.StructLit{Lbrace: x.Lbrace, Elts: elts, Rbrace: x.Rbrace}, tpath, base, b)
		return
	case *ast.CallExpr:
		if fn, ok := x.Fun.(*ast.Ident); ok {
			switch fn.Name {
			case opOrElse:
				e.parseOrElse(x, &en, b)
				return
			case opMatch:
				e.parseMatch(x, &en, b)
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
	e.errf(f.Value.Pos(), "%s: a mapping is a reference to %s, a constant, a nested body, drop, orelse, match or each; other expressions are not supported yet", name, e.root)
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

// parseMatch reads match(ref, {value: constant, ..., [_]: constant}).
func (e *edge) parseMatch(x *ast.CallExpr, en *entry, b *body) {
	if len(x.Args) != opArity {
		e.errf(x.Pos(), "match takes two arguments: match(%s.<path>, {...})", e.root)
		return
	}
	p, ok := e.refPath(x.Args[0])
	if !ok {
		e.errf(x.Args[0].Pos(), "match's first argument must be a reference to %s", e.root)
		return
	}
	arms, ok := x.Args[1].(*ast.StructLit)
	if !ok {
		e.errf(x.Args[1].Pos(), "match's arms are a struct of value: value entries, with an optional [_] arm")
		return
	}
	en.kind, en.source = kindMatch, p
	for _, d := range arms.Elts {
		f, ok := d.(*ast.Field)
		if !ok {
			if _, ok := d.(*ast.CommentGroup); ok {
				continue
			}
			e.errf(d.Pos(), "match's arms hold only value: value entries")
			return
		}
		if !isLiteral(f.Value) {
			e.errf(f.Value.Pos(), "a match arm's value must be a constant")
			return
		}
		if !e.noAttrs(f) {
			return
		}
		a := arm{value: f.Value, pos: f.Pos()}
		if isCatchAll(f.Label) {
			if en.catchAll != nil {
				e.errf(f.Pos(), "a match has at most one [_] arm")
				return
			}
			en.catchAll = &a
			continue
		}
		key, _, err := ast.LabelName(f.Label)
		if err != nil {
			e.errf(f.Label.Pos(), "a match arm's key is a string")
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
	en.kind, en.source, en.body = kindEach, p, bodyLit
	b.add(*en)
}

// noAttrs rejects attributes on a mapping or an arm: a lacuna is declared in
// the lens's lacunas field, and nothing else attaches to a mapping.
func (e *edge) noAttrs(f *ast.Field) bool {
	if len(f.Attrs) == 0 {
		return true
	}
	e.errf(f.Attrs[0].Pos(), "a mapping has no attributes; a lacuna is declared in the lens's %s field, keyed by the mapping", fieldLacunas)
	return false
}

// parseLacunas reads a lens's lacunas field into t. Its keys nest like the
// lens body's: a struct descends, a list declares the lacunas of the mapping
// at that key.
func (e *edge) parseLacunas(lit *ast.StructLit, base cue.Path, t *lacunaTable) {
	for _, d := range lit.Elts {
		f, ok := d.(*ast.Field)
		if !ok {
			if _, ok := d.(*ast.CommentGroup); ok {
				continue
			}
			e.errf(d.Pos(), "%s holds only <mapping>: [...] entries, nested like the mappings they describe", fieldLacunas)
			continue
		}
		name, _, err := ast.LabelName(f.Label)
		if err != nil || f.Constraint != token.ILLEGAL || len(f.Attrs) > 0 {
			e.errf(f.Pos(), "a %s key is a plain field name, the key of the mapping it describes", fieldLacunas)
			continue
		}
		p := appendPath(base, cue.Str(name))
		switch x := f.Value.(type) {
		case *ast.StructLit:
			e.parseLacunas(x, p, t)
		case *ast.ListLit:
			if len(t.at(p)) > 0 {
				e.errf(f.Pos(), "%s names %s twice", fieldLacunas, p)
				continue
			}
			for _, el := range x.Elts {
				if d := e.parseLacuna(el, p); d != nil {
					t.decls = append(t.decls, d)
				}
			}
		default:
			e.errf(f.Value.Pos(), "%s.%s is a list of the mapping's lacunas, or a struct of keys beneath it", fieldLacunas, p)
		}
	}
}

// parseLacuna reads one lacuna: {type: Type, message: "...", when: cond},
// when being optional.
func (e *edge) parseLacuna(x ast.Expr, p cue.Path) *lacunaDecl {
	const shape = `a lacuna is {type: Type, message: "..."}, with an optional when: <condition>`
	s, ok := x.(*ast.StructLit)
	if !ok {
		e.errf(x.Pos(), "%s", shape)
		return nil
	}
	d := &lacunaDecl{path: p, pos: x.Pos()}
	seen := make(map[string]bool)
	for _, el := range s.Elts {
		f, ok := el.(*ast.Field)
		if !ok {
			if _, ok := el.(*ast.CommentGroup); ok {
				continue
			}
			e.errf(el.Pos(), "%s", shape)
			return nil
		}
		name, _, err := ast.LabelName(f.Label)
		if err != nil || f.Constraint != token.ILLEGAL || len(f.Attrs) > 0 {
			e.errf(f.Pos(), "%s", shape)
			return nil
		}
		if seen[name] {
			e.errf(f.Pos(), "a lacuna has one %s", name)
			return nil
		}
		seen[name] = true
		switch name {
		case fieldType:
			id, ok := f.Value.(*ast.Ident)
			if !ok || !isLacunaType(id.Name) {
				e.errf(f.Value.Pos(), "%s is not a lacuna type; the types are %s", exprText(f.Value), lacunaTypeNames())
				return nil
			}
			d.typ = id.Name
		case fieldMessage:
			lit, ok := f.Value.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				e.errf(f.Value.Pos(), "a lacuna's message is a string")
				return nil
			}
			msg, err := literal.Unquote(lit.Value)
			if err != nil {
				e.errf(f.Value.Pos(), "a lacuna's message is a string")
				return nil
			}
			d.msg = msg
		case fieldWhen:
			if !e.condition(f.Value) {
				return nil
			}
			d.when = f.Value
		default:
			e.errf(f.Pos(), "%s is not a field of a lacuna; %s", name, shape)
			return nil
		}
	}
	if d.typ == "" || !seen[fieldMessage] {
		e.errf(x.Pos(), "%s", shape)
		return nil
	}
	return d
}

// condition checks an authored condition, whose references the normal form
// keeps as written. A condition reads fields the source always has, tests
// the presence of any source field (<ref> != _|_, <ref> == _|_), and uses
// CUE's builtins; reading an optional field is not supported yet, since
// nothing proves the condition evaluates when it is absent.
func (e *edge) condition(x ast.Expr) bool {
	ok := true
	labels := make(map[*ast.Ident]bool)
	ast.Walk(x, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if n.Op != token.NEQ && n.Op != token.EQL {
				return true
			}
			if _, isBottom := n.Y.(*ast.BottomLit); !isBottom {
				return true
			}
			if p, isRef := e.refPath(n.X); isRef {
				if !lookup(e.S, p).Exists() {
					e.errf(n.X.Pos(), "%s does not declare %s", e.sv, p)
					ok = false
				}
				e.unbind(rootIdent(n.X))
				return false
			}
		case *ast.SelectorExpr:
			if p, isRef := e.refPath(n); isRef {
				switch {
				case !lookup(e.S, p).Exists():
					e.errf(n.Pos(), "%s does not declare %s", e.sv, p)
					ok = false
				case e.optional(e.S, p):
					e.errf(n.Pos(), "%s is optional in %s, and a condition reading an optional field is not supported yet; a condition may test its presence: %s.%s != _|_", p, e.sv, e.root, p)
					ok = false
				}
				e.unbind(rootIdent(n))
				return false
			}
			if id, isID := n.Sel.(*ast.Ident); isID {
				labels[id] = true
			}
		case *ast.Field:
			if id, isID := n.Label.(*ast.Ident); isID {
				labels[id] = true
			}
		case *ast.Ident:
			if labels[n] || isTypeIdent(n.Name) || isBuiltin(n.Name) {
				return false
			}
			if n.Name == e.root {
				e.unbind(n)
				return false
			}
			e.errf(n.Pos(), "%s is not defined in a lens; a condition refers to %s and to CUE's builtins", n.Name, e.root)
			ok = false
		}
		return true
	}, nil)
	return ok
}

// unbind drops the binding the parser gave id, to the authoring file's
// parameter field, so that the evaluator resolves the name in the normal
// form, where the lens's field of the same name declares it.
func (e *edge) unbind(id *ast.Ident) {
	id.Node = nil
	id.Scope = nil
}

// rootIdent is the identifier a reference <root>.a.b starts from.
func rootIdent(x ast.Expr) *ast.Ident {
	for {
		switch s := x.(type) {
		case *ast.SelectorExpr:
			x = s.X
		case *ast.Ident:
			return s
		default:
			panic(fmt.Sprintf("rootIdent of %T", x))
		}
	}
}

func isBuiltin(name string) bool {
	switch name {
	case "len", "and", "or", "div", "mod", "quo", "rem", "close":
		return true
	}
	return false
}

// lacunaDropped is the type of the lacuna a drop derives.
const lacunaDropped = string(lacuna.DroppedField)

func isLacunaType(s string) bool { return lacuna.Type(s).Known() }

// lacunaTypeNames lists the lacuna types, for error messages.
func lacunaTypeNames() string {
	var names []string
	for _, t := range lacuna.Types() {
		names = append(names, string(t))
	}
	return strings.Join(names, ", ")
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
	// present, if non-empty, is an optional source path the leaf is written
	// under: if <root>.<present> != _|_ {...}.
	present cue.Path
	pos     token.Pos
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
	// declarations each bodies need beside them. derived holds the lacunas
	// of drops no declaration refines, until the declared ones are emitted
	// ahead of them.
	result  *ast.StructLit
	lacunas []ast.Expr
	derived []ast.Expr
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
				root.has(en.target).present = en.source
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
		case kindMatch:
			consumed[en.source.String()] = true
			if !e.checkMatch(en) || !e.place(root, en) {
				continue
			}
			out.effective = append(out.effective, *en)
		case kindEach:
			consumed[en.source.String()] = true
			if !e.place(root, en) || !e.compileEach(en, out) {
				continue
			}
			out.effective = append(out.effective, *en)
		}
		en.ok = true
	}

	e.fill(root, cue.MakePath(), b, consumed, dropped, out)
	e.unmapped(b, consumed, out)
	if e.each == nil {
		e.declare(b, out)
		out.lacunas = append(out.lacunas, out.derived...)
		out.derived = nil
	}

	out.result = e.emit(root, cue.MakePath())
}

// declare emits the lacunas the lens declares, in declared order, each under
// its when or the condition its mapping implies: for an orelse, that the
// fallback was used; for a match, that the [_] arm was; for a drop, that the
// field was present. A [_] arm must have a lacuna under its condition.
func (e *edge) declare(b *body, out *compiledLens) {
	catchAllLacuna := make(map[*entry]bool)
	for _, d := range e.lacunas.decls {
		if d.used {
			if d.emit != nil {
				out.lacunas = append(out.lacunas, d.emit)
			}
			continue
		}
		en := b.find(d.path)
		switch {
		case en == nil && b.findEach(d.path) != nil:
			e.errf(d.pos, "%s.%s: a lacuna on a mapping inside each is not supported yet; a %s lacuna on a drop refines the drop's", fieldLacunas, d.path, lacunaDropped)
			continue
		case en == nil:
			e.errf(d.pos, "%s.%s: %s has no mapping keyed %s", fieldLacunas, d.path, e.dir, d.path)
			continue
		case !en.ok:
			continue
		}
		cond := d.when
		switch en.kind {
		case kindDrop:
			if cond == nil && e.optional(e.S, en.source) {
				cond = e.present(d.pos, en.source)
			}
		case kindOrElse:
			if cond == nil {
				cond = e.absent(d.pos, en.source)
			}
		case kindMatch:
			if cond == nil {
				if en.catchAll == nil {
					e.errf(d.pos, "%s.%s: a lacuna on a match applies when its [_] arm does, and this match has none: say when it applies with %s: <condition>", fieldLacunas, d.path, fieldWhen)
					continue
				}
				cond = e.catchAllCondition(en, d.pos)
				if cond == nil {
					continue // emitLeaf reports it
				}
				catchAllLacuna[en] = true
			}
		case kindEach:
			if cond == nil {
				e.errf(d.pos, "%s.%s: a lacuna on each needs %s: <condition>", fieldLacunas, d.path, fieldWhen)
				continue
			}
		}
		d.used = true
		out.lacunas = append(out.lacunas, e.lacunaFor(en, d, cond))
	}
	for i := range b.entries {
		en := &b.entries[i]
		if en.ok && en.kind == kindMatch && en.catchAll != nil && !catchAllLacuna[en] {
			e.errf(en.catchAll.pos, "the [_] arm collapses every unmatched value into one: declare what is lost in %s: {%s: [{%s: Type, %s: \"...\"}]}", fieldLacunas, en.target, fieldType, fieldMessage)
		}
	}
}

// find returns the authored entry keyed p, if any.
func (b *body) find(p cue.Path) *entry {
	for i := range b.entries {
		if b.entries[i].target.String() == p.String() {
			return &b.entries[i]
		}
	}
	return nil
}

// findEach returns the each entry p lies beneath, if any.
func (b *body) findEach(p cue.Path) *entry {
	for i := range b.entries {
		if b.entries[i].kind == kindEach && pathHasPrefix(p, b.entries[i].target) {
			return &b.entries[i]
		}
	}
	return nil
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

// checkMatch checks a match entry: every arm value is an instance of the
// target, and every value the source may hold reaches an arm, the catch-all,
// or passes through to a target that accepts it.
func (e *edge) checkMatch(en *entry) bool {
	if !lookup(e.S, en.source).Exists() {
		e.errf(en.expr.Pos(), "%s does not declare %s", e.sv, en.source)
		return false
	}
	seen := make(map[string]bool)
	for _, a := range en.arms {
		if seen[a.key] {
			e.errf(a.pos, "match has two arms for %q", a.key)
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
			e.errf(en.expr.Pos(), "match on %s is not exhaustive: %s's %s may hold values no arm matches and %s's %s does not accept; add a [_] arm", en.source, e.sv, en.source, e.tv, en.target)
			return false
		}
		return true
	}
	for _, a := range alts {
		if s, err := a.String(); err == nil && seen[s] {
			continue
		}
		if tv.Unify(a).Validate() != nil {
			e.errf(en.expr.Pos(), "match on %s is not exhaustive: %s's %s may be %s, which no arm matches and %s's %s does not accept; add an arm for it, or a [_] arm", en.source, e.sv, en.source, fmtv(a), e.tv, en.target)
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

// passthrough reports whether a match needs a passthrough branch: some
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

// targetType returns the target field's constraint as syntax, for a match's
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
	sub := &edge{ctx: e.ctx, S: selem, T: telem, sv: e.sv, tv: e.tv, dir: e.dir, blockPos: en.pos, root: rootItem,
		lacunas: e.lacunas, each: &eachScope{list: en.source, target: en.target, n: out.nlets}}
	b := &body{}
	sub.parseBody(en.body, cue.MakePath(), cue.MakePath(), b)
	subOut := &compiledLens{nlets: out.nlets}
	sub.assembleInto(b, subOut)
	e.errs = cerrors.Append(e.errs, sub.errs)
	if sub.errs != nil {
		return false
	}
	out.derived = append(out.derived, subOut.derived...)
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
				c.present = spath
			}
			consumed[spath.String()] = true
			out.effective = append(out.effective, en)
			continue
		}
		if e.required(e.T, tpath) && !b.claimed[tpath.String()] {
			e.unproduced(tpath)
		}
	}
}

// unproduced reports a required target field no mapping produces, at the
// lens block the author must add it to, or at the file when there is none.
func (e *edge) unproduced(tpath cue.Path) {
	if e.written {
		e.errf(e.blockPos, "%s produces nothing for %s, which %s requires: add %s: <mapping> to %s", e.dir, tpath, e.tv, tpath, paramName(e.tv))
		return
	}
	e.errf(e.blockPos, "%s has no %s, and %s requires %s: write %s: {%s: _, %s: {%s: <mapping>}}", e.file, e.dir, e.tv, tpath, e.dir, paramName(e.sv), paramName(e.tv), tpath)
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

// emitLeaf renders one produced field: a plain field, a field under a
// presence test, or the arms of an orelse or match.
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
	case kindMatch:
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
				e.errf(en.expr.Pos(), "match on %s: %s's %s is not a self-contained type, so values cannot be tested against it for the passthrough; match every value with an arm", en.source, e.tv, en.target)
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
	if len(c.present.Selectors()) > 0 {
		return []ast.Decl{branch(e.present(p, c.present), val)}
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

// dropLacuna builds the derived DroppedField lacuna for a drop. A declared
// DroppedField lacuna keyed by the drop refines it: its message replaces the
// derived one, and its when the derived condition; the declaration then emits
// it in declared order. Inside an each body, the lacuna is aggregated over
// the list's elements, and has no when.
func (e *edge) dropLacuna(out *compiledLens, en *entry) {
	msg := e.dropMessage(en.source)
	var decl *lacunaDecl
	var decls []*lacunaDecl
	if !en.derived {
		decls = e.lacunas.at(e.key(en))
	}
	for _, d := range decls {
		if d.typ != lacunaDropped {
			continue
		}
		if decl != nil {
			e.errf(d.pos, "%s.%s: a drop has one %s lacuna, which refines the one it derives", fieldLacunas, d.path, lacunaDropped)
			return
		}
		if d.when != nil && e.each != nil {
			e.errf(d.when.Pos(), "%s.%s: a drop inside each aggregates over the list, and its lacuna has no %s", fieldLacunas, d.path, fieldWhen)
			return
		}
		decl, msg = d, d.msg
		d.used = true
	}
	p := en.pos
	elts := []ast.Decl{at(p, &ast.Attribute{Text: "@thema(derived)"})}
	if e.each != nil {
		// let _d_<n> = [for i, item in input.<list> if item.<p> != _|_ {path: "<list>[\(i)].<p>", value: item.<p>}]
		name := fmt.Sprintf("_d_%d", out.nlets)
		out.nlets++
		clauses := []ast.Clause{at(p, &ast.ForClause{Key: at(p, ast.NewIdent("i")), Value: at(p, ast.NewIdent(rootItem)), Source: at(p, ref(paramName(e.sv), e.each.list))})}
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
		switch {
		case decl != nil && decl.when != nil:
			elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("condition")), Value: decl.when}))
		case e.optional(e.S, en.source):
			elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("condition")), Value: e.present(p, en.source)}))
		}
		elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("sourceFields")), Value: at(p, ast.NewList(e.fieldRef(p, en.source, e.root)))}))
	}
	elts = append(elts,
		at(p, &ast.Field{Label: at(p, ast.NewIdent("message")), Value: at(p, ast.NewString(msg))}),
		at(p, &ast.Field{Label: at(p, ast.NewIdent("type")), Value: at(p, ast.NewString(lacunaDropped))}),
	)
	lac := at(p, &ast.StructLit{Elts: elts})
	if decl != nil {
		decl.emit = lac
		return
	}
	out.derived = append(out.derived, lac)
}

// dropMessage is the derived lacuna's default message for dropping p.
func (e *edge) dropMessage(p cue.Path) string {
	if e.each != nil {
		return fmt.Sprintf("%s[].%s has no equivalent in %s and was dropped", e.each.list, p, e.tv)
	}
	return fmt.Sprintf("%s has no equivalent in %s and was dropped", p, e.tv)
}

// catchAllCondition is the condition under which a match's [_] arm applies:
// no arm keys the value and the target rejects it. Nil if the target's type
// cannot be restated, which emitLeaf reports.
func (e *edge) catchAllCondition(en *entry, p token.Pos) ast.Expr {
	ttype, ok := e.targetType(en)
	if !ok {
		return nil
	}
	var keys []ast.Expr
	for _, a := range en.arms {
		keys = append(keys, at(p, ast.NewString(a.key)))
	}
	var unkeyed ast.Expr
	if len(keys) > 0 {
		unkeyed = e.notMatches(p, en.source, at(p, &ast.ParenExpr{X: ast.NewBinExpr(token.OR, keys...)}))
	}
	return and(unkeyed, e.notMatches(p, en.source, at(p, &ast.ParenExpr{X: ttype})))
}

// lacunaFor builds the lacuna d declares on en, applying under cond (nil for
// always).
func (e *edge) lacunaFor(en *entry, d *lacunaDecl, cond ast.Expr) ast.Expr {
	p := d.pos
	var elts []ast.Decl
	if cond != nil {
		elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("condition")), Value: cond}))
	}
	if en.kind == kindDrop {
		elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("sourceFields")), Value: at(p, ast.NewList(e.fieldRef(p, en.source, e.root)))}))
	} else {
		if len(en.source.Selectors()) > 0 {
			src := e.fieldRef(p, en.source, e.root)
			if e.optional(e.S, en.source) {
				src = at(p, &ast.Comprehension{Clauses: []ast.Clause{at(p, &ast.IfClause{Condition: e.present(p, en.source)})}, Value: src})
			}
			elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("sourceFields")), Value: at(p, ast.NewList(src))}))
		}
		elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("targetFields")), Value: at(p, ast.NewList(e.fieldRef(p, en.target, paramName(e.tv))))}))
	}
	elts = append(elts,
		at(p, &ast.Field{Label: at(p, ast.NewIdent("message")), Value: at(p, ast.NewString(d.msg))}),
		at(p, &ast.Field{Label: at(p, ast.NewIdent("type")), Value: at(p, ast.NewString(d.typ))}),
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
func (e *edge) ref(p cue.Path) ast.Expr { return ref(e.root, p) }

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
