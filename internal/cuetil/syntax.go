package cuetil

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/internal/cueview"
)

// LensSyntax is the syntax of a lens, read from the lens as evaluated (see
// cueview.Tree), for the checks of where a lens's result comes from. Its answers depend only on
// what the lens is, not on how its value was constructed.
type LensSyntax struct {
	v            cue.Value
	t            *cueview.Tree
	from, to     string
	own          []*ast.StructLit
	inputs       []*ast.Field
	resultFields []*ast.Field
	result       []cueview.Conjunct
	lacunas      []cueview.Conjunct
}

// LensParam is the name of the field a lens binds an instance of version
// major.minor to: v0_0 for 0.0. A lens's source field and its result field
// are both named this way, after the versions its edge names.
func LensParam(major, minor uint) string {
	return fmt.Sprintf("v%d_%d", major, minor)
}

// ParseLens reads the syntax of lens, a value whose source field is named
// from, whose result field is named to (both as [LensParam] names them), and
// whose lacunas field is lacunas.
func ParseLens(lens cue.Value, from, to string) (*LensSyntax, error) {
	return parseLens(lens, from, to, cueview.NewTree)
}

func parseLens(lens cue.Value, from, to string, tree func(cue.Value) (*cueview.Tree, error)) (*LensSyntax, error) {
	t, err := tree(lens)
	if err != nil {
		return nil, err
	}
	ls := &LensSyntax{v: lens, t: t, from: from, to: to}
	exprs := func(x ast.Expr) []ast.Expr {
		var out []ast.Expr
		for _, c := range t.Conjuncts(x) {
			out = append(out, c.Expr)
		}
		return out
	}
	for _, c := range t.Root() {
		s, ok := c.Expr.(*ast.StructLit)
		if !ok {
			continue
		}
		// Through embeddings too: a lens that is a definition is exported
		// wrapped, as {_#def, _#def: {...}}.
		own := false
		for _, fd := range cueview.FieldDecls(s.Elts, exprs) {
			f := fd.Field
			name, _, err := ast.LabelName(f.Label)
			if err != nil {
				continue
			}
			switch name {
			case from:
				ls.inputs = append(ls.inputs, f)
				own = true
			case to:
				ls.resultFields = append(ls.resultFields, f)
				ls.result = append(ls.result, t.Conjuncts(f.Value)...)
				own = true
			case "lacunas":
				ls.lacunas = append(ls.lacunas, t.Conjuncts(f.Value)...)
			}
		}
		if own {
			ls.own = append(ls.own, s)
		}
	}
	return ls, nil
}

// LacunasConditional reports whether the lens's lacunas list is built with a
// comprehension element (e.g. [if x {...}]), however the list is reached.
//
// Such a list's elements depend on evaluation, and a comprehension whose
// condition can't yet be decided may still evaluate to no elements, so this is
// determined from the syntax rather than the list's length.
func (ls *LensSyntax) LacunasConditional() bool {
	for _, c := range ls.lacunas {
		if listHasComprehension(c.Expr) {
			return true
		}
	}
	return false
}

// Result returns where the lens's result, and each of its fields, comes from.
func (ls *LensSyntax) Result() *LensField {
	return ls.classify(ls.result, false)
}

// Tree returns the syntax tree the lens was read from, for walks that need more
// of a lens than [LensField] records: the conjuncts of each field, the
// comprehensions they are declared in, and the expressions computed fields
// are built from.
func (ls *LensSyntax) Tree() *cueview.Tree {
	return ls.t
}

// ResultConjuncts returns the conjuncts of the lens's result field. Besides
// what the author wrote, they include thema's own constraint on a result
// (struct.MinFields(0)), which a walk recognizes as a builtin call at the
// root.
func (ls *LensSyntax) ResultConjuncts() []cueview.Conjunct {
	return ls.result
}

// LacunaConjuncts returns the conjuncts of the lens's lacunas field: the
// author's list, and thema's [...Lacuna], a list literal with only an ellipsis.
func (ls *LensSyntax) LacunaConjuncts() []cueview.Conjunct {
	return ls.lacunas
}

// FromInput reports whether the conjunct c is part of the lens's input: data
// the lens copies, however indirectly it reached the conjunct (lets, aliases,
// self, other fields). This is the one definition of "copied" (see [Origin]);
// every check of a lens shares it.
func (ls *LensSyntax) FromInput(c cueview.Conjunct) bool {
	return ls.fromInput(c)
}

