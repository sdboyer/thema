package compile

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/cueview"
	"github.com/sdboyer/thema/internal/lenscheck"
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
	// source is the source path a move, coalesce, match or each reads, or the
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

	// fallback is coalesce's second argument: a constant, or a reference.
	fallback ast.Expr
	// computed is a computed expression: the value of a computed mapping, or
	// coalesce's primary when that is computed. reads are the source paths it
	// reads, and optional those of them the source may omit.
	computed        ast.Expr
	reads, optional []cue.Path
	// arms and catchAll are match's arms.
	arms     []arm
	catchAll *arm
	// body is each's element body.
	body *ast.StructLit
	// item is the path within the element that each's body copies, where the
	// body is a copy (_item, _item.<path>) rather than a struct of mappings.
	item *cue.Path
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
	kindCoalesce
	kindMatch
	kindEach
	kindComputed
)

// The operations a lens body may call, and the arguments each takes.
const (
	opDrop   = "_drop"
	opCoalesce = "_coalesce"
	opMatch  = "_match"
	opEach   = "_each"
	opArity  = 2
)

// The roots references start from: input in a lens body, item in an each body.
const (
	rootItem = "_item"
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

// nests reports whether target path p is an authored nested body that copies
// from source path s, its counterpart, which is not a mapping of its own: a
// body that only drops leaves the rest of its struct to copy.
func (b *body) nests(p, s cue.Path) bool {
	for _, r := range b.rebases {
		if r.target.String() == p.String() {
			return r.source.String() == s.String()
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
	// identity is set for the forward edge of a minor version, which has no
	// lens: an instance is already an instance of its minor successor. The
	// edge is assembled only for derivation, so a source field the target
	// does not declare (one an open target admits) is not refused.
	identity bool
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
	// imports collects the packages the lens's expressions call into
	// (strings), for the normal form's import declaration.
	imports map[string]bool
	// each is set for the sub-edge compiling an each body.
	each *eachScope
	errs cerrors.Error
}

// key is the lacunas key of an entry of e: its target path, under the lists'
// paths inside an each body.
func (e *edge) key(en *entry) cue.Path {
	if e.each == nil {
		return en.target
	}
	return e.each.key(en.target)
}

// An eachScope is what an each body's sub-edge knows about its list.
type eachScope struct {
	// list is the source list's path: from input, or inside an each body
	// nested in another, from the enclosing body's item.
	list cue.Path
	// target is the target list's path, from the result or the enclosing
	// body's element.
	target cue.Path
	// n numbers the body's generated lets within the lens.
	n int
	// optional is set when the source may omit the list, so that what
	// iterates it outside the result tests its presence first.
	optional bool
	// parent is the scope of the each body this one is nested in, if any.
	parent *eachScope
}

// key is the full target path, from the result, of p beneath the list's
// element.
func (s *eachScope) key(p cue.Path) cue.Path {
	p = joinPath(s.target, p)
	if s.parent != nil {
		return s.parent.key(p)
	}
	return p
}

// chain is the scope's lists, outermost first.
func (s *eachScope) chain() []*eachScope {
	if s.parent == nil {
		return []*eachScope{s}
	}
	return append(s.parent.chain(), s)
}

// name renders the source lists as the author reads an element's path:
// items[], or items[].tags[] for a list in an element of another.
func (s *eachScope) name() string {
	var parts []string
	for _, c := range s.chain() {
		parts = append(parts, c.list.String()+"[]")
	}
	return strings.Join(parts, ".")
}

// errf reports a problem with the lens at p, under code. The message is in the
// author's terms: version file names, the lens's parameter names, mappings.
func (e *edge) errf(code diag.Code, p token.Pos, format string, args ...any) {
	e.errs = cerrors.Append(e.errs, diag.New(code).At(p).Headlinef(format, args...).Err())
}

// undeclared reports, under code, that the source version (code E0340) or
// the target version (E0341) does not declare the path p, which a mapping
// reads or writes at pos. Where p is, or lies beneath, a key that only a
// pattern constraint matches, it says so, and how to declare the key: only a
// field with a predictable name is declared (docs/translation.md, A4), and a
// lens reads or writes such a key only with the struct that holds it, copied
// whole. name names p as the headline does.
func (e *edge) undeclared(code diag.Code, pos token.Pos, p cue.Path, name, format string, args ...any) {
	b := diag.New(code).At(pos).Headlinef(format, args...)
	sch, v, verb := e.S, e.sv, "reads"
	if code == diag.E0341 {
		sch, v, verb = e.T, e.tv, "writes"
	}
	if k, ok := cuetil.PatternKey(e.ctx, sch, p); ok {
		key := strings.TrimSuffix(name, strings.TrimPrefix(p.String(), k.String()))
		b.Notef(token.NoPos, "only a pattern constraint of %s matches %s, and a pattern constraint declares no field", v, key)
		if code == diag.E0341 {
			b.Helpf("a lens %s only fields declared by name: declare %s in %s, or copy the struct that holds it whole", verb, key, v)
		} else {
			b.Helpf("a lens %s only fields declared by name: declare %s as an optional field in a new minor version, for the values instances carry there to land in, then map from it in the lens from that version", verb, key)
		}
	}
	e.errs = cerrors.Append(e.errs, b.Err())
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
			e.errf(diag.E0330, d.Pos(), "a lens body has no embeddings; a nested body may embed %s.<path> to copy from it", e.root)
		case *ast.Comprehension:
			e.errf(diag.E0330, d.Pos(), "comprehensions are not allowed in a lens body; a reference to an optional field is copied only when present, and _coalesce(ref, fallback) decides what to use when it is absent")
		case *ast.LetClause:
			e.errf(diag.E0330, d.Pos(), "let is not allowed in a lens body")
		default:
			e.errf(diag.E0330, d.Pos(), "a lens body holds only mappings")
		}
	}
}

func (e *edge) parseField(f *ast.Field, tbase, sbase cue.Path, b *body) {
	name, isIdent, err := ast.LabelName(f.Label)
	if err != nil || f.Constraint != token.ILLEGAL {
		e.errf(diag.E0331, f.Pos(), "a mapping's key is a plain field name")
		return
	}
	if e.notData(f, name, isIdent) {
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
					e.errf(diag.E0334, emb.Pos(), "a nested body may embed only %s.<path>, to copy from it", e.root)
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
			case opCoalesce:
				e.parseCoalesce(x, &en, b)
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
	reads, optional, ok := e.expression(f.Value, false)
	if !ok {
		return
	}
	en.kind, en.computed, en.reads, en.optional = kindComputed, f.Value, reads, optional
	b.add(en)
}

// parseCoalesce reads coalesce(ref, fallback): ref must refer to an optional
// source field, and fallback must be a constant (a struct of constants, for a
// struct) or a reference the source always has.
func (e *edge) parseCoalesce(x *ast.CallExpr, en *entry, b *body) {
	if len(x.Args) != opArity {
		e.errf(diag.E0335, x.Pos(), "_coalesce takes two arguments: _coalesce(%s.<path>, fallback)", e.root)
		return
	}
	fb := x.Args[1]
	if _, ok := e.refPath(fb); !ok && !isConstant(fb) {
		e.errf(diag.E0335, fb.Pos(), "_coalesce's fallback must be a constant or a reference to %s", e.root)
		return
	}
	if p, ok := e.refPath(x.Args[0]); ok {
		en.kind, en.source, en.fallback = kindCoalesce, p, fb
		b.add(*en)
		return
	}
	// A computed primary: the fallback applies when a field it reads is
	// absent, so it must read one the source may omit.
	reads, optional, ok := e.expression(x.Args[0], false)
	if !ok {
		return
	}
	if len(optional) == 0 {
		e.errf(diag.E0348, x.Args[0].Pos(), "%s always has every field %s reads, so _coalesce's fallback could never apply: write %s: %s", e.sv, exprText(x.Args[0]), en.target, exprText(x.Args[0]))
		return
	}
	en.kind, en.computed, en.reads, en.optional, en.fallback = kindCoalesce, x.Args[0], reads, optional, fb
	b.add(*en)
}

// parseMatch reads match(ref, {value: constant, ..., [_]: constant}).
func (e *edge) parseMatch(x *ast.CallExpr, en *entry, b *body) {
	if len(x.Args) != opArity {
		e.errf(diag.E0336, x.Pos(), "_match takes two arguments: _match(%s.<path>, {...})", e.root)
		return
	}
	p, ok := e.refPath(x.Args[0])
	if !ok {
		e.errf(diag.E0336, x.Args[0].Pos(), "_match's first argument must be a reference to %s", e.root)
		return
	}
	arms, ok := x.Args[1].(*ast.StructLit)
	if !ok {
		e.errf(diag.E0336, x.Args[1].Pos(), "_match's arms are a struct of value: value entries, with an optional [_] arm")
		return
	}
	en.kind, en.source = kindMatch, p
	for _, d := range arms.Elts {
		f, ok := d.(*ast.Field)
		if !ok {
			if _, ok := d.(*ast.CommentGroup); ok {
				continue
			}
			e.errf(diag.E0338, d.Pos(), "_match's arms hold only value: value entries")
			return
		}
		if !isLiteral(f.Value) {
			e.errf(diag.E0338, f.Value.Pos(), "a _match arm's value must be a constant")
			return
		}
		if !e.noAttrs(f) {
			return
		}
		a := arm{value: f.Value, pos: f.Pos()}
		if isCatchAll(f.Label) {
			if en.catchAll != nil {
				e.errf(diag.E0338, f.Pos(), "a _match has at most one [_] arm")
				return
			}
			en.catchAll = &a
			continue
		}
		key, _, err := ast.LabelName(f.Label)
		if err != nil {
			e.errf(diag.E0338, f.Label.Pos(), "a _match arm's key is a string")
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

// parseEach reads each(ref, {body}), or each(ref, _item.<path>), whose body
// copies the element or a path within it.
func (e *edge) parseEach(x *ast.CallExpr, en *entry, b *body) {
	if len(x.Args) != opArity {
		e.errf(diag.E0337, x.Pos(), "_each takes two arguments: _each(%s.<list>, {...})", e.root)
		return
	}
	p, ok := e.refPath(x.Args[0])
	if !ok {
		e.errf(diag.E0337, x.Args[0].Pos(), "_each's first argument must be a reference to a list in %s", e.root)
		return
	}
	en.kind, en.source = kindEach, p
	if bodyLit, ok := x.Args[1].(*ast.StructLit); ok {
		en.body = bodyLit
		b.add(*en)
		return
	}
	if root, ip, ok := refPathAny(x.Args[1]); ok && root == rootItem {
		en.item = &ip
		b.add(*en)
		return
	}
	e.errs = cerrors.Append(e.errs, diag.New(diag.E0337).At(x.Args[1].Pos()).
		Headlinef("_each's body %s is neither a struct of mappings over %s nor a copy of it", exprText(x.Args[1]), rootItem).
		Helpf("write a struct of mappings, {name: %s.title}, or copy the element or a field of it: %s, %s.<path>", rootItem, rootItem, rootItem).
		Err())
}

// notData refuses a hidden field or a definition as a key in a lens body or
// its lacunas. Neither is data, so a mapping for one means nothing; and with
// none in a lens, nothing in it can shadow the operations (_drop, _coalesce,
// _match, _each) or _item, which are spelled as hidden identifiers for that
// reason. A quoted label ("_x") is a regular field, and data.
func (e *edge) notData(f *ast.Field, name string, isIdent bool) bool {
	if !isIdent || !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, "#") {
		return false
	}
	kind := "hidden field"
	if strings.HasPrefix(name, "#") {
		kind = "definition"
	}
	e.errs = cerrors.Append(e.errs, diag.New(diag.E0333).At(f.Pos()).
		Headlinef("the %s in %s declares %s, a %s, which is not data", e.dir, e.file, name, kind).
		Note(token.NoPos, "a lens maps data fields, so a key is a field of the target, or of its lacunas, by name").
		Help("remove it; to map a field whose name starts with _ or #, quote the key: \""+name+"\": ...").Err())
	return true
}

// noAttrs rejects attributes on a mapping or an arm: a lacuna is declared in
// the lens's lacunas field, and nothing else attaches to a mapping.
func (e *edge) noAttrs(f *ast.Field) bool {
	if len(f.Attrs) == 0 {
		return true
	}
	e.errf(diag.E0332, f.Attrs[0].Pos(), "a mapping has no attributes; a lacuna is declared in the lens's %s field, keyed by the mapping", fieldLacunas)
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
			e.errf(diag.E0410, d.Pos(), "%s holds only <mapping>: [...] entries, nested like the mappings they describe", fieldLacunas)
			continue
		}
		name, isIdent, err := ast.LabelName(f.Label)
		if err != nil || f.Constraint != token.ILLEGAL || len(f.Attrs) > 0 {
			e.errf(diag.E0410, f.Pos(), "a %s key is a plain field name, the key of the mapping it describes", fieldLacunas)
			continue
		}
		if e.notData(f, name, isIdent) {
			continue
		}
		p := appendPath(base, cue.Str(name))
		switch x := f.Value.(type) {
		case *ast.StructLit:
			e.parseLacunas(x, p, t)
		case *ast.ListLit:
			if len(t.at(p)) > 0 {
				e.errf(diag.E0410, f.Pos(), "%s names %s twice", fieldLacunas, p)
				continue
			}
			for _, el := range x.Elts {
				if d := e.parseLacuna(el, p); d != nil {
					t.decls = append(t.decls, d)
				}
			}
		default:
			e.errf(diag.E0410, f.Value.Pos(), "%s.%s is a list of the mapping's lacunas, or a struct of keys beneath it", fieldLacunas, p)
		}
	}
}

// parseLacuna reads one lacuna: {type: Type, message: "...", when: cond},
// when being optional.
func (e *edge) parseLacuna(x ast.Expr, p cue.Path) *lacunaDecl {
	const shape = `a lacuna is {type: Type, message: "..."}, with an optional when: <condition>`
	s, ok := x.(*ast.StructLit)
	if !ok {
		e.errf(diag.E0410, x.Pos(), "%s", shape)
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
			e.errf(diag.E0410, el.Pos(), "%s", shape)
			return nil
		}
		name, _, err := ast.LabelName(f.Label)
		if err != nil || f.Constraint != token.ILLEGAL || len(f.Attrs) > 0 {
			e.errf(diag.E0410, f.Pos(), "%s", shape)
			return nil
		}
		if seen[name] {
			e.errf(diag.E0410, f.Pos(), "a lacuna has one %s", name)
			return nil
		}
		seen[name] = true
		switch name {
		case fieldType:
			id, ok := f.Value.(*ast.Ident)
			if !ok || !isLacunaType(id.Name) {
				b := diag.New(diag.E0411).At(f.Value.Pos()).
					Headlinef("the lacuna's type %s is not a lacuna type", exprText(f.Value)).
					Helpf("the lacuna types are %s", lacunaTypeNames())
				if lit, isLit := f.Value.(*ast.BasicLit); isLit && lit.Kind == token.STRING {
					if name, err := literal.Unquote(lit.Value); err == nil && isLacunaType(name) {
						b.Helpf("write %s without quotes", name)
					}
				}
				e.errs = cerrors.Append(e.errs, b.Err())
				return nil
			}
			d.typ = id.Name
		case fieldMessage:
			lit, ok := f.Value.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				e.errf(diag.E0410, f.Value.Pos(), "a lacuna's message is a string")
				return nil
			}
			msg, err := literal.Unquote(lit.Value)
			if err != nil {
				e.errf(diag.E0410, f.Value.Pos(), "a lacuna's message is a string")
				return nil
			}
			d.msg = msg
		case fieldWhen:
			cond, ok := e.condition(f.Value)
			if !ok {
				return nil
			}
			d.when = cond
		default:
			e.errf(diag.E0407, f.Pos(), "%s is not a field of a lacuna; %s", name, shape)
			return nil
		}
	}
	if d.typ == "" || !seen[fieldMessage] {
		e.errf(diag.E0410, x.Pos(), "%s", shape)
		return nil
	}
	return d
}

