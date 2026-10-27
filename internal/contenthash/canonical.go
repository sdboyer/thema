package contenthash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/internal/cueview"
	"github.com/sdboyer/thema/internal/util"
)

// The canonical form of a value is the text of its SelfContained view (see
// cueview), rewritten so that what doesn't change the value's meaning doesn't
// change the text. The rewrites are:
//
//   - Comments, positions and formatting are dropped.
//   - A reference to an imported package names it by its import path,
//     __import("strings").MinRunes, and import declarations are dropped. Only
//     builtin packages are left to import: SelfContained inlines the rest.
//     Identifiers beginning with __ are reserved in CUE, so no value's own
//     syntax can produce __import, nor __ref and the let names below.
//   - References into thema's own CUE module are kept by name, not content
//     (see kernelRef).
//   - Lets are named by their content and scope depth, so neither the author's
//     names nor the names CUE generates for references leaving the value (which
//     come from the referenced path) matter.
//   - Outside definitions, struct literals combined with & or by embedding are
//     merged into one, and fields declared more than once in a struct literal
//     into one field whose value is the & of their values. A struct literal
//     that only embeds an expression is that expression.
//   - The operands of & and | are sorted, as are a struct literal's
//     declarations; identical operands of & and identical declarations are
//     dropped.
//
// Every rewrite must keep the value's meaning: two values with different
// meanings must never have the same canonical form, while two with the same
// meaning may differ (and do, wherever CUE presents them differently and no
// rewrite here undoes that). Each rewrite is conservative where CUE's scoping or
// closedness would make it unsound, and says how.

// export returns v's SelfContained view as an expression, with its identifiers
// resolved, imports qualified, references into thema's module replaced by
// name, and comments and positions dropped. Its lets and fields keep their
// names: rewrite them with canonicalize.
func export(v cue.Value) (ast.Expr, error) {
	// An error is exported as _|_, its message in a comment, which is dropped
	// below: every error would have the same canonical form.
	if err := v.Validate(); err != nil {
		return nil, err
	}
	n, err := cueview.SelfContained(v)
	if err != nil {
		return nil, err
	}
	var f *ast.File
	switch x := n.(type) {
	case *ast.File:
		f = x
	case *ast.StructLit:
		f = &ast.File{Decls: x.Elts}
	case ast.Expr:
		f = &ast.File{Decls: []ast.Decl{&ast.EmbedDecl{Expr: x}}}
	default:
		return nil, fmt.Errorf("unexpected export of %T", n)
	}
	var errs []string
	astutil.Resolve(f, func(_ token.Pos, msg string, args ...any) {
		errs = append(errs, fmt.Sprintf(msg, args...))
	})
	if len(errs) > 0 {
		return nil, fmt.Errorf("resolving exported CUE: %s", strings.Join(errs, "; "))
	}

	f = astutil.Apply(f, nil, func(c astutil.Cursor) bool {
		switch x := c.Node().(type) {
		case *ast.Ident:
			// An import's name is chosen by CUE's export; its path is what the
			// reference means.
			if _, decl := c.Parent().Node().(*ast.ImportSpec); decl {
				break
			}
			if spec, ok := x.Node.(*ast.ImportSpec); ok {
				c.Replace(&ast.CallExpr{Fun: ast.NewIdent("__import"), Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: spec.Path.Value}}})
			}
		case *ast.LetClause:
			if ref := kernelRef(x); ref != nil {
				x.Expr = ref
			}
		}
		return true
	}).(*ast.File)
	strip(f)

	var decls []ast.Decl
	for _, d := range f.Decls {
		switch d.(type) {
		case *ast.ImportDecl, *ast.Package:
		default:
			decls = append(decls, d)
		}
	}
	if len(decls) == 1 {
		if e, ok := decls[0].(*ast.EmbedDecl); ok {
			return e.Expr, nil
		}
	}
	return &ast.StructLit{Elts: decls}, nil
}