// InputFields returns the declarations of the lens's input field, thema's and
// the author's, so that a walk can tell a reference into one of them from any
// other.
func (ls *LensSyntax) InputFields() []*ast.Field {
	return ls.inputs
}

// ResultFields returns the declarations of the lens's result field, thema's
// and the author's, so that a walk can recognize a reference to the result
// (as a lacuna's targetFields value makes) for what it is.
func (ls *LensSyntax) ResultFields() []*ast.Field {
	return ls.resultFields
}

// Origin is where the value of a field of a lens result comes from.
type Origin int

const (
	// Computed is any other value: an expression (input.a + 1, an
	// interpolation, a builtin call), or a mix of origins (input.n & {x: 1}).
	Computed Origin = iota
	// Copied is input data, unchanged: a reference that resolves into the
	// lens's input, however indirectly (lets, aliases, self, other fields).
	Copied
	// Constant is a literal the lens writes: a scalar, or a struct or list
	// literal without comprehensions, whose own fields and elements have their
	// own origins.
	Constant
)

func (o Origin) String() string {
	switch o {
	case Copied:
		return "copied"
	case Constant:
		return "constant"
	}
	return "computed"
}

// LensField describes where one field of a lens result (or the result itself)
// comes from, read from the lens's syntax: see [Origin]. A reference to
// anything other than input, such as a hidden helper field, is followed to what
// it refers to. Fields a struct has that its syntax doesn't name come from its
// copied conjuncts (e.g. n: input.n & {x: 1}), unless the struct also has
// fields whose labels its syntax can't name (dynamic labels, lists built by
// comprehensions, selections the syntax can't follow), in which case they are
// taken to be computed.
//
// A builtin call other than close() is taken to produce no fields of its own,
// as with thema's result: struct.MinFields(0). A builtin that does produce a
// struct (e.g. json.Unmarshal) is not seen as producing its fields (#45).
//
// A nil *LensField describes a value about which nothing is known: all of it is
// taken to be computed.
type LensField struct {
	Origin Origin
	// conditional is true if the field is declared only inside comprehensions,
	// and so may not exist until data makes them fire.
	conditional bool
	// open is true if the value may have computed fields not in children.
	open     bool
	children map[string]*LensField
}

// child returns where the field name of f comes from, and whether it is
// copied.
func (f *LensField) child(name string) (*LensField, bool) {
	if f == nil {
		return nil, false
	}
	if c, ok := f.children[name]; ok {
		return c, c.Origin == Copied
	}
	if f.open {
		return nil, false
	}
	return nil, true
}

func (ls *LensSyntax) fromInput(c cueview.Conjunct) bool {
	for _, in := range ls.inputs {
		if c.Via != nil && ls.t.Within(c.Via, in) {
			return true
		}
	}
	return false
}

func (ls *LensSyntax) classify(conjs []cueview.Conjunct, conditional bool) *LensField {
	b := &LensField{conditional: conditional, children: map[string]*LensField{}}
	b.Origin = ls.origin(conjs)
	fields, order := ls.collectFields(conjs, b)
	for _, name := range order {
		var cs []cueview.Conjunct
		cond := true
		for _, fd := range fields[name] {
			cs = append(cs, ls.t.Conjuncts(fd.Field.Value)...)
			cond = cond && fd.Conditional
		}
		b.children[name] = ls.classify(cs, conditional || cond)
	}
	return b
}

// origin returns where a value with the conjuncts conjs comes from.
func (ls *LensSyntax) origin(conjs []cueview.Conjunct) Origin {
	copied, written := 0, 0
	for _, c := range conjs {
		switch {
		case ls.fromInput(c):
			copied++
		case isLiteral(c.Expr):
			written++
		default:
			return Computed
		}
	}
	switch {
	case copied > 0 && written == 0:
		return Copied
	case written > 0 && copied == 0:
		return Constant
	}
	return Computed
}

// isLiteral reports whether x is a literal, or a predeclared identifier that
// adds nothing to a value (_, or a type such as int, which only constrains).
func isLiteral(x ast.Expr) bool {
	switch e := x.(type) {
	case *ast.BasicLit:
		return true
	case *ast.StructLit:
		// Which fields a struct with a comprehension has is computed.
		for _, d := range e.Elts {
			if _, ok := d.(*ast.Comprehension); ok {
				return false
			}
		}
		return true
	case *ast.ListLit:
		return !listHasComprehension(e)
	case *ast.UnaryExpr:
		// A negative number.
		_, ok := e.X.(*ast.BasicLit)
		return ok
	case *ast.Ident:
		return !opens(e)
	}
	return false
}