// condition compiles an authored when, whose references the normal form
// keeps as written, and returns the condition the lacuna applies under.
//
// A condition that reads an optional field concerns only the instances that
// have it: it is compiled to [if <field> != _|_ {<when>}, false][0], since
// CUE's && does not short-circuit over an absent field. An optional field
// named bare, when: v0_0.p, tests its presence; a required bool named bare is
// its value; any other bare reference is an error. A presence test written
// out (<ref> != _|_) is refused in favor of the bare form, while an absence
// test (<ref> == _|_) is a condition like any other. Beyond references, a
// condition uses CUE's operators, len and the integer divisions: what the
// totality proof proves a boolean.
func (e *edge) condition(x ast.Expr) (ast.Expr, bool) {
	pos := x.Pos()
	if p, isRef := e.refPath(x); isRef {
		switch {
		case !lookup(e.S, p).Exists():
			e.undeclared(diag.E0340, pos, p, p.String(), "%s does not declare %s", e.sv, p)
			return nil, false
		case e.optional(e.S, p):
			e.unbind(rootIdent(x))
			return e.present(pos, p), true
		case lookup(e.S, p).IncompleteKind() == cue.BoolKind:
			e.unbind(rootIdent(x))
			return x, true
		default:
			e.errf(diag.E0422, pos, "%s.%s is %s, not a condition: compare it; an optional field named bare tests its presence", e.root, p, lookup(e.S, p).IncompleteKind())
			return nil, false
		}
	}
	_, optional, ok := e.expression(x, true)
	if !ok {
		return nil, false
	}
	if len(optional) == 0 {
		return x, true
	}
	// Only the instances that have every optional field read.
	return e.head(pos, e.presentAll(pos, optional), x, at(pos, ast.NewBool(false))), true
}