// themaImportPath is the import path of thema's CUE package.
var themaImportPath, _, _ = strings.Cut(util.ThemaModulePath, "@")

// kernelRef returns the expression standing in for a let that CUE generated
// for a reference into thema's own CUE package, such as thema.Lacuna in a lens's
// lacunas: a call __ref("github.com/sdboyer/thema", "Lacuna"). It returns nil
// for any other let.
//
// thema's CUE is hashed by name, like a builtin package, rather than by content:
// its content is thema's, versioned with thema, and every hash that used it
// would otherwise change whenever thema's kernel did, even in an error message.
// The let is recognized by the doc comment CUE gives a generated let,
// //cue:path: "github.com/sdboyer/thema".Lacuna (see cueview.Evaluated).
// Identifiers beginning with __ are reserved in CUE, so no value's own syntax
// can produce __ref.
func kernelRef(l *ast.LetClause) ast.Expr {
	prefix := "cue:path: " + strconv.Quote(themaImportPath) + "."
	for _, cg := range ast.Comments(l) {
		text := strings.TrimSpace(cg.Text())
		if sel, ok := strings.CutPrefix(text, prefix); ok {
			return &ast.CallExpr{
				Fun:  ast.NewIdent("__ref"),
				Args: []ast.Expr{ast.NewString(themaImportPath), ast.NewString(sel)},
			}
		}
	}
	return nil
}

var posType = reflect.TypeFor[token.Pos]()

// strip drops n's comments, including comment groups standing as declarations,
// and every position in it. Formatting follows positions, so dropping them
// makes the text independent of how the value was laid out.
func strip(n ast.Node) {
	ast.Walk(n, func(n ast.Node) bool {
		ast.SetComments(n, nil)
		switch x := n.(type) {
		case *ast.File:
			x.Decls = dropComments(x.Decls)
		case *ast.StructLit:
			x.Elts = dropComments(x.Elts)
		}
		rv := reflect.ValueOf(n)
		if rv.Kind() != reflect.Pointer || rv.IsNil() {
			return true
		}
		rv = rv.Elem()
		if rv.Kind() != reflect.Struct {
			return true
		}
		for i := range rv.NumField() {
			if f := rv.Field(i); f.Type() == posType && f.CanSet() {
				f.Set(reflect.Zero(posType))
			}
		}
		return true
	}, nil)
}

func dropComments(decls []ast.Decl) []ast.Decl {
	return slices.DeleteFunc(decls, func(d ast.Decl) bool {
		_, ok := d.(*ast.CommentGroup)
		return ok
	})
}

// canonicalize rewrites x, an expression from export or built from several,
// into canonical form, and returns its text.
func canonicalize(x ast.Expr) (string, error) {
	c := &canon{named: map[*ast.LetClause]bool{}, naming: map[*ast.LetClause]bool{}}
	c.renameLets(x)
	x = c.dropDeadLets(x)
	x = c.merge(x)
	c.parents = parents(x)
	x, err := c.sort(x)
	if err != nil {
		return "", err
	}
	return text(x)
}

type canon struct {
	parents map[ast.Node]ast.Node
	// named holds the lets that have their canonical name; naming, those whose
	// name is being decided.
	named, naming map[*ast.LetClause]bool
	err           error
}

// renameLets gives every let in x a temporary name no other identifier can
// have, so that the merges below can't make one capture a reference, nor a
// reference to one be captured. Identifiers beginning with __ are reserved in
// CUE.
func (c *canon) renameLets(x ast.Expr) {
	i := 0
	ast.Walk(x, func(n ast.Node) bool {
		if l, ok := n.(*ast.LetClause); ok {
			l.Ident.Name = fmt.Sprintf("__let%d", i)
			i++
		}
		return true
	}, nil)
	ast.Walk(x, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			if l, ok := id.Node.(*ast.LetClause); ok {
				id.Name = l.Ident.Name
			}
		}
		return true
	}, nil)
}