// collectFields returns the declarations of each field the conjuncts that are
// not from input declare, by name, with the names in order of declaration. It
// records in b whether they may give fields the syntax can't name.
func (ls *LensSyntax) collectFields(conjs []cueview.Conjunct, b *LensField) (map[string][]cueview.FieldDecl, []string) {
	fields := map[string][]cueview.FieldDecl{}
	var order []string
	embedded := ls.embedded(b)
	for _, c := range conjs {
		if ls.fromInput(c) {
			continue
		}
		s, ok := c.Expr.(*ast.StructLit)
		if !ok {
			b.open = b.open || opens(c.Expr)
			continue
		}
		for _, fd := range cueview.FieldDecls(s.Elts, embedded) {
			name, isIdent, err := ast.LabelName(fd.Field.Label)
			if err != nil || (!isIdent && name == "") {
				b.open = true
				continue
			}
			if isIdent && (strings.HasPrefix(name, "#") || strings.HasPrefix(name, "_#")) {
				// Definitions are not data.
				continue
			}
			if !isIdent {
				// A quoted label is data, whatever it starts with.
				name = dataKey(name)
			}
			if _, ok := fields[name]; !ok {
				order = append(order, name)
			}
			fields[name] = append(fields[name], fd)
		}
	}
	return fields, order
}

// embedded returns the conjuncts of an embedded expression that are not from
// input, recording in b whether they may give fields the syntax can't name.
func (ls *LensSyntax) embedded(b *LensField) func(ast.Expr) []ast.Expr {
	return func(x ast.Expr) []ast.Expr {
		var out []ast.Expr
		for _, c := range ls.t.Conjuncts(x) {
			if ls.fromInput(c) {
				continue
			}
			if _, ok := c.Expr.(*ast.StructLit); !ok {
				b.open = b.open || opens(c.Expr)
			}
			out = append(out, c.Expr)
		}
		return out
	}
}

// opens reports whether a conjunct that isn't a struct literal may give its
// value fields the syntax can't name: a selection or variable the syntax
// couldn't follow, or a list built with a comprehension.
func opens(x ast.Expr) bool {
	switch e := x.(type) {
	case *ast.SelectorExpr, *ast.IndexExpr:
		return true
	case *ast.Ident:
		// A comprehension variable (resolved to the clause's identifier) could be
		// anything; predeclared identifiers (_, int, string, ...) build nothing.
		_, ok := e.Node.(*ast.Ident)
		return ok
	case *ast.ListLit:
		return listHasComprehension(e)
	}
	return false
}

func listHasComprehension(x ast.Expr) bool {
	l, ok := x.(*ast.ListLit)
	if !ok {
		return false
	}
	for _, elt := range l.Elts {
		if _, ok := elt.(*ast.Comprehension); ok {
			return true
		}
	}
	return false
}

// UndeclaredConditionalFields returns the paths of fields that f's lens writes
// inside comprehensions (that is, doesn't copy) and that sch does not declare,
// by the rules of [DeclaresPath]. Such fields may not exist until data makes the
// comprehension fire, so they are found from the lens's syntax. A path is
// reported only at its first undeclared step.
//
// Hidden fields follow closedness: one sch does not declare is the lens's own
// scratch space and is not checked; beneath one it declares, the lens may write
// only what it declares. Hidden labels are matched to sch's by name, which is
// exact where the lens and its schemas are in the same package.
func UndeclaredConditionalFields(ctx *cue.Context, f *LensField, sch cue.Value) []string {
	var found []string
	// schs are the schemas that declare path: where sch is a disjunction, any of
	// its alternatives may.
	var walk func(b *LensField, schs []cue.Value, path []string)
	walk = func(b *LensField, schs []cue.Value, path []string) {
		for _, name := range sortedKeys(b.children) {
			c := b.children[name]
			if c.Origin == Copied {
				continue
			}
			hidden := strings.HasPrefix(name, "_")
			if unq, err := strconv.Unquote(name); err == nil {
				// A data field whose name a hidden field or a definition
				// would otherwise share (see dataKey).
				name, hidden = unq, false
			}
			step := name
			if !hidden {
				step = cue.MakePath(cue.Str(name)).String()
			}
			var next []cue.Value
			everything := false
			for _, s := range schs {
				for _, alt := range alternatives(s, 0) {
					if alt.IncompleteKind() == cue.TopKind {
						// _ declares everything beneath it.
						everything = true
						continue
					}
					var v cue.Value
					var ok bool
					if hidden {
						v, ok = declaredHidden(alt, name)
					} else {
						v, _, ok = DeclaredChild(ctx, alt, cue.Str(name))
					}
					if ok {
						next = append(next, v)
					}
				}
			}
			p := append(append([]string{}, path...), step)
			switch {
			case everything:
			case len(next) > 0:
				walk(c, next, p)
			case !hidden && c.conditional:
				found = append(found, strings.Join(p, "."))
			}
		}
	}
	walk(f, []cue.Value{sch}, nil)
	return found
}