// expression checks a computed expression, whose references the normal form
// keeps as written, and returns the source paths it reads, and those of them
// the source may omit. An expression reads source fields by path, compares
// and combines them with CUE's operators, and calls len, the integer
// divisions and the strings functions the totality proof proves. In a
// condition, a presence test written out is refused in favor of the bare
// form (see condition).
func (e *edge) expression(x ast.Expr, inCondition bool) (reads, optional []cue.Path, ok bool) {
	ok = true
	seen := make(map[string]bool)
	read := func(p cue.Path) {
		if seen[p.String()] {
			return
		}
		seen[p.String()] = true
		reads = append(reads, p)
		if e.optional(e.S, p) {
			optional = append(optional, p)
		}
	}
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
				switch {
				case !lookup(e.S, p).Exists():
					e.undeclared(diag.E0340, n.X.Pos(), p, p.String(), "%s does not declare %s", e.sv, p)
					ok = false
				case inCondition && n.Op == token.NEQ:
					e.errs = cerrors.Append(e.errs, diag.New(diag.E0423).At(n.Pos()).
						Headlinef("this condition tests the presence of %s.%s, which it also reads", e.root, p).
						Note(token.NoPos, "a condition reading an optional field concerns only the instances that have it, so testing its presence says nothing more").
						Helpf("drop the presence test; to test presence alone, write %s: %s.%s", fieldWhen, e.root, p).
						Err())
					ok = false
				}
				e.unbind(rootIdent(n.X))
				return false
			}
		case *ast.SelectorExpr:
			if p, isRef := e.refPath(n); isRef {
				if !lookup(e.S, p).Exists() {
					e.undeclared(diag.E0340, n.Pos(), p, p.String(), "%s does not declare %s", e.sv, p)
					ok = false
				} else {
					read(p)
				}
				e.unbind(rootIdent(n))
				return false
			}
			if pkg, isID := n.X.(*ast.Ident); isID && pkg.Name == pkgStrings {
				name, _, err := ast.LabelName(n.Sel)
				if err != nil || !lenscheck.StringFunc(name) {
					e.errs = cerrors.Append(e.errs, diag.New(diag.E0357).At(n.Pos()).
						Headlinef("%s is not a function a lens may call", exprText(n)).
						Helpf("the strings functions a lens may call are %s", strings.Join(lenscheck.StringFuncs(), ", ")).
						Err())
					ok = false
				}
				e.imports[pkgStrings] = true
				e.unbind(pkg)
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
			e.errf(diag.E0418, n.Pos(), "%s is not defined in a lens; an expression refers to %s, to %s and to the strings functions", n.Name, e.root, strings.Join(lenscheck.Builtins, ", "))
			ok = false
		}
		return true
	}, nil)
	return reads, optional, ok
}

// pkgStrings is the one package a lens's expressions may call into.
const pkgStrings = "strings"

// inputRefs renders paths as the author reads them, for messages.
func (e *edge) inputRefs(paths []cue.Path) string {
	var out []string
	for _, p := range paths {
		out = append(out, e.root+"."+p.String())
	}
	return strings.Join(out, ", ")
}

// presentAll is the conjunction of presence tests for paths, positioned at p.
func (e *edge) presentAll(p token.Pos, paths []cue.Path) ast.Expr {
	cond := e.present(p, paths[0])
	for _, q := range paths[1:] {
		cond = at(p, ast.NewBinExpr(token.LAND, cond, e.present(p, q)))
	}
	return cond
}

// head builds [if cond {then}, els][0]: then where cond holds, els otherwise.
func (e *edge) head(p token.Pos, cond, then, els ast.Expr) ast.Expr {
	return at(p, &ast.IndexExpr{
		X: at(p, &ast.ListLit{Elts: []ast.Expr{
			at(p, &ast.Comprehension{
				Clauses: []ast.Clause{at(p, &ast.IfClause{Condition: cond})},
				Value:   at(p, &ast.StructLit{Elts: []ast.Decl{at(p, &ast.EmbedDecl{Expr: then})}}),
			}),
			els,
		}}),
		// Unpositioned: a position would break the index onto its own line.
		Index: ast.NewLit(token.INT, "0"),
	})
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

func isBuiltin(name string) bool { return slices.Contains(lenscheck.Builtins, name) }

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

// isConstant reports whether x is a literal, or a struct literal of regular
// fields whose values are constants: what _coalesce's fallback may be.
func isConstant(x ast.Expr) bool {
	s, ok := x.(*ast.StructLit)
	if !ok {
		return isLiteral(x)
	}
	for _, d := range s.Elts {
		f, ok := d.(*ast.Field)
		if !ok || f.Constraint != token.ILLEGAL || len(f.Attrs) > 0 || !isConstant(f.Value) {
			return false
		}
		if name, isID, err := ast.LabelName(f.Label); err != nil || isID && (strings.HasPrefix(name, "_") || strings.HasPrefix(name, "#")) {
			return false
		}
	}
	return true
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
	// For a leaf: present, if non-empty, are the optional source paths the
	// field is written under: if <root>.<p> != _|_ && ... {...}.
	present []cue.Path
	// For a struct the target may omit: guarded is set, and guard is the
	// condition it is written under (see settle). needs are the fields it
	// requires, each with the condition it is satisfied under, and satisfied
	// the condition all of them are.
	guarded   bool
	guard     dnf
	needs     []need
	satisfied dnf
	pos       token.Pos
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
				e.undeclared(diag.E0340, en.pos, en.source, en.source.String(), "%s does not declare %s, so there is nothing to drop", e.sv, en.source)
				continue
			}
			if t := lookup(e.T, en.target); t.Exists() && e.required(e.T, en.target) {
				e.errf(diag.E0342, en.pos, "%s requires %s; dropping it would leave it absent", e.tv, en.target)
				continue
			}
			dropped[en.source.String()] = true
			consumed[en.source.String()] = true
			out.effective = append(out.effective, *en)
			e.dropLacuna(out, en)
		case kindMove:
			if !lookup(e.S, en.source).Exists() {
				e.undeclared(diag.E0340, en.expr.Pos(), en.source, en.source.String(), "%s does not declare %s", e.sv, en.source)
				continue
			}
			if !e.place(root, en) {
				continue
			}
			if fs := e.misfits(en.source, en.target); len(fs) > 0 {
				// The source is accounted for; only the move is wrong.
				consumed[en.source.String()] = true
				e.copyMisfit(diag.E0359, en.expr.Pos(), en.source, en.target, fs)
				continue
			}
			if e.optional(e.S, en.source) {
				if e.required(e.T, en.target) {
					e.errf(diag.E0343, en.pos, "%s is optional in %s and %s requires %s: write _coalesce(%s.%s, <fallback>) to say what to use when it is absent", en.source, e.sv, e.tv, en.target, e.root, en.source)
					continue
				}
				root.has(en.target).present = e.presence(en)
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
		case kindCoalesce:
			if en.computed == nil {
				consumed[en.source.String()] = true
			}
			for _, p := range en.reads {
				consumed[p.String()] = true
			}
			if p, ok := e.refPath(en.fallback); ok {
				consumed[p.String()] = true
			}
			if !e.checkCoalesce(en) || !e.place(root, en) {
				continue
			}
			out.effective = append(out.effective, *en)
		case kindComputed:
			for _, p := range en.reads {
				consumed[p.String()] = true
			}
			if !e.place(root, en) {
				continue
			}
			if len(en.optional) > 0 {
				if e.required(e.T, en.target) {
					e.errs = cerrors.Append(e.errs, diag.New(diag.E0343).At(en.pos).
						Headlinef("%s reads %s, which %s may omit, and %s requires %s", exprText(en.computed), e.inputRefs(en.optional), e.sv, e.tv, en.target).
						Helpf("write _coalesce(%s, <fallback>) to say what to use when it is absent", exprText(en.computed)).
						Err())
					continue
				}
				root.has(en.target).present = e.presence(en)
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
			optional := e.optional(e.S, en.source)
			if optional && e.required(e.T, en.target) {
				e.errs = cerrors.Append(e.errs, diag.New(diag.E0343).At(en.pos).
					Headlinef("%s is optional in %s and %s requires %s, which _each cannot produce where it is absent", en.source, e.sv, e.tv, en.target).
					Note(token.NoPos, "an _each over a list the source may omit is written only where the list is present, and _each has no fallback").
					Err())
				continue
			}
			if !e.place(root, en) || !e.compileEach(en, out) {
				continue
			}
			if optional {
				root.has(en.target).present = []cue.Path{en.source}
			}
			out.effective = append(out.effective, *en)
		}
		en.ok = true
	}

	e.fill(root, cue.MakePath(), b, consumed, dropped, out)
	e.settle(root, cue.MakePath(), b)
	e.unmapped(b, consumed, out)
	e.setAside(root, cue.MakePath(), always, nil, out)
	if e.each == nil {
		e.declare(b, out)
		out.lacunas = append(out.lacunas, out.derived...)
		out.derived = nil
	}

	out.result = e.emit(root, cue.MakePath(), always)
}