// dropDeadLets removes lets nothing refers to. CUE rejects an author's unused
// let, so these are only the ones kernelRef made unused: those generated for
// the contents of thema's CUE that are now hashed by name.
func (c *canon) dropDeadLets(x ast.Expr) ast.Expr {
	for {
		refs := map[*ast.LetClause]int{}
		ast.Walk(x, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				if l, ok := id.Node.(*ast.LetClause); ok {
					refs[l]++
				}
			}
			return true
		}, nil)
		dead := func(d ast.Decl) bool {
			l, ok := d.(*ast.LetClause)
			return ok && refs[l] == 0
		}
		removed := false
		ast.Walk(x, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.StructLit:
				before := len(s.Elts)
				s.Elts = slices.DeleteFunc(s.Elts, dead)
				removed = removed || before != len(s.Elts)
			case *ast.Comprehension:
				before := len(s.Clauses)
				s.Clauses = slices.DeleteFunc(s.Clauses, func(cl ast.Clause) bool {
					l, ok := cl.(*ast.LetClause)
					return ok && refs[l] == 0
				})
				removed = removed || before != len(s.Clauses)
			}
			return true
		}, nil)
		if !removed {
			return x
		}
	}
}

// inDefinition returns the nodes of x that lie within the value of a
// definition (#x or _#x), such as the _#def field of the {_#def, _#def: ...}
// wrapper CUE exports a closed value as.
func inDefinition(x ast.Node) map[ast.Node]bool {
	in := map[ast.Node]bool{}
	ast.Walk(x, func(n ast.Node) bool {
		f, ok := n.(*ast.Field)
		if !ok || !isDefinition(f) {
			return true
		}
		ast.Walk(f.Value, func(n ast.Node) bool {
			in[n] = true
			return true
		}, nil)
		return true
	}, nil)
	return in
}

func isDefinition(f *ast.Field) bool {
	name, _, err := ast.LabelName(f.Label)
	return err == nil && (strings.HasPrefix(name, "#") || strings.HasPrefix(name, "_#"))
}

// merge merges, outside definitions, struct literals combined with & or by
// embedding, and fields a struct literal declares more than once.
//
// Within a definition, merging struct literals is unsound: CUE exports #A & #B
// as {_#def, _#def: {...} & {...}}, where each literal was closed on its own,
// while one literal holding both allows the fields of either.
func (c *canon) merge(x ast.Expr) ast.Expr {
	defs := inDefinition(x)
	return astutil.Apply(x, nil, func(cur astutil.Cursor) bool {
		switch n := cur.Node().(type) {
		case *ast.StructLit:
			if defs[n] {
				break
			}
			c.mergeStruct(n)
			if x := soleEmbed(n); x != nil {
				switch cur.Parent().Node().(type) {
				case *ast.Comprehension, *ast.FallbackClause:
					// Their syntax requires a struct literal.
				default:
					cur.Replace(x)
				}
			}
		case *ast.BinaryExpr:
			if n.Op == token.AND && !defs[n] {
				cur.Replace(c.mergeAnd(n))
			}
		}
		return true
	}).(ast.Expr)
}

// mergeAnd merges the struct literals among the operands of x, an & chain.
func (c *canon) mergeAnd(x ast.Expr) ast.Expr {
	var others []ast.Expr
	var structs []*ast.StructLit
	for _, op := range flatten(x, token.AND) {
		s, ok := op.(*ast.StructLit)
		if !ok || !plain(s) {
			others = append(others, op)
			continue
		}
		merged := false
		for i, acc := range structs {
			if !captures(acc, s) && !captures(s, acc) {
				structs[i] = &ast.StructLit{Elts: slices.Concat(acc.Elts, s.Elts)}
				merged = true
				break
			}
		}
		if !merged {
			structs = append(structs, s)
		}
	}
	ops := others
	for _, s := range structs {
		c.mergeStruct(s)
		ops = append(ops, s)
	}
	return ast.NewBinExpr(token.AND, ops...)
}