// dataKey is the key of the data field name among a LensField's children.
// A name starting with _ or # is quoted, so that a data field "_id" and a
// hidden field _id, which have the same name, have different keys; other
// names are their own key.
func dataKey(name string) string {
	if strings.HasPrefix(name, "_") || strings.HasPrefix(name, "#") {
		return strconv.Quote(name)
	}
	return name
}

// declaredHidden returns the value of the hidden field name that sch declares,
// if it does. Hidden labels are matched by name: see
// [UndeclaredConditionalFields].
func declaredHidden(sch cue.Value, name string) (cue.Value, bool) {
	iter, err := sch.Fields(cue.Hidden(true), cue.Optional(true))
	if err != nil {
		return cue.Value{}, false
	}
	for iter.Next() {
		// String marks an optional or required field (_h?), which a hidden name
		// can't otherwise contain.
		if s := iter.Selector(); s.LabelType() == cue.HiddenLabel && strings.TrimRight(s.String(), "?!") == name {
			return iter.Value(), true
		}
	}
	return cue.Value{}, false
}

func sortedKeys(m map[string]*LensField) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// InputReads returns the paths within the lens's input that its result reads:
// every reference into the input the result's syntax makes, directly or
// through lets, aliases and other fields, such as hidden helpers. A read
// through a comprehension's value (for x in input.l {x.a}) is of the list's
// open element, cue.AnyIndex (l[_].a); iterating a list is not itself a read
// of it. A selection the syntax cannot follow, such as a computed index, reads
// the whole value it selects from. The empty path is the whole input.
//
// Like [LensSyntax.Result], the answer depends only on what the lens is: it is
// read from the lens's syntax, never from data.
func (ls *LensSyntax) InputReads() []cue.Path {
	r := &inputReads{ls: ls, walked: map[ast.Node]bool{}, visiting: map[ast.Node]bool{}}
	for _, f := range ls.resultFields {
		r.walk(f.Value)
	}
	return r.out
}

// A Copy is a field of a lens's result whose contents come from one value of
// the lens's input as a whole: the field at Result holds the input's value at
// Input, with anything the lens writes beneath it.
type Copy struct {
	Result, Input cue.Path
}