// declare emits the lacunas the lens declares, in declared order, each under
// its when or the condition its mapping implies: for a move or a computed
// value that reads optional fields, that they were present; for a coalesce,
// that the fallback was used; for a match, that the [_] arm was; for a drop,
// that the field was present. A [_] arm must have a lacuna under its
// condition.
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
			e.errf(diag.E0413, d.pos, "%s.%s: a lacuna on a mapping inside _each is not supported yet; a %s lacuna on a _drop refines the _drop's", fieldLacunas, d.path, lacunaDropped)
			continue
		case en == nil:
			e.errf(diag.E0412, d.pos, "%s.%s: %s has no mapping keyed %s", fieldLacunas, d.path, e.dir, d.path)
			continue
		case !en.ok:
			continue
		}
		cond := d.when
		switch en.kind {
		case kindDrop:
			if cond == nil {
				cond = e.dropCondition(d.pos, en.source)
			}
		case kindMove, kindComputed:
			if present := e.presence(en); cond == nil && len(present) > 0 {
				// Where the mapping writes nothing, it loses nothing.
				cond = e.presentAll(d.pos, present)
			}
		case kindCoalesce:
			if cond == nil && en.computed != nil {
				// The fallback was used: not every optional field read was present.
				cond = e.head(d.pos, e.presentAll(d.pos, en.optional), at(d.pos, ast.NewBool(false)), at(d.pos, ast.NewBool(true)))
			} else if cond == nil {
				cond = e.absent(d.pos, en.source)
			}
		case kindMatch:
			if cond == nil {
				if en.catchAll == nil {
					e.errf(diag.E0420, d.pos, "%s.%s: a lacuna on a _match applies when its [_] arm does, and this _match has none: say when it applies with %s: <condition>", fieldLacunas, d.path, fieldWhen)
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
				e.errf(diag.E0415, d.pos, "%s.%s: a lacuna on _each needs %s: <condition>", fieldLacunas, d.path, fieldWhen)
				continue
			}
		}
		d.used = true
		out.lacunas = append(out.lacunas, e.lacunaFor(en, d, cond))
	}
	for i := range b.entries {
		en := &b.entries[i]
		if en.ok && en.kind == kindMatch && en.catchAll != nil && !catchAllLacuna[en] {
			e.errf(diag.E0414, en.catchAll.pos, "the [_] arm collapses every unmatched value into one: declare what is lost in %s: {%s: [{%s: Type, %s: \"...\"}]}", fieldLacunas, en.target, fieldType, fieldMessage)
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
		e.errs = cerrors.Append(e.errs, diag.New(diag.E0344).At(x.Pos()).
			Headlinef("%s is not an instance of %s's %s", exprText(x), e.tv, tpath).
			Cause(err).
			Err())
		return false
	}
	return true
}

