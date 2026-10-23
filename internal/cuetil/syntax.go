package cuetil

import (
	"sort"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"

	"github.com/sdboyer/thema/internal/cueview"
)

// LensSyntax is the syntax of a lens, read from the lens as evaluated (see
// cueview.Tree), for the checks of what a lens builds. Its answers depend only on
// what the lens is, not on how its value was constructed.
type LensSyntax struct {
	t       *cueview.Tree
	inputs  []*ast.Field
	result  []cueview.Conjunct
	lacunas []cueview.Conjunct
}

// ParseLens reads the syntax of lens, a value with input, result and lacunas
// fields.
func ParseLens(lens cue.Value) (*LensSyntax, error) {
	t, err := cueview.NewTree(lens)
	if err != nil {
		return nil, err
	}
	ls := &LensSyntax{t: t}
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
		for _, fd := range cueview.FieldDecls(s.Elts, exprs) {
			f := fd.Field
			name, _, err := ast.LabelName(f.Label)
			if err != nil {
				continue
			}
			switch name {
			case "input":
				ls.inputs = append(ls.inputs, f)
			case "result":
				ls.result = append(ls.result, t.Conjuncts(f.Value)...)
			case "lacunas":
				ls.lacunas = append(ls.lacunas, t.Conjuncts(f.Value)...)
			}
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

// Result returns how the lens builds its result.
func (ls *LensSyntax) Result() *Built {
	return ls.classify(ls.result, false)
}

// Built describes how a lens produces one field of its result (or the result
// itself), from the lens's syntax: whether the lens passes input data through,
// or builds it.
//
// A value is passed through if every conjunct of it is a reference that
// resolves into the lens's input, however indirectly: through lets, aliases,
// self, or other fields. Anything else is built, including a reference to a
// hidden helper field. Fields a built struct has that its syntax doesn't name
// come from its passed-through conjuncts (e.g. n: input.n & {x: 1}), unless the
// struct also has fields whose labels its syntax can't name (dynamic labels,
// lists built by comprehensions, selections the syntax can't follow), in which
// case they count as built.
//
// A builtin call other than close() is taken to produce no fields of its own,
// as with thema's result: struct.MinFields(0). A builtin that does produce a
// struct (e.g. json.Unmarshal) is not seen as building its fields.
//
// A nil *Built describes a value about which nothing is known: all of it counts
// as built.
type Built struct {
	passThrough bool
	// conditional is true if the field is declared only inside comprehensions,
	// and so may not exist until data makes them fire.
	conditional bool
	// open is true if the value may have built fields not in children.
	open     bool
	children map[string]*Built
}

// child returns how the field name of b is produced, and whether it is passed
// through.
func (b *Built) child(name string) (*Built, bool) {
	if b == nil {
		return nil, false
	}
	if c, ok := b.children[name]; ok {
		return c, c.passThrough
	}
	if b.open {
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

func (ls *LensSyntax) classify(conjs []cueview.Conjunct, conditional bool) *Built {
	b := &Built{passThrough: len(conjs) > 0, conditional: conditional, children: map[string]*Built{}}
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

// collectFields returns the declarations of each field the conjuncts that are
// not from input declare, by name, with the names in order of declaration. It
// records in b whether all of conjs are from input, and whether they may give
// fields the syntax can't name.
func (ls *LensSyntax) collectFields(conjs []cueview.Conjunct, b *Built) (map[string][]cueview.FieldDecl, []string) {
	fields := map[string][]cueview.FieldDecl{}
	var order []string
	embedded := ls.embedded(b)
	for _, c := range conjs {
		if ls.fromInput(c) {
			continue
		}
		b.passThrough = false
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
			if strings.HasPrefix(name, "#") || strings.HasPrefix(name, "_#") {
				// Definitions are not data.
				continue
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
func (ls *LensSyntax) embedded(b *Built) func(ast.Expr) []ast.Expr {
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

// UndeclaredConditionalFields returns the paths of fields that b's lens builds
// inside comprehensions and that sch does not declare, by the rules of
// [DeclaresPath]. Such fields may not exist until data makes the comprehension
// fire, so they are found from the lens's syntax. A path is reported only at its
// first undeclared step.
//
// Hidden fields follow closedness: one sch does not declare is the lens's own
// scratch space and is not checked; beneath one it declares, the lens may build
// only what it declares. Hidden labels are matched to sch's by name, which is
// exact where the lens and its schemas are in the same package.
func UndeclaredConditionalFields(ctx *cue.Context, b *Built, sch cue.Value) []string {
	var found []string
	// schs are the schemas that declare path: where sch is a disjunction, any of
	// its alternatives may.
	var walk func(b *Built, schs []cue.Value, path []string)
	walk = func(b *Built, schs []cue.Value, path []string) {
		for _, name := range sortedKeys(b.children) {
			c := b.children[name]
			if c.passThrough {
				continue
			}
			hidden := strings.HasPrefix(name, "_")
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
						v, ok = declaredChild(ctx, alt, cue.Str(name))
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
	walk(b, []cue.Value{sch}, nil)
	return found
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

func sortedKeys(m map[string]*Built) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