// Copies returns the fields of the lens's result whose contents come from one
// value of its input as a whole: a move (info: v0_0.meta), an embedding
// (info: {v0_0.meta, x: 1}), or either with mappings beneath it (info:
// v0_0.meta & {x: v0_0.y}). The value is a reference into the input, however
// indirect (lets, aliases, other fields), and struct literals: every
// reference, in every declaration of the field, refers to the same input
// path. A field assembled from parts of several input values, or computed, is
// not a copy. Fields are followed through struct literals, beneath copies
// too; lists, and the values a comprehension over a list yields, are not
// followed.
//
// Like [LensSyntax.InputReads], the answer depends only on what the lens is.
func (ls *LensSyntax) Copies() []Copy {
	var out []Copy
	var walk func(values []ast.Expr, at cue.Path, root bool)
	walk = func(values []ast.Expr, at cue.Path, root bool) {
		var refs []cue.Path
		other := false
		fields := map[string][]ast.Expr{}
		var order []string
		var visit func(x ast.Expr)
		var decls func(elts []ast.Decl)
		visit = func(x ast.Expr) {
			switch e := x.(type) {
			case *ast.BinaryExpr:
				if e.Op == token.AND {
					visit(e.X)
					visit(e.Y)
					return
				}
			case *ast.ParenExpr:
				visit(e.X)
				return
			}
			r := &inputReads{ls: ls, walked: map[ast.Node]bool{}, visiting: map[ast.Node]bool{}}
			if p, ok, exact := r.resolve(x); ok {
				if exact {
					refs = append(refs, p)
				} else {
					other = true
				}
				return
			}
			for _, c := range ls.t.Conjuncts(x) {
				if ls.fromInput(c) {
					other = true
					continue
				}
				switch e := c.Expr.(type) {
				case *ast.StructLit:
					decls(e.Elts)
				case *ast.Ident:
					// _, or a type, which adds no data of its own.
					other = other || !isLiteral(e)
				default:
					other = true
				}
			}
		}
		decls = func(elts []ast.Decl) {
			for _, d := range elts {
				switch e := d.(type) {
				case *ast.Field:
					name, isIdent, err := ast.LabelName(e.Label)
					if err != nil || (isIdent && strings.HasPrefix(name, "_")) || (isIdent && strings.HasPrefix(name, "#")) {
						// Not data: a dynamic label, or a hidden field or definition.
						continue
					}
					if _, ok := fields[name]; !ok {
						order = append(order, name)
					}
					fields[name] = append(fields[name], e.Value)
				case *ast.Comprehension:
					if s, ok := e.Value.(*ast.StructLit); ok {
						decls(s.Elts)
					}
				case *ast.EmbedDecl:
					visit(e.Expr)
				case ast.Expr:
					// An embedding beside a comprehension (see FieldDecls).
					visit(e)
				}
			}
		}
		for _, v := range values {
			visit(v)
		}
		if !root && !other && len(refs) > 0 {
			same := true
			for _, r := range refs[1:] {
				same = same && r.String() == refs[0].String()
			}
			if same {
				out = append(out, Copy{Result: at, Input: refs[0]})
			}
		}
		for _, name := range order {
			walk(fields[name], appendPath(at, cue.Str(name)), false)
		}
	}
	var roots []ast.Expr
	for _, f := range ls.resultFields {
		roots = append(roots, f.Value)
	}
	walk(roots, cue.MakePath(), true)
	return out
}

// A Reader is a field of a lens's result that reads a path of the lens's
// input: it copies the value there, or computes from it.
type Reader struct {
	Result, Input cue.Path
	// arms are the comprehensions whose bodies declare the field, or a field
	// it is beneath, outermost first: the field reads Input only for an input
	// where their clauses hold.
	arms []*ast.Comprehension
}

// Readers returns, for each path of its input the lens's result reads, the
// fields of the result that read it: the fields whose origin is copied or
// computed (see [LensField]), with their reads resolved as [LensSyntax.InputReads]
// resolves them. Fields are followed through struct literals, a
// comprehension's fields included; a value that is not a struct literal, such
// as a list or a reference to a helper struct, is one reader of everything it
// reads, and so is a field with a dynamic label, of what its label and value
// read, at the struct it is in. What an if clause reads is never read by a
// reader, wherever the clause is: a condition decides whether fields or
// elements exist, and puts none of its data in them. Nor does what a lacuna
// reads, or a hidden field that no field of the result refers to, have a
// reader.
//
// A field declared in a comprehension's body reads only where the
// comprehension fires; [LensSyntax.Firing] says which readers read for an
// input. Like [LensSyntax.InputReads], the answer depends only on what the
// lens is.
func (ls *LensSyntax) Readers() []Reader {
	out, _ := ls.readers()
	return out
}