// checkCoalesce checks a coalesce entry: its reference is to an optional source
// field, and its fallback is total for the target.
func (e *edge) checkCoalesce(en *entry) bool {
	if en.computed == nil {
		if !lookup(e.S, en.source).Exists() {
			e.undeclared(diag.E0340, en.expr.Pos(), en.source, en.source.String(), "%s does not declare %s", e.sv, en.source)
			return false
		}
		if !e.optional(e.S, en.source) {
			e.errf(diag.E0348, en.expr.Pos(), "%s always has %s, so _coalesce's fallback could never apply: write %s: %s.%s", e.sv, en.source, en.target, e.root, en.source)
			return false
		}
	}
	if p, ok := e.refPath(en.fallback); ok {
		if !lookup(e.S, p).Exists() {
			e.undeclared(diag.E0340, en.fallback.Pos(), p, p.String(), "%s does not declare %s", e.sv, p)
			return false
		}
		if e.optional(e.S, p) {
			e.errf(diag.E0349, en.fallback.Pos(), "%s is optional in %s, so it cannot be _coalesce's fallback: the fallback must always exist", p, e.sv)
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
		e.undeclared(diag.E0340, en.expr.Pos(), en.source, en.source.String(), "%s does not declare %s", e.sv, en.source)
		return false
	}
	seen := make(map[string]bool)
	for _, a := range en.arms {
		if seen[a.key] {
			e.errf(diag.E0350, a.pos, "_match has two arms for %q", a.key)
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
			e.errs = cerrors.Append(e.errs, diag.New(diag.E0351).At(en.expr.Pos()).
				Headlinef("_match on %s is not exhaustive: %s's %s may hold values no arm matches and %s's %s does not accept", en.source, e.sv, en.source, e.tv, en.target).
				Help("add a [_] arm").Err())
			return false
		}
		return true
	}
	for _, a := range alts {
		if s, err := a.String(); err == nil && seen[s] {
			continue
		}
		if tv.Unify(a).Validate() != nil {
			e.errs = cerrors.Append(e.errs, diag.New(diag.E0351).At(en.expr.Pos()).
				Headlinef("_match on %s is not exhaustive: %s's %s may be %s, which no arm matches and %s's %s does not accept", en.source, e.sv, en.source, fmtv(a), e.tv, en.target).
				Helpf("add an arm for %s, or a [_] arm", fmtv(a)).Err())
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
		e.undeclared(diag.E0340, en.expr.Pos(), en.source, en.source.String(), "%s does not declare %s", e.sv, en.source)
		return false
	}
	if sl.IncompleteKind() != cue.ListKind || tl.IncompleteKind() != cue.ListKind {
		e.errf(diag.E0353, en.expr.Pos(), "_each maps a list to a list; %s's %s and %s's %s must both be lists", e.sv, en.source, e.tv, en.target)
		return false
	}
	selem, telem := sl.LookupPath(cue.MakePath(cue.AnyIndex)), tl.LookupPath(cue.MakePath(cue.AnyIndex))
	if !selem.Exists() || !telem.Exists() {
		e.errf(diag.E0353, en.expr.Pos(), "_each maps the elements of open lists ([...T]); a fixed-length list is not supported")
		return false
	}
	sub := &edge{ctx: e.ctx, S: selem, T: telem, sv: e.sv, tv: e.tv, dir: e.dir, file: e.file, written: e.written, blockPos: en.pos, root: rootItem,
		lacunas: e.lacunas, imports: e.imports, each: &eachScope{list: en.source, target: en.target, n: out.nlets, optional: e.optional(e.S, en.source), parent: e.each}}
	subOut := &compiledLens{nlets: out.nlets}
	if en.item != nil {
		sub.eachCopy(en, subOut)
	} else {
		b := &body{}
		sub.parseBody(en.body, cue.MakePath(), cue.MakePath(), b)
		sub.assembleInto(b, subOut)
	}
	e.errs = cerrors.Append(e.errs, sub.errs)
	if sub.errs != nil {
		return false
	}
	out.derived = append(out.derived, subOut.derived...)
	out.lets = append(out.lets, subOut.lets...)
	out.nlets = subOut.nlets
	if en.item != nil {
		// The element is the copy, embedded: [for _item in l {_item.id}].
		en.body = at(en.expr.Pos(), &ast.StructLit{Elts: []ast.Decl{&ast.EmbedDecl{Expr: at(en.expr.Pos(), ref(rootItem, *en.item))}}})
		return true
	}
	en.body = subOut.result
	return true
}

// eachCopy checks an each body that copies the element, or the path within it
// en.item names, as a copy into the target's element: the element must have
// it, every element must have it, since a list has no absent elements, and
// the target's element must accept every value of it. Every other field of
// the element is lost, and dropped by derivation in either direction.
func (e *edge) eachCopy(en *entry, out *compiledLens) {
	p := *en.item
	// Problems with the copy are the body's: _each's second argument.
	pos := en.expr.(*ast.CallExpr).Args[1].Pos()
	elem, item := e.each.name(), rootItem
	if len(p.Selectors()) > 0 {
		elem += "." + p.String()
		item += "." + p.String()
	}
	switch {
	case !lookup(e.S, p).Exists():
		e.undeclared(diag.E0340, pos, p, elem, "%s does not declare %s", e.sv, elem)
		return
	case e.optional(e.S, p):
		e.errs = cerrors.Append(e.errs, diag.New(diag.E0343).At(pos).
			Headlinef("%s is optional in %s, and each element of %s's %s needs a value", elem, e.sv, e.tv, en.target).
			Note(token.NoPos, "an _each whose body copies a field writes one element per source element, so the field must be present in every one").
			Err())
		return
	}
	if fs := e.misfits(p, cue.MakePath()); len(fs) > 0 {
		b := diag.New(diag.E0359).At(pos).
			Headlinef("the %s in %s copies %s into the elements of %s, which do not accept every value of it", e.dir, e.file, elem, en.target).
			Helpf("copy into %s a field whose values its elements accept, or map them with a struct of mappings", en.target)
		r := strings.NewReplacer("the prior schema", e.sv.String(), "the new schema", e.tv.String())
		for _, f := range fs {
			subject := en.target.String() + "[]"
			if sels := f.Path.Selectors(); len(sels) > 0 {
				subject += "." + f.Path.String()
			}
			b.Notef(token.NoPos, "%s in %s %s (copying %s; compatibility rule %s)", subject, e.tv, r.Replace(f.Detail), item, f.Rule)
		}
		e.errs = cerrors.Append(e.errs, b.Err())
		return
	}
	if len(p.Selectors()) == 0 {
		return
	}
	// The copy is the whole target element, so every other field of the
	// source element is lost, whether or not the target's element declares a
	// field of the same name. In either direction it is dropped by
	// derivation: a copy body has nowhere to write a drop, and the author who
	// copies one field as the element has said what the element becomes.
	copied := map[string]bool{p.String(): true}
	for _, spath := range e.leaves(e.S, cue.MakePath()) {
		if consumedUnder(copied, spath) {
			continue
		}
		en := entry{kind: kindDrop, source: spath, target: spath, pos: lookup(e.S, spath).Pos(), derived: true}
		out.effective = append(out.effective, en)
		e.dropLacuna(out, &en)
	}
}

// place puts an explicit entry into the result tree, checking the target
// declares its path and nothing else produces it.
func (e *edge) place(root *node, en *entry) bool {
	if !lookup(e.T, en.target).Exists() {
		e.undeclared(diag.E0341, en.pos, en.target, en.target.String(), "%s does not declare %s", e.tv, en.target)
		return false
	}
	n := root.has(en.target)
	if n != nil && (n.leaf != nil || len(n.fields) > 0) {
		e.errf(diag.E0345, en.pos, "%s is mapped twice", en.target)
		return false
	}
	for p := en.target; len(p.Selectors()) > 1; {
		p = cue.MakePath(p.Selectors()[:len(p.Selectors())-1]...)
		if a := root.has(p); a != nil && a.leaf != nil {
			e.errf(diag.E0345, en.pos, "%s is mapped twice: %s is already mapped whole", en.target, p)
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
		if b.claimed[tpath.String()] && !b.nests(tpath, spath) {
			// An authored mapping that was refused: its error stands, and an
			// implicit copy would only add another.
			continue
		}
		if sv := lookup(e.S, spath); sv.Exists() && !dropped[spath.String()] {
			tv := lookup(e.T, tpath)
			if isStruct(sv) && isStruct(tv) && !e.covers(sv, tv) {
				c := n.child(name)
				c.pos = e.blockPos
				e.fill(c, tpath, b, consumed, dropped, out)
				continue
			}
			en := entry{kind: kindMove, target: tpath, source: spath, pos: e.blockPos, derived: true}
			if fs := e.misfits(spath, tpath); len(fs) > 0 {
				e.copyMisfit(diag.E0358, e.blockPos, spath, tpath, fs)
				continue
			}
			if e.optional(e.S, spath) && e.required(e.T, tpath) {
				e.errf(diag.E0343, lookup(e.T, tpath).Pos(), "%s is optional in %s and required in %s: write %s: _coalesce(%s.%s, <fallback>) to say what to use when it is absent", spath, e.sv, e.tv, tpath, e.root, spath)
				continue
			}
			c := n.child(name)
			c.leaf = &en
			c.pos = en.pos
			if e.optional(e.S, spath) {
				c.present = []cue.Path{spath}
			}
			consumed[spath.String()] = true
			out.effective = append(out.effective, en)
			continue
		}
		if e.required(e.T, tpath) {
			e.unproduced(tpath)
		}
	}
}

// misfits returns why a copy of source path spath into target path tpath
// would not always produce an instance of the target: the compatibility
// checker's first clause between the two values (every source value is a
// target value), with defaults set aside, since a copied value is data. Only
// definite findings: an Unknown verdict is left to the totality proof at
// bind, which reports it in full. A required field the target adds beneath
// the copy is left too, since the lens may write it.
func (e *edge) misfits(spath, tpath cue.Path) []compat.Finding {
	sv, tv := lookup(e.S, spath), lookup(e.T, tpath)
	if !sv.Exists() || !tv.Exists() {
		return nil
	}
	var out []compat.Finding
	for _, f := range compat.Check(e.ctx, sv, tv).Instances().Findings {
		if f.Verdict != compat.Incompatible || f.Rule == compat.RuleFieldAddedRequired {
			continue
		}
		out = append(out, f)
	}
	return out
}

// copyMisfit reports a copy or a move (code says which) of spath into tpath
// that would produce a value the target rejects, with the checker's findings
// as notes.
func (e *edge) copyMisfit(code diag.Code, p token.Pos, spath, tpath cue.Path, fs []compat.Finding) {
	b := diag.New(code).At(p)
	if code == diag.E0358 {
		b.Headlinef("the %s in %s would copy %s.%s into %s, which does not accept every value of it", e.dir, e.file, e.root, spath, tpath)
		b.Notef(token.NoPos, "both %s and %s declare %s, so a lens that does not map it copies it", e.sv, e.tv, tpath)
		b.Helpf("map %s in the %s: a _match, a computed value, or a constant", tpath, e.dir)
	} else {
		b.Headlinef("the %s in %s moves %s.%s into %s, which does not accept every value of it", e.dir, e.file, e.root, spath, tpath)
		b.Helpf("map %s from a value it accepts: a _match, or a computed value", tpath)
	}
	r := strings.NewReplacer("the prior schema", e.sv.String(), "the new schema", e.tv.String())
	for _, f := range fs {
		// The checker's sentence describes the target's value, against the
		// source's ("is now X, which does not accept every value of the prior Y").
		subject := tpath.String()
		if sels := f.Path.Selectors(); len(sels) > 0 {
			subject += "." + f.Path.String()
		}
		verb := "moving"
		if code == diag.E0358 {
			verb = "copying"
		}
		b.Notef(token.NoPos, "%s in %s %s (%s %s.%s; compatibility rule %s)", subject, e.tv, r.Replace(f.Detail), verb, e.root, spath, f.Rule)
	}
	e.errs = cerrors.Append(e.errs, b.Err())
}

// unproduced reports a required target field no mapping produces, at the
// lens block the author must add it to, or at the file when there is none.
func (e *edge) unproduced(tpath cue.Path) {
	if e.written {
		e.errf(diag.E0346, e.blockPos, "%s produces nothing for %s, which %s requires: add %s: <mapping> to %s", e.dir, tpath, e.tv, tpath, paramName(e.tv))
		return
	}
	e.errf(diag.E0346, e.blockPos, "%s has no %s, and %s requires %s: write %s: {%s: _, %s: {%s: <mapping>}}", e.file, e.dir, e.tv, tpath, e.dir, paramName(e.sv), paramName(e.tv), tpath)
}

// unmapped applies the direction's rule to every source field the target does
// not declare and nothing consumed (see unaccounted).
func (e *edge) unmapped(b *body, consumed map[string]bool, out *compiledLens) {
	for _, spath := range e.unaccounted(b, consumed, cue.MakePath()) {
		switch e.dir {
		case forward:
			if e.identity {
				// No lens: the instance keeps it, and its successor admits it.
				continue
			}
			e.errf(diag.E0347, e.blockPos, "%s declares %s and %s does not, and nothing maps it: write <field>: %s.%s to move it, or %s: _drop", e.sv, spath, e.tv, e.root, spath, spath)
		case backward:
			if e.carries(b, consumed, spath) {
				// Translation carries it as a field the target does not
				// declare (#193): nothing to drop.
				continue
			}
			en := entry{kind: kindDrop, source: spath, target: spath, pos: lookup(e.S, spath).Pos(), derived: true}
			out.effective = append(out.effective, en)
			e.dropLacuna(out, &en)
		}
	}
}

// unaccounted returns the source fields under p that the target does not
// declare (or whose counterpart a rebase puts elsewhere) and nothing
// consumed. A struct is returned whole, since its presence is information,
// unless something beneath it was consumed: then its remaining fields are.
func (e *edge) unaccounted(b *body, consumed map[string]bool, p cue.Path) []cue.Path {
	var out []cue.Path
	for _, name := range e.fields(e.S, p) {
		fp := appendPath(p, cue.Str(name))
		if consumedUnder(consumed, fp) {
			continue
		}
		kept := lookup(e.T, fp).Exists() && !e.rebased(b, fp)
		if isStruct(lookup(e.S, fp)) && (kept || consumedBeneath(consumed, fp)) {
			out = append(out, e.unaccounted(b, consumed, fp)...)
			continue
		}
		if !kept {
			out = append(out, fp)
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

// carries reports whether translation carries spath, a source field the
// target does not declare that nothing consumed, rather than the derived
// backward lens dropping it: see carriedField. A field under a rebase, or in
// an each body whose list the lens moves, has its counterpart elsewhere in the
// target, where translation does not follow it, so it is dropped.
func (e *edge) carries(b *body, consumed map[string]bool, spath cue.Path) bool {
	if e.rebased(b, spath) || e.each != nil && e.each.list.String() != e.each.target.String() {
		return false
	}
	return carriedField(e.ctx, e.T, spath, func(outer cue.Path) bool {
		for k := range consumed {
			if pathHasPrefix(cue.ParsePath(k), outer) {
				return true
			}
		}
		return false
	})
}

// carriedField reports whether translation carries the source field spath
// into target, which does not declare it: the target admits, through an open
// struct's ..., the outermost field along spath it does not declare, and the
// lens reads nothing at or beneath that field (read reports whether it does).
// Translation carries the fields of an instance that no lens maps wherever
// the target struct is open (#193), so the derived backward lens drops only
// what translation would not carry. Decompile applies the same rule to
// recognize the drops derivation makes.
func carriedField(ctx *cue.Context, target cue.Value, spath cue.Path, read func(outer cue.Path) bool) bool {
	sels := spath.Selectors()
	for i := range sels {
		outer := cue.MakePath(sels[:i+1]...)
		if lookup(target, outer).Exists() {
			continue
		}
		if read(outer) {
			return false
		}
		parent := lookup(target, cue.MakePath(sels[:i]...))
		return cuetil.CloseRecursive(ctx, parent).Allows(sels[i])
	}
	return false
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

// consumedBeneath reports whether something strictly beneath p was consumed.
func consumedBeneath(consumed map[string]bool, p cue.Path) bool {
	for k := range consumed {
		if pathHasPrefix(cue.ParsePath(k), p) && k != p.String() {
			return true
		}
	}
	return false
}

// emit builds the result struct from the tree, in the target's field order.
// enc is the condition the enclosing structs are written under, which the
// tests beneath them need not repeat.
func (e *edge) emit(n *node, tbase cue.Path, enc dnf) *ast.StructLit {
	s := &ast.StructLit{}
	for _, name := range e.order(n, tbase) {
		c := n.fields[name]
		if c.leaf == nil {
			if c.guarded && len(c.guard) == 0 {
				continue // never written: settle has reported why
			}
			var cond dnf
			inner := enc
			if c.guarded && !e.implies(enc, c.guard) {
				cond, inner = e.beneath(c.guard, enc), e.and(enc, c.guard)
			}
			f := at(c.pos, &ast.Field{Label: at(c.pos, label(name)), Value: e.emit(c, appendPath(tbase, cue.Str(name)), inner)})
			if cond == nil {
				s.Elts = append(s.Elts, f)
				continue
			}
			// The totality proof follows conjunctions, so a condition with
			// several terms is written as disjoint conjunctions, each arm
			// writing the same struct.
			for _, cb := range e.disjoint(cond) {
				s.Elts = append(s.Elts, at(c.pos, &ast.Comprehension{
					Clauses: []ast.Clause{at(c.pos, &ast.IfClause{Condition: e.cubeExpr(c.pos, cb)})},
					Value:   at(c.pos, &ast.StructLit{Elts: []ast.Decl{f}}),
				}))
			}
			continue
		}
		s.Elts = append(s.Elts, e.emitLeaf(name, c, e.beneathTerm(c.present, enc))...)
	}
	return s
}

// emitLeaf renders one produced field: a plain field, a field under a
// presence test of the paths in present, or the arms of a coalesce or match.
func (e *edge) emitLeaf(name string, c *node, present []cue.Path) []ast.Decl {
	en, p := c.leaf, c.pos
	field := func(val ast.Expr) *ast.Field {
		f := at(p, &ast.Field{Label: at(p, label(name)), Value: val})
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
	case kindCoalesce:
		fallback := en.fallback
		if fp, ok := e.refPath(en.fallback); ok {
			fallback = at(en.fallback.Pos(), e.ref(fp))
		}
		if en.computed != nil {
			// The expression where every optional field it reads is present,
			// the fallback otherwise.
			return []ast.Decl{field(e.head(p, e.presentAll(p, en.optional), en.computed, fallback))}
		}
		return []ast.Decl{
			branch(e.present(p, en.source), at(p, e.ref(en.source))),
			branch(e.absent(p, en.source), fallback),
		}
	case kindComputed:
		if len(present) > 0 {
			return []ast.Decl{branch(e.presentAll(p, present), en.computed)}
		}
		return []ast.Decl{field(en.computed)}
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
				e.errs = cerrors.Append(e.errs, diag.New(diag.E0352).At(en.expr.Pos()).
					Headlinef("_match on %s: %s's %s is not a self-contained type", en.source, e.tv, en.target).
					Note(token.NoPos, "a value no arm matches passes through where the target accepts it, which cannot be tested against a type that refers to other fields").
					Help("match every value with an arm, or add a [_] arm").
					Err())
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
		if len(c.present) > 0 {
			return []ast.Decl{branch(e.presentAll(p, c.present), list)}
		}
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
	if len(present) > 0 {
		return []ast.Decl{branch(e.presentAll(p, present), val)}
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
			e.errf(diag.E0416, d.pos, "%s.%s: a _drop has one %s lacuna, which refines the one it derives", fieldLacunas, d.path, lacunaDropped)
			return
		}
		if d.when != nil && e.each != nil {
			e.errf(diag.E0421, d.when.Pos(), "%s.%s: a _drop inside _each aggregates over the list, and its lacuna has no %s", fieldLacunas, d.path, fieldWhen)
			return
		}
		decl, msg = d, d.msg
		d.used = true
	}
	p := en.pos
	elts := []ast.Decl{at(p, &ast.Attribute{Text: "@thema(derived)"})}
	if e.each != nil {
		var conds []ast.Expr
		if e.optional(e.S, en.source) {
			conds = append(conds, e.present(p, en.source))
		}
		if e.container(en.source) {
			conds = append(conds, e.nonEmpty(p, en.source))
		}
		name := e.aggregate(out, p, conds, en.source)
		elts = append(elts,
			at(p, &ast.Field{Label: at(p, ast.NewIdent("condition")), Value: e.nonEmptyLet(p, name)}),
			at(p, &ast.Field{Label: at(p, ast.NewIdent("sourceFields")), Value: at(p, ast.NewIdent(name))}),
		)
	} else {
		switch {
		case decl != nil && decl.when != nil:
			elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("condition")), Value: decl.when}))
		case e.dropCondition(p, en.source) != nil:
			elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("condition")), Value: e.dropCondition(p, en.source)}))
		}
		elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("sourceFields")), Value: at(p, ast.NewList(e.dropSource(p, en.source)))}))
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

