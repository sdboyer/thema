package compile

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
)

// An entry is one mapping in a lens body, after the body's nesting has been
// flattened to full target paths.
type entry struct {
	kind entryKind
	// target is the target path the entry produces. For a drop it is the
	// path the dropped field would have had in the target, used only to
	// check that the target does not require it.
	target cue.Path
	// source is the source path a move reads, or the path a drop drops.
	source cue.Path
	// expr is the author's expression: the reference of a move, or the
	// literal of a constant. Nil for derived entries, whose expression is
	// built at emission.
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
}

type entryKind int

const (
	kindMove entryKind = iota
	kindConst
	kindDrop
)

// A lacunaAttr is a parsed @lacuna(Type, "message") or @lacuna("message").
type lacunaAttr struct {
	typ, msg string
	pos      token.Pos
}

// A rebase says that target fields under target copy, when not mapped, from
// source fields under source: the effect of a nested body embedding input.m.
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

// An edge compiles the two lenses between consecutive versions.
type edge struct {
	ctx      *cue.Context
	from, to *version
	// S and T are the source and target schema values of the lens being
	// compiled; the author's input and result.
	S, T     cue.Value
	sv, tv   thema.SyntacticVersion
	dir      direction
	blockPos token.Pos
	errs     cerrors.Error
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
			e.errf(d.Pos(), "a lens body has no embeddings; a nested body may embed input.<path> to copy from it")
		case *ast.Comprehension:
			e.errf(d.Pos(), "comprehensions are not allowed in a lens body; a reference to an optional field is guarded for you")
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
	lac, ok := e.lacunaAttr(f)
	if !ok {
		return
	}

	switch x := f.Value.(type) {
	case *ast.Ident:
		if x.Name == "drop" {
			b.add(entry{kind: kindDrop, target: tpath, source: spath, expr: x, pos: f.Pos(), lacuna: lac, comments: ast.Comments(f)})
			return
		}
	case *ast.StructLit:
		base := spath
		var elts []ast.Decl
		for _, d := range x.Elts {
			if emb, ok := d.(*ast.EmbedDecl); ok {
				p, ok := refPath(emb.Expr)
				if !ok {
					e.errf(emb.Pos(), "a nested body may embed only input.<path>, to copy from it")
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
	}
	if p, ok := refPath(f.Value); ok {
		b.add(entry{kind: kindMove, target: tpath, source: p, expr: f.Value, pos: f.Pos(), lacuna: lac, comments: ast.Comments(f)})
		return
	}
	if isLiteral(f.Value) {
		b.add(entry{kind: kindConst, target: tpath, expr: f.Value, pos: f.Pos(), lacuna: lac, comments: ast.Comments(f)})
		return
	}
	e.errf(f.Value.Pos(), "%s: a mapping is a reference to input, a constant, a nested body or drop; other expressions are not supported yet", name)
}

// lacunaAttr reads the field's @lacuna attribute. A second @lacuna, or any
// other attribute, is an error.
func (e *edge) lacunaAttr(f *ast.Field) (*lacunaAttr, bool) {
	var out *lacunaAttr
	for _, a := range f.Attrs {
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

// refPath returns the path of a reference input.a.b, if x is one.
func refPath(x ast.Expr) (cue.Path, bool) {
	var sels []cue.Selector
	for {
		switch s := x.(type) {
		case *ast.SelectorExpr:
			name, _, err := ast.LabelName(s.Sel)
			if err != nil {
				return cue.Path{}, false
			}
			sels = append([]cue.Selector{cue.Str(name)}, sels...)
			x = s.X
		case *ast.Ident:
			if s.Name != "input" {
				return cue.Path{}, false
			}
			return cue.MakePath(sels...), true
		default:
			return cue.Path{}, false
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
	// guarded on: if input.<guard> != _|_ {...}.
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
	// result is the lens's result, and lacunas its lacunas.
	result  *ast.StructLit
	lacunas []ast.Expr
	// effective is every move the lens makes, written or implicit, and every
	// drop. Derivation inverts these.
	effective []entry
}

// assemble builds the lens for e from b: explicit entries, implicit copies at
// the shallowest path the two schemas share, and the direction's rule for
// unmapped source fields.
func (e *edge) assemble(b *body) *compiledLens {
	out := &compiledLens{}
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
					e.errf(en.pos, "%s is optional in %s and %s requires %s: orelse(input.%s, <fallback>) would decide what to use when it is absent, but is not supported yet", en.source, e.sv, e.tv, en.target, en.source)
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
			lit := e.ctx.BuildExpr(en.expr)
			if err := lookup(e.T, en.target).Unify(lit).Validate(); err != nil {
				e.errf(en.expr.Pos(), "%s is not an instance of %s's %s: %s", exprText(en.expr), e.tv, en.target, oneLine(cerrors.Details(err, nil)))
				continue
			}
		}
		if en.kind != kindDrop && en.lacuna != nil {
			out.lacunas = append(out.lacunas, e.declaredLacuna(en, "targetFields", "result"))
		}
	}

	e.fill(root, cue.MakePath(), b, consumed, dropped, out)
	e.unmapped(b, consumed, out)

	out.result = e.emit(root, cue.MakePath())
	return out
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
			if isStruct(sv) && isStruct(tv) && !e.covers(sv, tv, spath, tpath) {
				c := n.child(name)
				c.pos = e.blockPos
				e.fill(c, tpath, b, consumed, dropped, out)
				continue
			}
			en := entry{kind: kindMove, target: tpath, source: spath, pos: e.blockPos, derived: true}
			if e.optional(e.S, spath) && e.required(e.T, tpath) {
				e.errf(lookup(e.T, tpath).Pos(), "%s is optional in %s and required in %s: map it with orelse(input.%s, <fallback>), which is not supported yet", spath, e.sv, e.tv, spath)
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
			e.errf(e.blockPos, "%s declares %s and %s does not, and nothing maps it: write <field>: input.%s to move it, or %s: drop", e.sv, spath, e.tv, spath, spath)
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
		var val ast.Expr
		switch {
		case c.leaf == nil:
			val = e.emit(c, appendPath(tbase, cue.Str(name)))
		case c.leaf.expr != nil:
			val = c.leaf.expr
		default:
			val = at(c.pos, inputRef(c.leaf.source))
		}
		f := at(c.pos, &ast.Field{Label: at(c.pos, ast.NewIdent(name)), Value: val})
		if c.leaf != nil {
			for _, cg := range c.leaf.comments {
				ast.AddComment(f, cg)
			}
		}
		if len(c.guard.Selectors()) > 0 {
			s.Elts = append(s.Elts, at(c.pos, &ast.Comprehension{
				Clauses: []ast.Clause{at(c.pos, &ast.IfClause{Condition: at(c.pos, ast.NewBinExpr(token.NEQ, at(c.pos, inputRef(c.guard)), at(c.pos, &ast.BottomLit{})))})},
				Value:   at(c.pos, &ast.StructLit{Elts: []ast.Decl{f}}),
			}))
			continue
		}
		s.Elts = append(s.Elts, f)
	}
	return s
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
// @lacuna of another type adds a declared lacuna as well.
func (e *edge) dropLacuna(out *compiledLens, en *entry) {
	msg := fmt.Sprintf("%s has no equivalent in %s and was dropped", en.source, e.tv)
	if en.lacuna != nil && (en.lacuna.typ == "" || en.lacuna.typ == lacunaDropped) {
		msg = en.lacuna.msg
	} else if en.lacuna != nil {
		out.lacunas = append(out.lacunas, e.declaredLacuna(en, "sourceFields", "input"))
	}
	p := en.pos
	elts := []ast.Decl{at(p, &ast.Attribute{Text: "@thema(derived)"})}
	if e.optional(e.S, en.source) {
		elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("condition")), Value: at(p, ast.NewBinExpr(token.NEQ, at(p, inputRef(en.source)), at(p, &ast.BottomLit{})))}))
	}
	elts = append(elts,
		at(p, &ast.Field{Label: at(p, ast.NewIdent("sourceFields")), Value: at(p, ast.NewList(fieldRef(p, en.source, "input")))}),
		at(p, &ast.Field{Label: at(p, ast.NewIdent("message")), Value: at(p, ast.NewString(msg))}),
		at(p, &ast.Field{Label: at(p, ast.NewIdent("type")), Value: at(p, ast.NewString(lacunaDropped))}),
	)
	out.lacunas = append(out.lacunas, at(p, &ast.StructLit{Elts: elts}))
}

// declaredLacuna builds the lacuna an entry's @lacuna declares, referring to
// the entry's field through side ("targetFields" or "sourceFields") and root
// ("result" or "input").
func (e *edge) declaredLacuna(en *entry, side, root string) ast.Expr {
	p := en.lacuna.pos
	path := en.target
	if side == "sourceFields" {
		path = en.source
	}
	typ := en.lacuna.typ
	if typ == "" {
		e.errf(p, "a lacuna on a mapping needs a type: @lacuna(Type, %q)", en.lacuna.msg)
	}
	return at(p, &ast.StructLit{Elts: []ast.Decl{
		at(p, &ast.Field{Label: at(p, ast.NewIdent(side)), Value: at(p, ast.NewList(fieldRef(p, path, root)))}),
		at(p, &ast.Field{Label: at(p, ast.NewIdent("message")), Value: at(p, ast.NewString(en.lacuna.msg))}),
		at(p, &ast.Field{Label: at(p, ast.NewIdent("type")), Value: at(p, ast.NewString(typ))}),
	}})
}

// fieldRef builds {path: "<p>", value: <root>.<p>}.
func fieldRef(pos token.Pos, p cue.Path, root string) ast.Expr {
	return at(pos, &ast.StructLit{Elts: []ast.Decl{
		at(pos, &ast.Field{Label: at(pos, ast.NewIdent("path")), Value: at(pos, ast.NewString(p.String()))}),
		at(pos, &ast.Field{Label: at(pos, ast.NewIdent("value")), Value: at(pos, ref(root, p))}),
	}})
}

func inputRef(p cue.Path) ast.Expr { return ref("input", p) }

func ref(root string, p cue.Path) ast.Expr {
	var names []string
	for _, s := range p.Selectors() {
		names = append(names, s.Unquoted())
	}
	return ast.NewSel(ast.NewIdent(root), names...)
}

// derive builds the backward body implied by a forward lens: the inverse of
// every single-use move. Authored backward entries override derived ones at
// the same target path, or beneath it.
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
	names := e.fields(v, p)
	for _, name := range names {
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

// covers reports whether the target declares, at tp, every path the source
// declares under sp, so that the source's struct can be copied whole.
func (e *edge) covers(sv, tv cue.Value, sp, tp cue.Path) bool {
	for _, name := range e.fields(sv, cue.MakePath()) {
		t := lookup(tv, cue.MakePath(cue.Str(name)))
		if !t.Exists() {
			return false
		}
		s := lookup(sv, cue.MakePath(cue.Str(name)))
		if isStruct(s) && isStruct(t) && !e.covers(s, t, appendPath(sp, cue.Str(name)), appendPath(tp, cue.Str(name))) {
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