// readers returns Readers, and for each arm a reader is in, the result path
// whose struct its body declares fields in.
func (ls *LensSyntax) readers() ([]Reader, map[*ast.Comprehension]cue.Path) {
	var out []Reader
	armAt := map[*ast.Comprehension]cue.Path{}
	// A declaration of a field, under the arms it is in.
	type decl struct {
		x    ast.Expr
		arms []*ast.Comprehension
	}
	var walk func(values []decl, at cue.Path)
	walk = func(values []decl, at cue.Path) {
		fields := map[string][]decl{}
		var order []string
		read := func(arms []*ast.Comprehension, ps ...cue.Path) {
			for _, p := range ps {
				out = append(out, Reader{Result: at, Input: p, arms: arms})
			}
		}
		var visit func(x ast.Expr, arms []*ast.Comprehension)
		var decls func(elts []ast.Decl, arms []*ast.Comprehension)
		visit = func(x ast.Expr, arms []*ast.Comprehension) {
			switch e := x.(type) {
			case *ast.BinaryExpr:
				if e.Op == token.AND {
					visit(e.X, arms)
					visit(e.Y, arms)
					return
				}
			case *ast.ParenExpr:
				visit(e.X, arms)
				return
			case *ast.StructLit:
				decls(e.Elts, arms)
				return
			}
			r := ls.uses()
			if p, ok, _ := r.resolve(x); ok {
				read(arms, p)
			}
			// What resolving walked: the reads of whatever x refers to that is
			// not a chain into the input.
			read(arms, r.out...)
		}
		decls = func(elts []ast.Decl, arms []*ast.Comprehension) {
			for _, d := range elts {
				switch e := d.(type) {
				case *ast.Field:
					name, isIdent, err := ast.LabelName(e.Label)
					if err != nil {
						// A dynamic label, or a pattern: the struct it is in reads
						// what it reads.
						r := ls.uses()
						r.walk(e)
						read(arms, r.out...)
						continue
					}
					if isIdent && (strings.HasPrefix(name, "_") || strings.HasPrefix(name, "#")) {
						// Not data: a hidden field or definition.
						continue
					}
					if _, ok := fields[name]; !ok {
						order = append(order, name)
					}
					fields[name] = append(fields[name], decl{e.Value, arms})
				case *ast.Comprehension:
					if s, ok := e.Value.(*ast.StructLit); ok {
						armAt[e] = at
						decls(s.Elts, append(append([]*ast.Comprehension(nil), arms...), e))
					}
				case *ast.EmbedDecl:
					visit(e.Expr, arms)
				case ast.Expr:
					// An embedding beside a comprehension (see FieldDecls).
					visit(e, arms)
				}
			}
		}
		for _, v := range values {
			visit(v.x, v.arms)
		}
		for _, name := range order {
			walk(fields[name], appendPath(at, cue.Str(name)))
		}
	}
	var roots []decl
	for _, f := range ls.resultFields {
		roots = append(roots, decl{x: f.Value})
	}
	walk(roots, cue.MakePath())
	return out, armAt
}

// Firing returns the readers (see [LensSyntax.Readers]) that read for input,
// an instance the lens's input field takes: a reader declared in the body of a
// comprehension reads only where the comprehension fired, every comprehension
// around it included. A comprehension over a list fired where it produced its
// body for at least one element. A reader returned need not have been written:
// a field it is beneath may not have been.
//
// To find which comprehensions fired, Firing evaluates the lens's syntax, as
// exported self-contained, on input, with a marker in each body. So, like the
// rest of LensSyntax, the answer depends only on what the lens is (and on
// input), not on how its value was constructed.
func (ls *LensSyntax) Firing(ctx *cue.Context, input cue.Value) ([]Reader, error) {
	readers := ls.Readers()
	conditional := false
	for _, r := range readers {
		conditional = conditional || len(r.arms) > 0
	}
	if !conditional {
		return readers, nil
	}
	// The exported syntax may refer to packages the lens's source imported,
	// which a probe compiled on its own can't load; exported self-contained,
	// their content is in lets.
	sc, err := parseLens(ls.v, ls.from, ls.to, cueview.NewSelfContainedTree)
	if err != nil {
		return nil, err
	}
	readers, armAt := sc.readers()
	fired, err := sc.fired(ctx, input, armAt)
	if err != nil {
		return nil, err
	}
	var out []Reader
	for _, r := range readers {
		all := true
		for _, a := range r.arms {
			all = all && fired[a]
		}
		if all {
			out = append(out, Reader{Result: r.Result, Input: r.Input})
		}
	}
	return out, nil
}