// aggregate declares the let a lacuna inside an each body reports through,
// listing path in every element where conds hold, and returns its name:
//
//	let _d_<n> = [for i, item in input.<list> if <cond> ... {path: "<list>[\(i)].<path>", value: item.<path>}]
//
// testing first that the list is present where the source may omit it.
// Inside an each nested in another, there is one for clause per list,
// outermost first, each over the enclosing clause's item, and one index per
// list in the path: "<list>[\(i)].<inner>[\(i1)].<path>".
func (e *edge) aggregate(out *compiledLens, p token.Pos, conds []ast.Expr, path cue.Path) string {
	name := fmt.Sprintf("_d_%d", out.nlets)
	out.nlets++
	inner := func(s string) string { q := strconv.Quote(s); return q[1 : len(q)-1] }
	var clauses []ast.Clause
	var parts []ast.Expr
	for k, s := range e.each.chain() {
		root, idx, open := rootItem, fmt.Sprintf("i%d", k), `)].`
		if k == 0 {
			root, idx, open = paramName(e.sv), "i", `"`
		}
		if s.optional {
			clauses = append(clauses, at(p, &ast.IfClause{Condition: at(p, ast.NewBinExpr(token.NEQ, at(p, ref(root, s.list)), at(p, &ast.BottomLit{})))}))
		}
		clauses = append(clauses, at(p, &ast.ForClause{Key: at(p, ast.NewIdent(idx)), Value: at(p, ast.NewIdent(rootItem)), Source: at(p, ref(root, s.list))}))
		parts = append(parts, at(p, ast.NewLit(token.STRING, open+inner(s.list.String()+"[")+`\(`)), at(p, ast.NewIdent(idx)))
	}
	for _, c := range conds {
		clauses = append(clauses, at(p, &ast.IfClause{Condition: c}))
	}
	pathLit := at(p, &ast.Interpolation{Elts: append(parts, at(p, ast.NewLit(token.STRING, `)].`+path.String()+`"`)))})
	comp := at(p, &ast.Comprehension{Clauses: clauses, Value: at(p, &ast.StructLit{Elts: []ast.Decl{
		at(p, &ast.Field{Label: at(p, ast.NewIdent("path")), Value: pathLit}),
		at(p, &ast.Field{Label: at(p, ast.NewIdent("value")), Value: at(p, e.ref(path))}),
	}})})
	out.lets = append(out.lets, at(p, &ast.LetClause{Ident: at(p, ast.NewIdent(name)), Expr: at(p, &ast.ListLit{Elts: []ast.Expr{comp}})}))
	return name
}

// nonEmptyLet is len(<name>) > 0, an aggregate's condition.
func (e *edge) nonEmptyLet(p token.Pos, name string) ast.Expr {
	return at(p, ast.NewBinExpr(token.GTR, at(p, &ast.CallExpr{Fun: at(p, ast.NewIdent("len")), Args: []ast.Expr{at(p, ast.NewIdent(name))}}), at(p, ast.NewLit(token.INT, "0"))))
}

// dropCondition is the condition a drop of path implies, nil for always: that
// the field was present, and for a struct or a list, that it was not empty.
// An empty container carries no information, so dropping one loses nothing.
func (e *edge) dropCondition(p token.Pos, path cue.Path) ast.Expr {
	optional, container := e.optional(e.S, path), e.container(path)
	switch {
	case optional && container:
		// len of an absent field is not false but an error, so the length is
		// read only where the field is present.
		return e.head(p, e.present(p, path), e.nonEmpty(p, path), at(p, ast.NewBool(false)))
	case optional:
		return e.present(p, path)
	case container:
		return e.nonEmpty(p, path)
	}
	return nil
}

// dropSource is the field a drop's lacuna reports. Where its condition reads
// an optional container's length under a presence test, the field is read
// under the same test.
func (e *edge) dropSource(p token.Pos, path cue.Path) ast.Expr {
	ref := e.fieldRef(p, path, e.root)
	if e.optional(e.S, path) && e.container(path) {
		return at(p, &ast.Comprehension{Clauses: []ast.Clause{at(p, &ast.IfClause{Condition: e.present(p, path)})}, Value: ref})
	}
	return ref
}

// container reports whether the source's field at p is a struct or a list.
func (e *edge) container(p cue.Path) bool {
	k := lookup(e.S, p).IncompleteKind()
	return k == cue.StructKind || k == cue.ListKind
}

// nonEmpty is len(<root>.<path>) > 0, positioned at p.
func (e *edge) nonEmpty(p token.Pos, path cue.Path) ast.Expr {
	return at(p, ast.NewBinExpr(token.GTR, at(p, &ast.CallExpr{Fun: at(p, ast.NewIdent("len")), Args: []ast.Expr{at(p, e.ref(path))}}), at(p, ast.NewLit(token.INT, "0"))))
}