// soleEmbed returns the expression s embeds, if that is all s holds: {X} is X.
// (CUE's own Raw view writes {#R} as #R.) It returns nil if X refers to self,
// which in {X} denotes the literal itself.
func soleEmbed(s *ast.StructLit) ast.Expr {
	if len(s.Elts) != 1 {
		return nil
	}
	e, ok := s.Elts[0].(*ast.EmbedDecl)
	if !ok {
		return nil
	}
	for _, id := range references(e.Expr, nil) {
		if id.Name == "self" || id.Name == "__self" {
			return nil
		}
	}
	return e.Expr
}

// plain reports whether s embeds nothing. What s embeds may be a closed
// definition, or not a struct at all, and how that combines with the fields of
// another literal is the evaluator's business; rather than rely on it (as of
// CUE v0.18, declaring a field beside a closed embedding doesn't allow it), such
// literals are not merged.
func plain(s *ast.StructLit) bool {
	for _, d := range s.Elts {
		if _, ok := d.(*ast.EmbedDecl); ok {
			return false
		}
	}
	return true
}

// mergeStruct splices into s the struct literals it embeds, if all it embeds
// is plain struct literals, then merges fields s declares more than once.
func (c *canon) mergeStruct(s *ast.StructLit) {
	var embeds []*ast.StructLit
	for _, d := range s.Elts {
		if e, ok := d.(*ast.EmbedDecl); ok {
			if sl, ok := e.Expr.(*ast.StructLit); ok && plain(sl) {
				embeds = append(embeds, sl)
				continue
			}
			embeds = nil
			break
		}
	}
	if len(embeds) > 0 {
		var elts []ast.Decl
		for _, d := range s.Elts {
			e, ok := d.(*ast.EmbedDecl)
			if !ok {
				elts = append(elts, d)
				continue
			}
			sl := e.Expr.(*ast.StructLit)
			// The embedded literal's references already resolve through s, so
			// only s's own can be captured, by what the literal declares.
			if capturesExcept(sl, s, sl) {
				elts = append(elts, d)
				continue
			}
			elts = append(elts, sl.Elts...)
		}
		s.Elts = elts
	}
	c.mergeFields(s)
}

// mergeFields merges the fields s declares more than once into the first of
// them, with the & of their values: a: X, a: Y is a: X & Y. A field with a
// postfix alias or attributes is left alone, as are fields whose constraints
// (?, !) differ.
func (c *canon) mergeFields(s *ast.StructLit) {
	// A quoted label is never hidden nor a definition, so "_a" is not _a: an
	// identifier and a string are different keys, even for the same name.
	type key struct {
		label      string
		quoted     bool
		constraint token.Token
	}
	first := map[key]*ast.Field{}
	merged := map[*ast.Field]bool{}
	var elts []ast.Decl
	for _, d := range s.Elts {
		f, ok := d.(*ast.Field)
		if !ok || f.Alias != nil || len(f.Attrs) > 0 {
			elts = append(elts, d)
			continue
		}
		name, ok := staticLabel(f.Label)
		if !ok {
			elts = append(elts, d)
			continue
		}
		_, quoted := f.Label.(*ast.BasicLit)
		k := key{name, quoted, f.Constraint}
		if prev, ok := first[k]; ok {
			prev.Value = &ast.BinaryExpr{Op: token.AND, X: prev.Value, Y: f.Value}
			merged[prev] = true
			continue
		}
		first[k] = f
		elts = append(elts, d)
	}
	s.Elts = elts
	for f := range merged {
		// A definition's value is within a definition: see merge.
		if !isDefinition(f) {
			f.Value = c.mergeAnd(f.Value)
		}
	}
}