// fired evaluates the lens on input, and returns the arms of armAt (each with
// the result path whose struct its body declares fields in) that fired.
func (ls *LensSyntax) fired(ctx *cue.Context, input cue.Value, armAt map[*ast.Comprehension]cue.Path) (map[*ast.Comprehension]bool, error) {
	// Mark each arm, for as long as it takes to write the lens out, in the
	// order the arms are declared.
	var arms []*ast.Comprehension
	ast.Walk(ls.t.File, func(n ast.Node) bool {
		if c, ok := n.(*ast.Comprehension); ok {
			if _, ok := armAt[c]; ok {
				arms = append(arms, c)
			}
		}
		return true
	}, nil)
	marker := func(i int) string { return fmt.Sprintf("_thema_fired%d", i) }
	elts := make([][]ast.Decl, len(arms))
	for i, a := range arms {
		s := a.Value.(*ast.StructLit)
		elts[i] = s.Elts
		s.Elts = append(append([]ast.Decl(nil), s.Elts...), &ast.Field{Label: ast.NewIdent(marker(i)), Value: ast.NewBool(true)})
	}
	src, err := cueview.Text(ls.probe())
	for i, a := range arms {
		a.Value.(*ast.StructLit).Elts = elts[i]
	}
	if err != nil {
		return nil, err
	}
	lens := ctx.CompileString(src)
	if err := lens.Err(); err != nil {
		return nil, fmt.Errorf("evaluating the lens's syntax: %w", err)
	}
	result := lens.FillPath(cue.MakePath(cue.Str(ls.from)), input).LookupPath(cue.MakePath(cue.Str(ls.to)))
	out := map[*ast.Comprehension]bool{}
	for i, a := range arms {
		m := result.LookupPath(cue.MakePath(append(armAt[a].Selectors(), cue.Hid(marker(i), "_"))...))
		if m.Exists() && m.Err() == nil {
			out[a] = true
		}
	}
	return out, nil
}

// probe returns the lens's own syntax as a file to evaluate on its own: the
// declarations of its input and result fields, and those they refer to, with
// the lets and imports they need. What thema's Lens adds to a lens is left
// out: the export can't always write its constraints as CUE that evaluates.
func (ls *LensSyntax) probe() *ast.File {
	f := ls.t.File
	// body is the lens's own declarations: the file's own, where the file is
	// the lens's struct, or else the structs among its conjuncts that declare
	// its fields, embedded.
	var body []ast.Decl
	whole := false
	for _, s := range ls.own {
		whole = whole || (len(s.Elts) > 0 && len(f.Decls) > 0 && &s.Elts[0] == &f.Decls[0])
	}
	if whole {
		for _, d := range f.Decls {
			switch d.(type) {
			case *ast.ImportDecl, *ast.LetClause:
			default:
				body = append(body, d)
			}
		}
	} else {
		var x ast.Expr
		for _, s := range ls.own {
			if x == nil {
				x = s
				continue
			}
			x = &ast.BinaryExpr{X: x, Op: token.AND, Y: s}
		}
		if x != nil {
			body = append(body, &ast.EmbedDecl{Expr: x})
		}
	}

	// The file's lets and imports the body refers to, however indirectly:
	// CUE rejects a let or an import that nothing refers to. Each is known by
	// the name it binds, as the file is compiled again from its text: the
	// exporter resolves a reference to an import to a spec of its own, not to
	// the file's.
	used := map[ast.Node]bool{}
	names := map[string]bool{}
	var refs func(n ast.Node)
	refs = func(n ast.Node) {
		ast.Walk(n, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || id.Node == nil || used[id.Node] {
				return true
			}
			switch d := id.Node.(type) {
			case *ast.LetClause:
				used[d] = true
				names[id.Name] = true
				refs(d.Expr)
			case *ast.ImportSpec:
				names[id.Name] = true
			}
			return true
		}, nil)
	}
	for _, d := range body {
		refs(d)
	}
	var decls []ast.Decl
	for _, d := range f.Decls {
		switch e := d.(type) {
		case *ast.ImportDecl:
			var specs []*ast.ImportSpec
			for _, sp := range e.Specs {
				if info, err := astutil.ParseImportSpec(sp); err == nil && names[info.Ident] {
					specs = append(specs, sp)
				}
			}
			if len(specs) > 0 {
				decls = append(decls, &ast.ImportDecl{Specs: specs})
			}
		case *ast.LetClause:
			if names[e.Ident.Name] {
				decls = append(decls, d)
			}
		}
	}
	return &ast.File{Decls: append(decls, body...)}
}

type inputReads struct {
	ls       *LensSyntax
	walked   map[ast.Node]bool
	visiting map[ast.Node]bool
	out      []cue.Path
	// uses is set to skip what if clauses read (see [LensSyntax.Readers]).
	uses bool
}

// uses returns an inputReads that skips what if clauses read.
func (ls *LensSyntax) uses() *inputReads {
	return &inputReads{ls: ls, walked: map[ast.Node]bool{}, visiting: map[ast.Node]bool{}, uses: true}
}