// dropMessage is the derived lacuna's default message for dropping p.
func (e *edge) dropMessage(p cue.Path) string {
	if e.each != nil {
		return fmt.Sprintf("%s.%s has no equivalent in %s and was dropped", e.each.name(), p, e.tv)
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

// presence returns the optional source paths a mapping's target field is
// written under, none if it is written always: the source of a move, and the
// optional fields a computed value reads. The result and the lacunas that
// report the field use the same test.
func (e *edge) presence(en *entry) []cue.Path {
	switch en.kind {
	case kindMove:
		if e.optional(e.S, en.source) {
			return []cue.Path{en.source}
		}
	case kindComputed:
		return en.optional
	}
	return nil
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
		elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("sourceFields")), Value: at(p, ast.NewList(e.dropSource(p, en.source)))}))
	} else {
		var srcs []ast.Expr
		paths := en.reads
		if len(paths) == 0 && len(en.source.Selectors()) > 0 {
			paths = []cue.Path{en.source}
		}
		for _, sp := range paths {
			src := e.fieldRef(p, sp, e.root)
			if e.optional(e.S, sp) {
				src = at(p, &ast.Comprehension{Clauses: []ast.Clause{at(p, &ast.IfClause{Condition: e.present(p, sp)})}, Value: src})
			}
			srcs = append(srcs, src)
		}
		if len(srcs) > 0 {
			elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("sourceFields")), Value: at(p, ast.NewList(srcs...))}))
		}
		// The target field exists only where the mapping writes it, so it
		// is reported under the same presence test.
		tgt := e.fieldRef(p, en.target, paramName(e.tv))
		if present := e.presence(en); len(present) > 0 {
			tgt = at(p, &ast.Comprehension{Clauses: []ast.Clause{at(p, &ast.IfClause{Condition: e.presentAll(p, present)})}, Value: tgt})
		}
		elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("targetFields")), Value: at(p, ast.NewList(tgt))}))
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
	var x ast.Expr = ast.NewIdent(root)
	for _, s := range p.Selectors() {
		x = &ast.SelectorExpr{X: x, Sel: label(s.Unquoted())}
	}
	return x
}

// label is the label for a data field named name: an identifier where one
// names it as data, else a quoted string. A name starting with _ or # would
// be a hidden field or a definition as an identifier, and must be quoted to
// be data; so must a name that is not an identifier at all.
func label(name string) ast.Label {
	if ast.IsValidIdent(name) && !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, "#") {
		return ast.NewIdent(name)
	}
	return ast.NewString(name)
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

// optionalAt reports whether the field at p is itself optional in v, whatever
// the fields above it.
func (e *edge) optionalAt(v cue.Value, p cue.Path) bool {
	sels := p.Selectors()
	if len(sels) == 0 {
		return false
	}
	iter, err := lookup(v, cue.MakePath(sels[:len(sels)-1]...)).Fields(cue.Optional(true))
	if err != nil {
		return false
	}
	name := sels[len(sels)-1].Unquoted()
	for iter.Next() {
		if sel := iter.Selector(); sel.IsString() && sel.Unquoted() == name {
			return sel.ConstraintType() == cue.OptionalConstraint
		}
	}
	return false
}

// required reports whether an instance of v must supply p: it is not optional,
// and it is marked required (p!), or the value there is not complete without
// data (a default, a constant or a struct whose fields all fill count as
// complete). An open list is complete, as the empty list, unless marked.
func (e *edge) required(v cue.Value, p cue.Path) bool {
	if e.optional(v, p) {
		return false
	}
	if e.marked(v, p) {
		return true
	}
	return lookup(v, p).Validate(cue.Concrete(true)) != nil
}

// marked reports whether the last field along p is a required field (p!).
func (e *edge) marked(v cue.Value, p cue.Path) bool {
	sels := p.Selectors()
	if len(sels) == 0 {
		return false
	}
	iter, err := lookup(v, cue.MakePath(sels[:len(sels)-1]...)).Fields(cue.Optional(true))
	if err != nil {
		return false
	}
	last := sels[len(sels)-1]
	for iter.Next() {
		if sel := iter.Selector(); sel.IsString() && sel.Unquoted() == last.Unquoted() {
			return sel.ConstraintType() == cue.RequiredConstraint
		}
	}
	return false
}