// staticLabel returns the name a label declares, if it is an identifier or a
// string literal.
func staticLabel(l ast.Label) (string, bool) {
	switch x := l.(type) {
	case *ast.Ident:
		return x.Name, true
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	}
	return "", false
}

// captures reports whether merging a and b into one struct literal could make
// a reference in b resolve to something a declares, where it resolved outside b
// before.
func captures(a, b *ast.StructLit) bool {
	return capturesExcept(a, b, nil)
}

// capturesExcept is captures, ignoring the references within skip.
func capturesExcept(a, b *ast.StructLit, skip ast.Node) bool {
	declared := declaredNames(a)
	within := nodes(b)
	if skip != nil {
		for n := range nodes(skip) {
			delete(within, n)
		}
	}
	captured := false
	for _, id := range references(b, skip) {
		if id.Name == "self" || id.Name == "__self" {
			// self denotes the struct literal it is in.
			return true
		}
		if id.Node != nil && within[id.Node] {
			continue
		}
		if declared[id.Name] {
			captured = true
		}
	}
	return captured
}

// declaredNames returns the names s declares: its fields' labels (including
// those inside its comprehensions) and their aliases, and its lets.
func declaredNames(s *ast.StructLit) map[string]bool {
	names := map[string]bool{}
	var decls func([]ast.Decl)
	decls = func(ds []ast.Decl) {
		for _, d := range ds {
			switch x := d.(type) {
			case *ast.Field:
				if name, ok := staticLabel(x.Label); ok {
					names[name] = true
				}
				if x.Alias != nil {
					for _, id := range []*ast.Ident{x.Alias.Label, x.Alias.Field} {
						if id != nil {
							names[id.Name] = true
						}
					}
				}
			case *ast.LetClause:
				names[x.Ident.Name] = true
			case *ast.Comprehension:
				if sl, ok := x.Value.(*ast.StructLit); ok {
					decls(sl.Elts)
				}
			}
		}
	}
	decls(s.Elts)
	return names
}

// references returns the identifiers in n that refer to something, rather
// than declare a name or select a field, leaving out those within skip.
func references(n, skip ast.Node) []*ast.Ident {
	notRef := map[*ast.Ident]bool{}
	var out []*ast.Ident
	ast.Walk(n, func(n ast.Node) bool {
		if n == skip && skip != nil {
			return false
		}
		switch x := n.(type) {
		case *ast.Field:
			if id, ok := x.Label.(*ast.Ident); ok {
				notRef[id] = true
			}
			if x.Alias != nil {
				notRef[x.Alias.Label] = true
				notRef[x.Alias.Field] = true
			}
		case *ast.LetClause:
			notRef[x.Ident] = true
		case *ast.ForClause:
			notRef[x.Key] = true
			notRef[x.Value] = true
		case *ast.SelectorExpr:
			if id, ok := x.Sel.(*ast.Ident); ok {
				notRef[id] = true
			}
		case *ast.Ident:
			if !notRef[x] {
				out = append(out, x)
			}
		}
		return true
	}, nil)
	return out
}

func nodes(n ast.Node) map[ast.Node]bool {
	set := map[ast.Node]bool{}
	ast.Walk(n, func(n ast.Node) bool {
		set[n] = true
		return true
	}, nil)
	return set
}