// walk records the reads of every reference within n.
func (r *inputReads) walk(n ast.Node) {
	if n == nil || r.walked[n] {
		return
	}
	r.walked[n] = true
	ast.Walk(n, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Field:
			switch x.Label.(type) {
			case *ast.Ident, *ast.BasicLit:
			default:
				// A dynamic label is computed.
				r.walk(x.Label)
			}
			r.walk(x.Value)
			return false
		case *ast.LetClause:
			// Read where it is referenced.
			return false
		case *ast.IfClause:
			return !r.uses
		case *ast.ForClause:
			// Iterating is not a read; the value variable's uses are.
			r.resolve(x.Source)
			return false
		case *ast.Ident, *ast.SelectorExpr, *ast.IndexExpr, *ast.ParenExpr:
			if p, ok, _ := r.resolve(x.(ast.Expr)); ok {
				r.out = append(r.out, p)
			}
			return false
		}
		return true
	}, nil)
}

// resolve returns the input path x reads, if x is a chain of selections rooted
// in the input. exact is false where a selection could not be followed; the
// path is then the value selected from. Whatever x refers to that is not such
// a chain is walked for its own reads.
func (r *inputReads) resolve(x ast.Expr) (p cue.Path, ok, exact bool) {
	switch e := x.(type) {
	case *ast.ParenExpr:
		return r.resolve(e.X)
	case *ast.SelectorExpr:
		base, ok, exact := r.resolve(e.X)
		if !ok || !exact {
			return base, ok, exact
		}
		name, _, err := ast.LabelName(e.Sel)
		if err != nil {
			return base, true, false
		}
		return appendPath(base, cue.Str(name)), true, true
	case *ast.IndexExpr:
		r.walk(e.Index)
		base, ok, exact := r.resolve(e.X)
		if !ok || !exact {
			return base, ok, exact
		}
		if lit, isLit := e.Index.(*ast.BasicLit); isLit {
			switch lit.Kind {
			case token.INT:
				if i, err := strconv.Atoi(lit.Value); err == nil {
					return appendPath(base, cue.Index(i)), true, true
				}
			case token.STRING:
				if s, err := literal.Unquote(lit.Value); err == nil {
					return appendPath(base, cue.Str(s)), true, true
				}
			}
		}
		return base, true, false
	case *ast.Ident:
		return r.ident(e)
	}
	r.walk(x)
	return cue.Path{}, false, false
}

func (r *inputReads) ident(id *ast.Ident) (cue.Path, bool, bool) {
	if r.visiting[id] {
		// A reference cycle.
		return cue.Path{}, false, false
	}
	r.visiting[id] = true
	defer delete(r.visiting, id)

	switch n := id.Node.(type) {
	case *ast.LetClause:
		return r.resolve(n.Expr)
	case *ast.Alias:
		return r.resolve(n.Expr)
	case *ast.Field:
		// A postfix alias of a field.
		return r.field(n)
	case ast.Expr:
		parent := r.ls.t.Parent(n)
		if fc, ok := parent.(*ast.ForClause); ok {
			if fc.Value != n {
				// A comprehension's key is a label or an index, not data.
				return cue.Path{}, false, false
			}
			base, ok, exact := r.resolve(fc.Source)
			if !ok || !exact {
				return base, ok, exact
			}
			return appendPath(base, cue.AnyIndex), true, true
		}
		if f, ok := parent.(*ast.Field); ok && f.Value == n {
			return r.field(f)
		}
		r.walk(n)
	}
	// Predeclared identifiers and imported packages read nothing.
	return cue.Path{}, false, false
}

// field returns the input path a reference to f reads: the input itself, a
// field within its declaration, or whatever f's value reads.
func (r *inputReads) field(f *ast.Field) (cue.Path, bool, bool) {
	for _, in := range r.ls.inputs {
		if f == in {
			return cue.MakePath(), true, true
		}
		if r.ls.t.Within(f, in) {
			var sels []cue.Selector
			for n := ast.Node(f); n != nil && n != in; n = r.ls.t.Parent(n) {
				if fld, ok := n.(*ast.Field); ok {
					if name, _, err := ast.LabelName(fld.Label); err == nil {
						sels = append([]cue.Selector{cue.Str(name)}, sels...)
					}
				}
			}
			return cue.MakePath(sels...), true, true
		}
	}
	return r.resolve(f.Value)
}