// lookup returns the value v declares at p, whatever the form of the fields
// along it: a plain selector finds only regular fields, an optional one finds
// regular, optional and required fields (see TestLookupPathFieldForms).
//
// A name an open struct admits only through its ... is not declared, though
// LookupPath finds it (as _): the result does not exist, by the rule bind and
// the totality check apply (cuetil.DeclaresPath). So a field only one version
// declares is never copied implicitly, nor treated as present in the other.
func lookup(v cue.Value, p cue.Path) cue.Value {
	//nolint:staticcheck // every schema the compiler looks into is built in the one context it was given
	if !cuetil.DeclaresPath(v.Context(), v, p) {
		return cue.Value{}
	}
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

// Presence. A struct the target may omit exists when there is enough data for
// it to exist: settle decides the condition each one is written under, emit
// writes it, and setAside reports what an unwritten struct would have held.

// A dnf is a condition on what the source has, in disjunctive normal form:
// each term is a conjunction of source paths that are present. A term with
// no paths always holds; a dnf with no terms never does.
type dnf [][]cue.Path

var always = dnf{nil}

// A need is a field a target struct requires, and the condition it is
// satisfied under.
type need struct {
	path cue.Path
	cond dnf
}

// settle decides the condition each struct beneath n is written under, and
// returns what n writes: the condition any of its content is written under. A
// struct the target may omit is written where any of its content is and
// every field it requires is satisfied, written by the lens or filled by the
// target's default; the presence of the source struct it copies is content of
// its own, so that an empty struct stays empty and an absent one absent. A
// struct the target requires is written wherever its parent is; where a
// field it requires is not written, the lens is not total, which bind
// reports.
func (e *edge) settle(n *node, tbase cue.Path, b *body) (content dnf) {
	needs := always
	if s := b.sourceBase(tbase); isStruct(lookup(e.S, s)) {
		content = e.term(s)
	}
	for _, name := range e.order(n, tbase) {
		c, tpath := n.fields[name], appendPath(tbase, cue.Str(name))
		var w dnf
		if c.leaf != nil {
			w = e.writes(c)
			if e.requiredAt(e.T, tpath) {
				needs = e.and(needs, w)
				n.needs = append(n.needs, need{tpath, w})
			}
		} else {
			w = e.settle(c, tpath, b)
			if e.optionalAt(e.T, tpath) {
				c.guarded, c.guard = true, e.and(w, c.satisfied)
				w = c.guard
			}
		}
		content = e.or(content, w)
	}
	for _, name := range e.fields(e.T, tbase) {
		tpath := appendPath(tbase, cue.Str(name))
		if _, ok := n.fields[name]; ok || !e.requiredAt(e.T, tpath) {
			continue
		}
		// Nothing produces a field n requires, so n is never written. Where
		// n is required all the way up, or the source has the field (a copy
		// that was refused), fill has reported it.
		if !e.required(e.T, tpath) && !b.claimed[tpath.String()] && !lookup(e.S, appendPath(b.sourceBase(tbase), cue.Str(name))).Exists() {
			e.unproduced(tpath)
		}
		needs = nil
		n.needs = append(n.needs, need{tpath, nil})
	}
	n.satisfied = needs
	return content
}

// writes is the condition a leaf is written under.
func (e *edge) writes(c *node) dnf {
	switch en := c.leaf; en.kind {
	case kindMatch, kindEach:
		return e.term(en.source)
	}
	return e.term(c.present...)
}

// requiredAt reports whether the struct holding p must supply it: the field
// is not optional there, and its value is not complete without data.
func (e *edge) requiredAt(v cue.Value, p cue.Path) bool {
	return !e.optionalAt(v, p) && lookup(v, p).Validate(cue.Concrete(true)) != nil
}

// An enclosing struct is one a leaf is written beneath, under its guard.
type enclosing struct {
	path cue.Path
	n    *node
}

// setAside derives a DroppedField lacuna for every value the lens has but
// does not write because a struct it goes into is not written: where the
// value's sources are present and the structs it goes into are not all
// written. enc is the condition the structs above n are written under, and
// chain the guarded ones among them.
func (e *edge) setAside(n *node, tbase cue.Path, enc dnf, chain []enclosing, out *compiledLens) {
	if len(enc) == 0 {
		return // never written: settle has reported why
	}
	for _, name := range e.order(n, tbase) {
		c, tpath := n.fields[name], appendPath(tbase, cue.Str(name))
		if c.leaf == nil {
			inner, ch := enc, chain
			if c.guarded {
				inner = e.and(enc, c.guard)
				ch = append(slices.Clone(chain), enclosing{tpath, c})
			}
			e.setAside(c, tpath, inner, ch, out)
			continue
		}
		srcs := e.reads(c.leaf)
		if len(srcs) == 0 {
			continue
		}
		has := c.present
		if k := c.leaf.kind; k != kindMove && k != kindComputed {
			has = nil
			for _, sp := range srcs {
				if e.optional(e.S, sp) {
					has = append(has, sp)
				}
			}
		}
		lost := e.beneath(enc, e.term(has...))
		if lost == nil {
			continue
		}
		e.setAsideLacuna(out, c.pos, has, srcs, lost, e.setAsideMessage(srcs, has, chain))
	}
}

// reads returns the source paths whose values a leaf writes.
func (e *edge) reads(en *entry) []cue.Path {
	switch en.kind {
	case kindMove, kindMatch, kindEach:
		return []cue.Path{en.source}
	case kindComputed:
		return en.reads
	case kindCoalesce:
		if en.computed != nil {
			return en.reads
		}
		return []cue.Path{en.source}
	}
	return nil
}

// setAsideLacuna builds the lacuna for the values at srcs: where every path
// in has is present and written, the condition the structs they go into are
// written under, does not hold.
func (e *edge) setAsideLacuna(out *compiledLens, p token.Pos, has, srcs []cue.Path, written dnf, msg string) {
	var cond ast.Expr
	if len(has) > 0 {
		cond = e.presentAll(p, has)
	}
	cond = at(p, and(cond, e.notExpr(p, written)))
	if e.each != nil {
		for _, sp := range srcs {
			name := e.aggregate(out, p, []ast.Expr{cond}, sp)
			out.derived = append(out.derived, at(p, &ast.StructLit{Elts: []ast.Decl{
				at(p, &ast.Attribute{Text: "@thema(setaside)"}),
				at(p, &ast.Field{Label: at(p, ast.NewIdent("condition")), Value: e.nonEmptyLet(p, name)}),
				at(p, &ast.Field{Label: at(p, ast.NewIdent("sourceFields")), Value: at(p, ast.NewIdent(name))}),
				at(p, &ast.Field{Label: at(p, ast.NewIdent("message")), Value: at(p, ast.NewString(msg))}),
				at(p, &ast.Field{Label: at(p, ast.NewIdent("type")), Value: at(p, ast.NewString(lacunaDropped))}),
			}}))
		}
		return
	}
	var fields []ast.Expr
	for _, sp := range srcs {
		src := e.fieldRef(p, sp, e.root)
		if e.optional(e.S, sp) {
			src = at(p, &ast.Comprehension{Clauses: []ast.Clause{at(p, &ast.IfClause{Condition: e.present(p, sp)})}, Value: src})
		}
		fields = append(fields, src)
	}
	out.derived = append(out.derived, at(p, &ast.StructLit{Elts: []ast.Decl{
		at(p, &ast.Attribute{Text: "@thema(setaside)"}),
		at(p, &ast.Field{Label: at(p, ast.NewIdent("condition")), Value: cond}),
		at(p, &ast.Field{Label: at(p, ast.NewIdent("sourceFields")), Value: at(p, ast.NewList(fields...))}),
		at(p, &ast.Field{Label: at(p, ast.NewIdent("message")), Value: at(p, ast.NewString(msg))}),
		at(p, &ast.Field{Label: at(p, ast.NewIdent("type")), Value: at(p, ast.NewString(lacunaDropped))}),
	}}))
}

// setAsideMessage names the values at srcs, and the innermost struct they go
// into that is not written, with the fields it requires that the instance
// does not satisfy.
func (e *edge) setAsideMessage(srcs, has []cue.Path, chain []enclosing) string {
	var names []string
	for _, sp := range srcs {
		if e.each != nil {
			names = append(names, fmt.Sprintf("%s[].%s", e.each.list, sp))
			continue
		}
		names = append(names, sp.String())
	}
	target := func(p cue.Path) string {
		if e.each != nil {
			return fmt.Sprintf("%s[].%s", e.each.target, p)
		}
		return p.String()
	}
	what, verb := strings.Join(names, " and "), "was"
	if len(names) > 1 {
		verb = "were"
	}
	t := e.term(has...)
	for i := len(chain) - 1; i >= 0; i-- {
		s := chain[i]
		var missing []string
		for _, nd := range s.n.needs {
			if !e.implies(t, nd.cond) {
				missing = append(missing, target(nd.path))
			}
		}
		if len(missing) > 0 {
			return fmt.Sprintf("%s %s dropped: %s in %s is written only with %s", what, verb, target(s.path), e.tv, strings.Join(missing, " and "))
		}
	}
	return fmt.Sprintf("%s %s dropped: the struct it goes into in %s is not written", what, verb, e.tv)
}

// atom is the path whose presence p's stands for: its deepest optional
// prefix, since a valid instance that has a struct has every field the struct
// requires. Empty where nothing along p is optional.
func (e *edge) atom(p cue.Path) cue.Path {
	sels := p.Selectors()
	for i := len(sels); i > 0; i-- {
		if q := cue.MakePath(sels[:i]...); e.optionalAt(e.S, q) {
			return q
		}
	}
	return cue.MakePath()
}

// impliesPath reports whether the presence of every path in t implies q's.
func (e *edge) impliesPath(t []cue.Path, q cue.Path) bool {
	qa := e.atom(q)
	if len(qa.Selectors()) == 0 {
		return true
	}
	for _, x := range t {
		if pathHasPrefix(e.atom(x), qa) {
			return true
		}
	}
	return false
}

func (e *edge) impliesTerm(t, u []cue.Path) bool {
	for _, q := range u {
		if !e.impliesPath(t, q) {
			return false
		}
	}
	return true
}

// implies reports whether x implies y: every term of x implies a term of y.
func (e *edge) implies(x, y dnf) bool {
	for _, t := range x {
		if !slices.ContainsFunc(y, func(u []cue.Path) bool { return e.impliesTerm(t, u) }) {
			return false
		}
	}
	return true
}

// term is the conjunction of the presence of paths, leaving out each path
// another implies.
func (e *edge) term(paths ...cue.Path) dnf {
	var t []cue.Path
	for _, q := range paths {
		if e.impliesPath(t, q) {
			continue
		}
		t = slices.DeleteFunc(t, func(k cue.Path) bool { return e.impliesPath([]cue.Path{q}, k) })
		t = append(t, q)
	}
	return dnf{t}
}

// absorb leaves out each term that implies another.
func (e *edge) absorb(d dnf) dnf {
	var out dnf
	for _, t := range d {
		if slices.ContainsFunc(out, func(u []cue.Path) bool { return e.impliesTerm(t, u) }) {
			continue
		}
		out = slices.DeleteFunc(out, func(u []cue.Path) bool { return e.impliesTerm(u, t) })
		out = append(out, t)
	}
	return out
}

func (e *edge) or(x, y dnf) dnf { return e.absorb(append(slices.Clone(x), y...)) }

func (e *edge) and(x, y dnf) dnf {
	var out dnf
	for _, t := range x {
		for _, u := range y {
			out = append(out, e.term(append(slices.Clone(t), u...)...)[0])
		}
	}
	return e.absorb(out)
}

// beneath is d where has holds: each term without the paths has implies. Nil
// if d holds there always.
func (e *edge) beneath(d, has dnf) dnf {
	var out dnf
	for _, t := range d {
		var kept []cue.Path
		for _, q := range t {
			if !e.implies(has, dnf{{q}}) {
				kept = append(kept, q)
			}
		}
		if len(kept) == 0 {
			return nil
		}
		out = append(out, kept)
	}
	return out
}

// beneathTerm is the presence tests a leaf written under present needs
// inside structs written under enc.
func (e *edge) beneathTerm(present []cue.Path, enc dnf) []cue.Path {
	var out []cue.Path
	for _, q := range present {
		if !e.implies(enc, dnf{{q}}) {
			out = append(out, q)
		}
	}
	return out
}

// A presenceTest is a presence test, or with present unset an absence test, and
// a cube the conjunction of them.
type presenceTest struct {
	path    cue.Path
	present bool
}

type cube []presenceTest

// disjoint splits d into cubes that hold for the same instances and never
// two for one: each term, less the terms before it.
func (e *edge) disjoint(d dnf) []cube {
	var out []cube
	for i, t := range d {
		var c cube
		for _, q := range t {
			c = append(c, presenceTest{q, true})
		}
		cubes := []cube{c}
		for _, u := range d[:i] {
			var next []cube
			for _, c := range cubes {
				next = append(next, e.subtract(c, u)...)
			}
			cubes = next
		}
		out = append(out, cubes...)
	}
	return out
}

// subtract returns the cubes that cover what c covers outside the term u.
func (e *edge) subtract(c cube, u []cue.Path) []cube {
	var pos []cue.Path
	for _, l := range c {
		if l.present {
			pos = append(pos, l.path)
			continue
		}
		for _, x := range u {
			if e.impliesPath([]cue.Path{x}, l.path) {
				return []cube{c} // c has a path absent that u needs
			}
		}
	}
	var out []cube
	cur := slices.Clone(c)
	for _, x := range u {
		if e.impliesPath(pos, x) {
			continue
		}
		out = append(out, append(slices.Clone(cur), presenceTest{x, false}))
		cur = append(cur, presenceTest{x, true})
		pos = append(pos, x)
	}
	return out
}

// cubeExpr is c as a CUE condition.
func (e *edge) cubeExpr(p token.Pos, c cube) ast.Expr {
	var x ast.Expr
	for _, l := range c {
		y := e.absent(p, l.path)
		if l.present {
			y = e.present(p, l.path)
		}
		if x == nil {
			x = y
			continue
		}
		x = at(p, ast.NewBinExpr(token.LAND, x, y))
	}
	return x
}

// notExpr is the negation of d: in every term, some path is absent.
func (e *edge) notExpr(p token.Pos, d dnf) ast.Expr {
	var x ast.Expr
	for _, t := range d {
		var y ast.Expr
		for _, q := range t {
			if y == nil {
				y = e.absent(p, q)
				continue
			}
			y = at(p, ast.NewBinExpr(token.LOR, y, e.absent(p, q)))
		}
		if len(t) > 1 && len(d) > 1 {
			y = at(p, &ast.ParenExpr{X: y})
		}
		x = and(x, y)
	}
	return x
}