func parents(x ast.Node) map[ast.Node]ast.Node {
	p := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Walk(x, func(n ast.Node) bool {
		if len(stack) > 0 {
			p[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		return true
	}, func(ast.Node) { stack = stack[:len(stack)-1] })
	return p
}

// sort sorts, bottom up, the operands of & and | and the declarations of
// struct literals, dropping duplicates where that keeps the meaning, and gives
// each let its canonical name before anything containing a reference to it is
// sorted.
func (c *canon) sort(x ast.Expr) (ast.Expr, error) {
	x = astutil.Apply(x, nil, c.sortNode).(ast.Expr)
	return x, c.err
}

func (c *canon) sortNode(cur astutil.Cursor) bool {
	switch n := cur.Node().(type) {
	case *ast.Ident:
		if l, ok := n.Node.(*ast.LetClause); ok {
			c.name(l)
			n.Name = l.Ident.Name
		}
	case *ast.LetClause:
		c.name(n)
	case *ast.StructLit:
		n.Elts = sortDedup(n.Elts, true, func(d ast.Decl) string { return c.text(d) })
	case *ast.BinaryExpr:
		switch n.Op {
		case token.AND, token.OR:
			ops := sortDedup(flatten(n, n.Op), n.Op == token.AND, func(x ast.Expr) string { return c.text(x) })
			cur.Replace(ast.NewBinExpr(n.Op, ops...))
		}
	}
	return c.err == nil
}

// text is text, recording the first error in c.err.
func (c *canon) text(n ast.Node) string {
	t, err := text(n)
	if err != nil && c.err == nil {
		c.err = err
	}
	return t
}

// name gives l its canonical name, from its scope depth and the canonical text
// of its expression: __L<depth>_<digest prefix>. A let's name then changes only
// with its content. The depth keeps two lets with the same text in nested
// scopes, which may mean different things, from sharing a name.
func (c *canon) name(l *ast.LetClause) {
	if c.named[l] || c.err != nil {
		return
	}
	if c.naming[l] {
		c.err = fmt.Errorf("let %s refers to itself", l.Ident.Name)
		return
	}
	c.naming[l] = true
	l.Expr = astutil.Apply(l.Expr, nil, c.sortNode).(ast.Expr)
	t := c.text(l.Expr)
	if c.err != nil {
		return
	}
	depth := 0
	for p := c.parents[l]; p != nil; p = c.parents[p] {
		switch p.(type) {
		case *ast.StructLit, *ast.Comprehension:
			depth++
		}
	}
	sum := sha256.Sum256([]byte(t))
	l.Ident.Name = fmt.Sprintf("__L%d_%s", depth, hex.EncodeToString(sum[:8]))
	c.named[l] = true
}

// flatten returns the operands of a chain of op. & is associative, so it is
// flattened through parentheses; | only where it is chained directly, as
// parenthesized disjunctions carry their own defaults. Operands that are
// themselves binary expressions are parenthesized, so that the text of the
// rebuilt chain can't be read with a different grouping.
func flatten(x ast.Expr, op token.Token) []ast.Expr {
	var out []ast.Expr
	var walk func(ast.Expr)
	walk = func(x ast.Expr) {
		inner := x
		if op == token.AND {
			for {
				p, ok := inner.(*ast.ParenExpr)
				if !ok {
					break
				}
				inner = p.X
			}
		}
		if b, ok := inner.(*ast.BinaryExpr); ok && b.Op == op {
			walk(b.X)
			walk(b.Y)
			return
		}
		if b, ok := x.(*ast.BinaryExpr); ok {
			x = &ast.ParenExpr{X: b}
		}
		out = append(out, x)
	}
	walk(x)
	return out
}

// sortDedup sorts xs by their text and, if dedup is set, drops all but one of
// those with the same text.
func sortDedup[T ast.Node](xs []T, dedup bool, textOf func(T) string) []T {
	type keyed struct {
		t string
		x T
	}
	ks := make([]keyed, len(xs))
	for i, x := range xs {
		ks[i] = keyed{textOf(x), x}
	}
	slices.SortStableFunc(ks, func(a, b keyed) int { return strings.Compare(a.t, b.t) })
	out := make([]T, 0, len(ks))
	for i, k := range ks {
		if dedup && i > 0 && ks[i-1].t == k.t {
			continue
		}
		out = append(out, k.x)
	}
	return out
}

func text(n ast.Node) (string, error) {
	b, err := format.Node(n)
	if err != nil {
		return "", fmt.Errorf("formatting canonical form: %w", err)
	}
	return string(b), nil
}
