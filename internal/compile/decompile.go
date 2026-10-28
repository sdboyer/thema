package compile

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/cueview"
	"github.com/sdboyer/thema/internal/lenscheck"
)

// Decompile recovers the authoring files of a lineage in normal form. It is
// the inverse of [Compile] on the canonical subset of the normal form, the
// lineages Compile produces: implicit copies are dropped, derived lacunas
// become the drops they came from, and backward entries equal to what
// derivation would produce are omitted. A lens outside that subset is an
// error naming its edge; nothing is approximated.
//
// lin is the lineage value, as compiled or as loaded; it need not be bound.
func Decompile(lin cue.Value) (name string, files []File, err error) {
	name, files, err = decompile(lin)
	if err != nil {
		// Every reason is a sentence in the normal form's terms about what
		// the authoring form cannot express; the lineage is the position.
		return "", nil, diag.New(diag.E0356).AtValue(lin).Headline(err.Error()).
			Help("the authoring form expresses the lenses Compile produces: mappings that copy and compute, and _drop, _orelse, _match and _each").
			Err()
	}
	return name, files, nil
}

func decompile(lin cue.Value) (name string, files []File, err error) {
	name, err = lin.LookupPath(cue.MakePath(cue.Str("name"))).String()
	if err != nil {
		return "", nil, fmt.Errorf("name: %w", err)
	}
	vs, err := decompileVersions(lin)
	if err != nil {
		return "", nil, err
	}
	lenses, err := decompileEdges(lin)
	if err != nil {
		return "", nil, err
	}

	for i, v := range vs {
		var fwd, bwd *dbody
		if i > 0 {
			prev := vs[i-1]
			fkey, bkey := edgeKey(prev.v, v.v), edgeKey(v.v, prev.v)
			var fwdEntries []dentry
			if l, ok := lenses[fkey]; ok {
				fwd, err = decompileLens(l, fkey, prev.v, v.v)
				if err != nil {
					return "", nil, err
				}
				fwdEntries = fwd.entries
			}
			l, ok := lenses[bkey]
			if !ok {
				return "", nil, fmt.Errorf("no lens at %s", bkey)
			}
			bwd, err = decompileLens(l, bkey, v.v, prev.v)
			if err != nil {
				return "", nil, err
			}
			bwd.entries = stripDerived(bwd.entries, fwdEntries, bwd.lacunas, v.schema, prev.schema)
		}
		var prev thema.SyntacticVersion
		if i > 0 {
			prev = vs[i-1].v
		}
		text, err := versionText(v, prev, fwd, bwd)
		if err != nil {
			return "", nil, err
		}
		p := fmt.Sprintf("%s%s", v.v, Ext)
		if i < len(vs)-1 {
			p = "history/" + p
		}
		files = append(files, File{Path: p, Text: []byte(text)})
	}
	return name, files, nil
}

type dversion struct {
	v      thema.SyntacticVersion
	schema cue.Value
}

func decompileVersions(lin cue.Value) ([]dversion, error) {
	var vs []dversion
	majors, err := lin.LookupPath(cue.MakePath(cue.Str("schemas"))).Fields()
	if err != nil {
		return nil, fmt.Errorf("schemas: %w", err)
	}
	for majors.Next() {
		major, err := strconv.ParseUint(majors.Selector().Unquoted(), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("schemas: %q is not a major version", majors.Selector())
		}
		minors, err := majors.Value().Fields()
		if err != nil {
			return nil, fmt.Errorf("schemas.%s: %w", majors.Selector(), err)
		}
		for minors.Next() {
			minor, err := strconv.ParseUint(minors.Selector().Unquoted(), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("schemas.%s: %q is not a minor version", majors.Selector(), minors.Selector())
			}
			vs = append(vs, dversion{v: thema.SV(uint(major), uint(minor)), schema: minors.Value().LookupPath(cue.MakePath(cue.Str("schema")))})
		}
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].v.Less(vs[j].v) })
	return vs, nil
}

func decompileEdges(lin cue.Value) (map[string]cue.Value, error) {
	out := make(map[string]cue.Value)
	iter, err := lin.LookupPath(cue.MakePath(cue.Str("lenses"))).Fields()
	if err != nil {
		return nil, fmt.Errorf("lenses: %w", err)
	}
	for iter.Next() {
		out[iter.Selector().Unquoted()] = iter.Value()
	}
	return out, nil
}

func edgeKey(from, to thema.SyntacticVersion) string {
	return fmt.Sprintf("%s->%s", from, to)
}

// A dentry is a mapping read back from a normal-form lens.
type dentry struct {
	kind     entryKind
	target   cue.Path
	source   cue.Path
	expr     ast.Expr
	comments []*ast.CommentGroup
	// fallback is orelse's fallback; arms and catchAll are match's arms;
	// body is each's body.
	fallback ast.Expr
	arms     []arm
	catchAll *arm
	body     *dbody
	// implicit marks a copy (a move from a path to itself) Compile would
	// regenerate, which is omitted unless a lacuna names it.
	implicit bool
	// computed is a computed mapping's expression, or orelse's computed
	// primary.
	computed ast.Expr
}

// A dbody is a lens body read back from the normal form, in result order
// with drops last. root is the field references start from, the same in the
// normal and the authoring form (v0_0, or item). lacunas are the lens's
// declared lacunas in list order, keyed by full path; only the lens's body
// holds them, not an each body's.
type dbody struct {
	root    string
	entries []dentry
	lacunas []dlacuna
}

// A dlacuna is a declared lacuna read back from the normal form.
type dlacuna struct {
	path     cue.Path
	typ, msg string
	// when is the authored condition, nil where the condition is the one the
	// mapping implies.
	when ast.Expr
}

// A branch is one `if cond {field}` of a result, before it is understood.
type branch struct {
	cond  ast.Expr
	field *ast.Field
}

// decompileLens reads the mappings of the lens at key, from sv to tv.
func decompileLens(l cue.Value, key string, sv, tv thema.SyntacticVersion) (*dbody, error) {
	// The whole lens is exported, so that references to input stay references
	// (exported alone, result's references to it would become a generated let).
	n, err := cueview.Documented(l)
	if err != nil {
		return nil, fmt.Errorf("lens %s: %w", key, err)
	}
	lens, err := lensDecls(n, key)
	if err != nil {
		return nil, err
	}
	var result *ast.StructLit
	var lacunas *ast.ListLit
	lets := make(map[string]ast.Expr)
	for _, d := range lens.Elts {
		switch d := d.(type) {
		case *ast.LetClause:
			lets[d.Ident.Name] = d.Expr
		case *ast.Field:
			name, _, _ := ast.LabelName(d.Label)
			switch name {
			case paramName(tv):
				result, _ = d.Value.(*ast.StructLit)
			case "lacunas":
				lacunas, _ = d.Value.(*ast.ListLit)
			}
		}
	}
	if result == nil {
		return nil, fmt.Errorf("lens %s is outside the authoring form: its result is not a struct literal", key)
	}
	b := &dbody{root: paramName(sv)}
	if werr := b.walk(result.Elts, cue.MakePath(), key); werr != nil {
		return nil, werr
	}
	if lacunas != nil {
		for _, el := range lacunas.Elts {
			if lerr := b.lacuna(el, lets, key, tv); lerr != nil {
				return nil, lerr
			}
		}
	}
	return b, nil
}

// lensDecls returns an exported lens as a struct. A lens whose expressions
// call into strings exports as a file importing it, which the authoring form
// writes without the import; any other import is outside the form.
func lensDecls(n ast.Node, key string) (*ast.StructLit, error) {
	switch n := n.(type) {
	case *ast.StructLit:
		return n, nil
	case *ast.File:
		lit := &ast.StructLit{}
		for _, d := range n.Decls {
			imp, ok := d.(*ast.ImportDecl)
			if !ok {
				lit.Elts = append(lit.Elts, d)
				continue
			}
			for _, spec := range imp.Specs {
				if path, _ := literal.Unquote(spec.Path.Value); path != pkgStrings || spec.Name != nil {
					return nil, fmt.Errorf("lens %s is outside the authoring form: it imports %s", key, spec.Path.Value)
				}
			}
		}
		return lit, nil
	}
	return nil, fmt.Errorf("lens %s is outside the authoring form: it is not a struct", key)
}

// walk reads a result struct's declarations into the body.
func (b *dbody) walk(decls []ast.Decl, tbase cue.Path, key string) error {
	var branches []branch
	start := len(b.entries)
	// order is each field's place in the result, by name beneath tbase, so
	// that fields written under arms keep it.
	order := make(map[string]int)
	note := func(f *ast.Field) {
		if name, _, err := ast.LabelName(f.Label); err == nil {
			if _, seen := order[name]; !seen {
				order[name] = len(order)
			}
		}
	}
	for _, d := range decls {
		switch d := d.(type) {
		case *ast.CommentGroup:
		case *ast.Field:
			note(d)
			if err := b.field(d, tbase, key); err != nil {
				return err
			}
		case *ast.Comprehension:
			br, ok := singleBranch(d)
			if !ok {
				return fmt.Errorf("lens %s is outside the authoring form: a comprehension in its result that is not an arm", key)
			}
			note(br.field)
			branches = append(branches, br)
		default:
			return fmt.Errorf("lens %s is outside the authoring form: %T in its result", key, d)
		}
	}
	if err := b.branches(branches, tbase, key); err != nil {
		return err
	}
	depth := len(tbase.Selectors())
	rank := func(en dentry) int {
		sels := en.target.Selectors()
		if len(sels) <= depth {
			return len(order)
		}
		if i, ok := order[sels[depth].Unquoted()]; ok {
			return i
		}
		return len(order)
	}
	sort.SliceStable(b.entries[start:], func(i, j int) bool {
		return rank(b.entries[start+i]) < rank(b.entries[start+j])
	})
	return nil
}

func (b *dbody) field(f *ast.Field, tbase cue.Path, key string) error {
	name, _, err := ast.LabelName(f.Label)
	if err != nil {
		return fmt.Errorf("lens %s is outside the authoring form: a field whose label is not a name", key)
	}
	tpath := appendPath(tbase, cue.Str(name))
	if s, ok := f.Value.(*ast.StructLit); ok {
		return b.walk(s.Elts, tpath, key)
	}
	en := dentry{target: tpath, expr: f.Value, comments: ast.Comments(f)}
	if root, p, ok := refPathAny(f.Value); ok && root == b.root {
		en.kind, en.source, en.implicit = kindMove, p, p.String() == tpath.String()
	} else if isLiteral(f.Value) {
		en.kind = kindConst
	} else if each, ok := b.eachOf(f.Value, key); ok {
		var eerr error
		en.kind, en.source, en.body, eerr = kindEach, each.source, each.body, each.err
		if eerr != nil {
			return eerr
		}
	} else if h, ok := asHeadSyntax(f.Value); ok && b.allPresence(h.cond) {
		// orelse over a computed primary: the expression where the optional
		// fields it reads are present, the fallback otherwise.
		if _, _, isRef := refPathAny(h.els); !isRef && !isLiteral(h.els) {
			return fmt.Errorf("lens %s is outside the authoring form: the fallback of %s is computed", key, tpath)
		}
		en.kind, en.computed, en.fallback = kindOrElse, h.then, h.els
	} else if b.expressionOK(f.Value) {
		en.kind, en.computed = kindComputed, f.Value
	} else {
		return fmt.Errorf("lens %s is outside the authoring form: %s is an expression the authoring form cannot write", key, tpath)
	}
	b.entries = append(b.entries, en)
	return nil
}

type eachSyntax struct {
	source cue.Path
	body   *dbody
	err    error
}

// eachOf recognizes [for item in <root>.<list> {...}].
func (b *dbody) eachOf(x ast.Expr, key string) (eachSyntax, bool) {
	list, ok := x.(*ast.ListLit)
	if !ok || len(list.Elts) != 1 {
		return eachSyntax{}, false
	}
	c, ok := list.Elts[0].(*ast.Comprehension)
	if !ok || len(c.Clauses) != 1 {
		return eachSyntax{}, false
	}
	fc, ok := c.Clauses[0].(*ast.ForClause)
	if !ok || fc.Key != nil || fc.Value == nil || fc.Value.Name != rootItem {
		return eachSyntax{}, false
	}
	root, p, ok := refPathAny(fc.Source)
	if !ok || root != b.root {
		return eachSyntax{}, false
	}
	s, ok := c.Value.(*ast.StructLit)
	if !ok {
		return eachSyntax{}, false
	}
	body := &dbody{root: rootItem}
	err := body.walk(s.Elts, cue.MakePath(), key)
	return eachSyntax{source: p, body: body, err: err}, true
}

// singleBranch reads `if cond {field}`.
func singleBranch(c *ast.Comprehension) (branch, bool) {
	if len(c.Clauses) != 1 {
		return branch{}, false
	}
	ifc, ok := c.Clauses[0].(*ast.IfClause)
	if !ok {
		return branch{}, false
	}
	s, ok := c.Value.(*ast.StructLit)
	if !ok || len(s.Elts) != 1 {
		return branch{}, false
	}
	f, ok := s.Elts[0].(*ast.Field)
	if !ok {
		return branch{}, false
	}
	return branch{cond: ifc.Condition, field: f}, true
}

// A test is one conjunct of a branch condition: <root>.p != _|_ (present),
// <root>.p == _|_ (absent), or (<root>.p & x) != _|_ / == _|_ (matches /
// rejects x), where x is a key, the disjunction of keys, or the target type.
// The exporter may swap the operands of &.
type test struct {
	present bool // the comparison is != _|_
	path    cue.Path
	operand ast.Expr // nil for a presence test
}

// key returns the string key the test matches, if its operand is one.
func (t test) key() (string, bool) {
	lit, ok := t.operand.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	k, err := literal.Unquote(lit.Value)
	return k, err == nil
}

// tests splits a branch condition into its conjuncts.
func (b *dbody) tests(cond ast.Expr) ([]test, bool) {
	if paren, ok := cond.(*ast.ParenExpr); ok {
		return b.tests(paren.X)
	}
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return nil, false
	}
	if bin.Op == token.LAND {
		l, ok1 := b.tests(bin.X)
		r, ok2 := b.tests(bin.Y)
		return append(l, r...), ok1 && ok2
	}
	if bin.Op != token.NEQ && bin.Op != token.EQL {
		return nil, false
	}
	if _, isBottom := bin.Y.(*ast.BottomLit); !isBottom {
		return nil, false
	}
	t := test{present: bin.Op == token.NEQ}
	x := bin.X
	if paren, ok := x.(*ast.ParenExpr); ok {
		x = paren.X
	}
	if root, p, isRef := refPathAny(x); isRef && root == b.root {
		t.path = p
		return []test{t}, true
	}
	and, isAnd := x.(*ast.BinaryExpr)
	if !isAnd || and.Op != token.AND {
		return nil, false
	}
	for _, side := range [][2]ast.Expr{{and.X, and.Y}, {and.Y, and.X}} {
		if root, p, isRef := refPathAny(side[0]); isRef && root == b.root {
			t.path, t.operand = p, side[1]
			if paren, ok := t.operand.(*ast.ParenExpr); ok {
				t.operand = paren.X
			}
			return []test{t}, true
		}
	}
	return nil, false
}

// branches interprets the comprehensions of one struct: a copy or move under
// a presence test, the two arms of an orelse, or the arms of a match.
func (b *dbody) branches(brs []branch, tbase cue.Path, key string) error {
	type group struct {
		name string
		brs  []branch
	}
	var groups []*group
	byName := make(map[string]*group)
	for _, br := range brs {
		name, _, err := ast.LabelName(br.field.Label)
		if err != nil {
			return fmt.Errorf("lens %s is outside the authoring form: a branch whose label is not a name", key)
		}
		g, ok := byName[name]
		if !ok {
			g = &group{name: name}
			byName[name] = g
			groups = append(groups, g)
		}
		g.brs = append(g.brs, br)
	}
	for _, g := range groups {
		tpath := appendPath(tbase, cue.Str(g.name))
		en := dentry{target: tpath, comments: ast.Comments(g.brs[0].field)}
		if s, ok := g.brs[0].field.Value.(*ast.StructLit); ok && len(g.brs) == 1 {
			// A copy of an optional struct, written only where the source
			// has it.
			ts, ok := b.tests(g.brs[0].cond)
			if !ok || len(ts) != 1 || ts[0].operand != nil || !ts[0].present || ts[0].path.String() != tpath.String() {
				return fmt.Errorf("lens %s is outside the authoring form: %s is written under a condition the authoring form cannot express", key, tpath)
			}
			if err := b.walk(s.Elts, tpath, key); err != nil {
				return err
			}
			continue
		}
		if len(g.brs) == 1 && b.allPresence(g.brs[0].cond) {
			if _, _, isRef := refPathAny(g.brs[0].field.Value); !isRef && !isLiteral(g.brs[0].field.Value) {
				// A computed mapping that reads optional fields, written only
				// where they are present.
				if !b.expressionOK(g.brs[0].field.Value) {
					return fmt.Errorf("lens %s is outside the authoring form: %s is an expression the authoring form cannot write", key, tpath)
				}
				en.kind, en.computed = kindComputed, g.brs[0].field.Value
				b.entries = append(b.entries, en)
				continue
			}
		}
		var present, absent, passthrough, catchAll []branch
		var arms []branch
		var armKeys []string
		var source cue.Path
		for _, br := range g.brs {
			ts, ok := b.tests(br.cond)
			if !ok || len(ts) == 0 || len(ts) > 2 {
				return fmt.Errorf("lens %s is outside the authoring form: a condition on %s the authoring form cannot express", key, tpath)
			}
			source = ts[0].path
			last := ts[len(ts)-1]
			switch {
			case len(ts) == 1 && last.operand == nil && last.present:
				present = append(present, br)
			case len(ts) == 1 && last.operand == nil:
				absent = append(absent, br)
			case len(ts) == 1 && last.present:
				k, isKey := last.key()
				if !isKey {
					// A type test alone: a passthrough with no arms.
					passthrough = append(passthrough, br)
					continue
				}
				arms = append(arms, br)
				armKeys = append(armKeys, k)
			case last.operand != nil && last.present:
				passthrough = append(passthrough, br)
			case last.operand != nil:
				catchAll = append(catchAll, br)
			default:
				return fmt.Errorf("lens %s is outside the authoring form: a condition on %s the authoring form cannot express", key, tpath)
			}
		}
		switch {
		case len(arms) > 0 || len(passthrough) > 0 || len(catchAll) > 0:
			if len(present)+len(absent) > 0 || len(passthrough) > 1 || len(catchAll) > 1 {
				return fmt.Errorf("lens %s is outside the authoring form: the arms of %s are not a _match", key, tpath)
			}
			en.kind, en.source = kindMatch, source
			for i, br := range arms {
				if !isLiteral(br.field.Value) {
					return fmt.Errorf("lens %s is outside the authoring form: _match arm %q of %s is not a constant", key, armKeys[i], tpath)
				}
				en.arms = append(en.arms, arm{key: armKeys[i], value: br.field.Value})
			}
			if len(catchAll) == 1 {
				if !isLiteral(catchAll[0].field.Value) {
					return fmt.Errorf("lens %s is outside the authoring form: the [_] arm of %s is not a constant", key, tpath)
				}
				en.catchAll = &arm{value: catchAll[0].field.Value}
			}
		case len(present) == 1 && len(absent) == 1:
			root, p, ok := refPathAny(present[0].field.Value)
			if !ok || root != b.root || p.String() != source.String() {
				return fmt.Errorf("lens %s is outside the authoring form: the arms of %s are not an _orelse", key, tpath)
			}
			fb := absent[0].field.Value
			if _, _, isRef := refPathAny(fb); !isRef && !isLiteral(fb) {
				return fmt.Errorf("lens %s is outside the authoring form: the fallback of %s is computed", key, tpath)
			}
			en.kind, en.source, en.fallback = kindOrElse, source, fb
		case len(present) == 1 && len(absent) == 0:
			root, p, ok := refPathAny(present[0].field.Value)
			if !ok || root != b.root || p.String() != source.String() {
				return fmt.Errorf("lens %s is outside the authoring form: the presence test on %s does not test its own reference", key, tpath)
			}
			en.kind, en.source, en.expr, en.implicit = kindMove, p, present[0].field.Value, p.String() == tpath.String()
		default:
			return fmt.Errorf("lens %s is outside the authoring form: the arms of %s", key, tpath)
		}
		b.entries = append(b.entries, en)
	}
	return nil
}

// lacuna reads one lacuna from its syntax: a derived DroppedField becomes a
// drop, and a declared lacuna attaches to the mapping, arm or fallback it
// names.
func (b *dbody) lacuna(x ast.Expr, lets map[string]ast.Expr, key string, tv thema.SyntacticVersion) error {
	s, ok := x.(*ast.StructLit)
	if !ok {
		return fmt.Errorf("lens %s is outside the authoring form: a lacuna that is not a struct literal", key)
	}
	var derived bool
	var typ, msg string
	var cond ast.Expr
	var sources, targets []ast.Expr
	for _, d := range s.Elts {
		switch d := d.(type) {
		case *ast.Attribute:
			if k, body := d.Split(); k == "thema" && strings.TrimSpace(body) == "derived" {
				derived = true
			}
		case *ast.Field:
			name, _, _ := ast.LabelName(d.Label)
			var err error
			switch name {
			case "type":
				typ, err = stringLit(d.Value)
			case "message":
				msg, err = stringLit(d.Value)
			case "condition":
				cond = d.Value
			case "sourceFields":
				sources = []ast.Expr{d.Value}
			case "targetFields":
				targets = []ast.Expr{d.Value}
			}
			if err != nil {
				return fmt.Errorf("lens %s is outside the authoring form: a lacuna's %s is not a string", key, name)
			}
		}
	}
	srcPaths, srcEach, err := fieldPaths(sources, lets)
	if err != nil {
		return fmt.Errorf("lens %s: %w", key, err)
	}
	tgtPaths, _, err := fieldPaths(targets, lets)
	if err != nil {
		return fmt.Errorf("lens %s: %w", key, err)
	}

	if derived {
		if typ != lacunaDropped || len(srcPaths) != 1 || len(tgtPaths) != 0 {
			return fmt.Errorf("lens %s is outside the authoring form: a derived lacuna of type %s", key, typ)
		}
		body, p, path := b, srcPaths[0], srcPaths[0]
		if srcEach != nil {
			// A drop inside an each body: find the each over that list.
			body = nil
			for i := range b.entries {
				if b.entries[i].kind == kindEach && b.entries[i].source.String() == srcEach.String() {
					body = b.entries[i].body
					path = joinPath(b.entries[i].target, p)
				}
			}
			if body == nil {
				return fmt.Errorf("lens %s is outside the authoring form: a lacuna over %s[], which no _each maps", key, srcEach)
			}
			cond = nil // the aggregate's len(_d_n) > 0
		}
		en := dentry{kind: kindDrop, source: p, target: p}
		body.entries = append(body.entries, en)
		when, structural := b.when(&en, cond)
		if msg != dropMessageFor(srcEach, p, tv) || !structural {
			b.lacunas = append(b.lacunas, dlacuna{path: path, typ: lacunaDropped, msg: msg, when: when})
		}
		return nil
	}

	var en *dentry
	switch {
	case len(tgtPaths) == 1:
		en = b.find(tgtPaths[0], false)
	case len(srcPaths) == 1 && len(tgtPaths) == 0:
		en = b.find(srcPaths[0], true)
	default:
		return fmt.Errorf("lens %s is outside the authoring form: a lacuna over several fields", key)
	}
	if en == nil {
		return fmt.Errorf("lens %s is outside the authoring form: a lacuna on a field no mapping produces", key)
	}
	en.implicit = false // the author wrote the copy, to key the lacuna
	when, _ := b.when(en, cond)
	b.lacunas = append(b.lacunas, dlacuna{path: en.target, typ: typ, msg: msg, when: when})
	return nil
}

// find returns the entry keyed p: a drop of p, or the mapping producing p.
func (b *dbody) find(p cue.Path, drop bool) *dentry {
	for i := range b.entries {
		en := &b.entries[i]
		if (en.kind == kindDrop) == drop && en.target.String() == p.String() {
			return en
		}
	}
	return nil
}

// when turns a lacuna's condition into the authored when: nil, and
// structural, if the condition is the one en implies (none for a move or a
// constant; the fallback for an orelse; the [_] arm for a match; presence
// for a drop), else the condition as written.
func (b *dbody) when(en *dentry, cond ast.Expr) (when ast.Expr, structural bool) {
	if cond == nil {
		return nil, en.kind != kindEach
	}
	if en.kind == kindOrElse && en.computed != nil {
		// The fallback was used: [if <presence> {false}, true][0].
		if h, ok := asHeadSyntax(cond); ok && b.allPresence(h.cond) && isBoolLit(h.then, litFalse) && isBoolLit(h.els, litTrue) {
			return nil, true
		}
	}
	ts, ok := b.tests(cond)
	if ok {
		last := ts[len(ts)-1]
		switch en.kind {
		case kindOrElse:
			if len(ts) == 1 && last.operand == nil && !last.present && last.path.String() == en.source.String() {
				return nil, true
			}
		case kindMatch:
			if last.operand != nil && !last.present && en.catchAll != nil {
				return nil, true
			}
		case kindDrop:
			if len(ts) == 1 && last.operand == nil && last.present && last.path.String() == en.source.String() {
				return nil, true
			}
		}
		if len(ts) == 1 && last.operand == nil && last.present {
			// A presence test alone is an optional field named bare.
			return ref(b.root, last.path), false
		}
	}
	if inner, ok := headCondition(cond); ok {
		// A condition reading optional fields, compiled to apply only where
		// they are present; the when is the inner expression.
		return inner, false
	}
	return cond, false
}

// headSyntax is [if cond {then}, els][0], as written.
type headSyntax struct {
	cond, then, els ast.Expr
}

// asHeadSyntax recognizes [if cond {then}, els][0].
func asHeadSyntax(x ast.Expr) (headSyntax, bool) {
	ix, ok := x.(*ast.IndexExpr)
	if !ok {
		return headSyntax{}, false
	}
	if lit, isLit := ix.Index.(*ast.BasicLit); !isLit || lit.Value != "0" {
		return headSyntax{}, false
	}
	l, ok := ix.X.(*ast.ListLit)
	if !ok || len(l.Elts) != 2 {
		return headSyntax{}, false
	}
	comp, ok := l.Elts[0].(*ast.Comprehension)
	if !ok || len(comp.Clauses) != 1 {
		return headSyntax{}, false
	}
	ifc, isIf := comp.Clauses[0].(*ast.IfClause)
	if !isIf {
		return headSyntax{}, false
	}
	s, ok := comp.Value.(*ast.StructLit)
	if !ok || len(s.Elts) != 1 {
		return headSyntax{}, false
	}
	emb, ok := s.Elts[0].(*ast.EmbedDecl)
	if !ok {
		return headSyntax{}, false
	}
	return headSyntax{cond: ifc.Condition, then: emb.Expr, els: l.Elts[1]}, true
}

// The bool literals, as written.
const (
	litTrue  = "true"
	litFalse = "false"
)

func isBoolLit(x ast.Expr, v string) bool {
	switch x := x.(type) {
	case *ast.BasicLit:
		return x.Value == v
	case *ast.Ident:
		return x.Name == v
	}
	return false
}

// allPresence reports whether cond is presence tests on the root, joined by &&.
func (b *dbody) allPresence(cond ast.Expr) bool {
	ts, ok := b.tests(cond)
	if !ok || len(ts) == 0 {
		return false
	}
	for _, t := range ts {
		if t.operand != nil || !t.present {
			return false
		}
	}
	return true
}

// expressionOK reports whether x is an expression the authoring form writes
// as a computed mapping: references from the root, literals, operators, and
// calls to the builtins and strings functions a lens may call.
func (b *dbody) expressionOK(x ast.Expr) bool {
	ok := true
	ast.Walk(x, func(n ast.Node) bool {
		if !ok {
			return false
		}
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if root, _, isRef := refPathAny(n); isRef && root == b.root {
				return false
			}
			if pkg, isID := n.X.(*ast.Ident); isID && pkg.Name == pkgStrings {
				name, _, err := ast.LabelName(n.Sel)
				ok = err == nil && lenscheck.StringFunc(name)
				return false
			}
			ok = false
		case *ast.Ident:
			ok = n.Name == b.root || isBuiltin(n.Name) || n.Name == litTrue || n.Name == litFalse || n.Name == "null"
		case *ast.BinaryExpr, *ast.UnaryExpr, *ast.ParenExpr, *ast.CallExpr, *ast.BasicLit, *ast.Interpolation:
		default:
			ok = false
		}
		return ok
	}, nil)
	return ok
}

// headCondition recognizes [if <presence tests> {when}, false][0], the form a
// when that reads optional fields compiles to, and returns the when.
func headCondition(x ast.Expr) (ast.Expr, bool) {
	ix, ok := x.(*ast.IndexExpr)
	if !ok {
		return nil, false
	}
	if lit, isLit := ix.Index.(*ast.BasicLit); !isLit || lit.Value != "0" {
		return nil, false
	}
	l, ok := ix.X.(*ast.ListLit)
	if !ok || len(l.Elts) != 2 {
		return nil, false
	}
	if lit, isLit := l.Elts[1].(*ast.BasicLit); !isLit || lit.Value != litFalse {
		return nil, false
	}
	comp, ok := l.Elts[0].(*ast.Comprehension)
	if !ok || len(comp.Clauses) != 1 {
		return nil, false
	}
	if _, isIf := comp.Clauses[0].(*ast.IfClause); !isIf {
		return nil, false
	}
	s, ok := comp.Value.(*ast.StructLit)
	if !ok || len(s.Elts) != 1 {
		return nil, false
	}
	emb, ok := s.Elts[0].(*ast.EmbedDecl)
	if !ok {
		return nil, false
	}
	return emb.Expr, true
}

func joinPath(p, q cue.Path) cue.Path {
	return cue.MakePath(append(append([]cue.Selector{}, p.Selectors()...), q.Selectors()...)...)
}

func stringLit(x ast.Expr) (string, error) {
	lit, ok := x.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", fmt.Errorf("not a string")
	}
	return literal.Unquote(lit.Value)
}

// fieldPaths reads the paths of a lacuna's sourceFields or targetFields: a
// list of {path: "..."} structs, each possibly under a presence test, or a reference to a
// let holding the list an each body's drop aggregates, in which case the
// list's path is returned too.
func fieldPaths(exprs []ast.Expr, lets map[string]ast.Expr) (paths []cue.Path, each *cue.Path, err error) {
	for _, x := range exprs {
		if id, ok := x.(*ast.Ident); ok {
			x = lets[id.Name]
			if x == nil {
				return nil, nil, fmt.Errorf("a lacuna's fields refer to %s, which is not a let of the lens", id.Name)
			}
		}
		list, ok := x.(*ast.ListLit)
		if !ok {
			return nil, nil, fmt.Errorf("a lacuna's fields are not a list")
		}
		for _, el := range list.Elts {
			if c, ok := el.(*ast.Comprehension); ok {
				if fc, ok := c.Clauses[0].(*ast.ForClause); ok {
					_, listPath, ok := refPathAny(fc.Source)
					if !ok {
						return nil, nil, fmt.Errorf("a lacuna's fields iterate something other than a list in input")
					}
					each = &listPath
				}
				el = c.Value
			}
			s, ok := el.(*ast.StructLit)
			if !ok {
				return nil, nil, fmt.Errorf("a lacuna's field is not a struct")
			}
			for _, d := range s.Elts {
				f, ok := d.(*ast.Field)
				if !ok {
					continue
				}
				if name, _, _ := ast.LabelName(f.Label); name != "path" {
					continue
				}
				p, err := pathLit(f.Value)
				if err != nil {
					return nil, nil, err
				}
				paths = append(paths, p)
			}
		}
	}
	return paths, each, nil
}

// pathLit reads a path string, or the element path of an each drop's
// interpolated "<list>[\(i)].<path>".
func pathLit(x ast.Expr) (cue.Path, error) {
	switch x := x.(type) {
	case *ast.BasicLit:
		s, err := literal.Unquote(x.Value)
		if err != nil {
			return cue.Path{}, fmt.Errorf("a lacuna field's path is not a string")
		}
		return cue.ParsePath(s), nil
	case *ast.Interpolation:
		// "<list>[\(i)].<path>": a string part, the index, a string part.
		const parts = 3
		if len(x.Elts) == parts {
			if tail, ok := x.Elts[2].(*ast.BasicLit); ok && strings.HasPrefix(tail.Value, ")].") {
				return cue.ParsePath(strings.TrimSuffix(strings.TrimPrefix(tail.Value, ")]."), `"`)), nil
			}
		}
	}
	return cue.Path{}, fmt.Errorf("a lacuna field's path is not a string")
}

func dropMessageFor(each *cue.Path, p cue.Path, tv thema.SyntacticVersion) string {
	if each != nil {
		return fmt.Sprintf("%s[].%s has no equivalent in %s and was dropped", each, p, tv)
	}
	return fmt.Sprintf("%s has no equivalent in %s and was dropped", p, tv)
}

// stripDerived removes from a backward body the entries derivation would
// produce from the forward entries: the inverse of each single-use move, and
// the drop of each field the newer version added, where no lacuna refines it.
func stripDerived(entries, fwd []dentry, lacunas []dlacuna, newer, older cue.Value) []dentry {
	named := make(map[string]bool)
	for _, l := range lacunas {
		named[l.path.String()] = true
	}
	uses := make(map[string]int)
	for _, en := range fwd {
		if en.kind == kindMove {
			uses[en.source.String()]++
		}
	}
	inverse := make(map[string]string) // backward target -> source
	for _, en := range fwd {
		if en.kind == kindMove && uses[en.source.String()] == 1 {
			inverse[en.source.String()] = en.target.String()
		}
	}
	var out []dentry
	for _, en := range entries {
		switch en.kind {
		case kindMove:
			if inverse[en.target.String()] == en.source.String() {
				continue
			}
		case kindDrop:
			if !named[en.target.String()] && lookup(newer, en.source).Exists() && !lookup(older, en.source).Exists() {
				continue
			}
		}
		out = append(out, en)
	}
	return out
}

// versionText renders a version file; prev is the previous version, whose
// edge the lenses are on.
func versionText(v dversion, prev thema.SyntacticVersion, fwd, bwd *dbody) (string, error) {
	n, err := cueview.Documented(v.schema)
	if err != nil {
		return "", fmt.Errorf("%s: schema: %w", v.v, err)
	}
	var parts []string
	var expr ast.Expr
	switch n := n.(type) {
	case ast.Expr:
		expr = n
	case *ast.File:
		// A schema that uses CUE's builtin packages exports with their
		// imports, which the version file carries.
		var paths []string
		lit := &ast.StructLit{}
		for _, d := range n.Decls {
			imp, ok := d.(*ast.ImportDecl)
			if !ok {
				lit.Elts = append(lit.Elts, d)
				continue
			}
			for _, spec := range imp.Specs {
				path, _ := literal.Unquote(spec.Path.Value)
				if !compat.IsBuiltinPackage(path) {
					return "", fmt.Errorf("%s: schema is outside the authoring form: it imports %s", v.v, path)
				}
				paths = append(paths, path)
			}
		}
		expr = lit
		sort.Strings(paths)
		decl := &ast.ImportDecl{}
		for _, p := range paths {
			decl.Specs = append(decl.Specs, ast.NewImport(nil, p))
		}
		text, err := format.Node(&ast.File{Decls: []ast.Decl{decl}})
		if err != nil {
			return "", err
		}
		parts = append(parts, strings.TrimRight(string(text), "\n")+"\n")
	default:
		return "", fmt.Errorf("%s: schema is outside the authoring form", v.v)
	}
	for _, block := range []struct {
		name string
		b    *dbody
	}{{fieldSchema, nil}, {fieldForward, fwd}, {fieldBackward, bwd}} {
		val := expr
		if block.name != fieldSchema {
			if block.b == nil || block.b.empty() {
				continue
			}
			from, to := prev, v.v
			if block.name == fieldBackward {
				from, to = v.v, prev
			}
			param := &ast.Field{Label: ast.NewIdent(paramName(from)), Value: ast.NewIdent("_")}
			result := &ast.Field{Label: ast.NewIdent(paramName(to)), Value: block.b.syntax()}
			lens := &ast.StructLit{Elts: []ast.Decl{param, result}}
			if len(block.b.lacunas) > 0 {
				lens.Elts = append(lens.Elts, &ast.Field{Label: ast.NewIdent(fieldLacunas), Value: block.b.lacunasSyntax()})
			}
			val = lens
		}
		field := &ast.Field{Label: ast.NewIdent(block.name), Value: val}
		layout(field)
		text, err := format.Node(field)
		if err != nil {
			return "", err
		}
		parts = append(parts, strings.TrimRight(string(text), "\n")+"\n")
	}
	return strings.Join(parts, "\n"), nil
}

// empty reports whether the body has nothing to write: no entry but the
// implicit copies, and no lacuna.
func (b *dbody) empty() bool {
	for _, en := range b.entries {
		if !en.implicit {
			return false
		}
	}
	return len(b.lacunas) == 0
}

// A nesting builds a struct of fields keyed by path, nesting by path.
type nesting struct {
	root    *ast.StructLit
	structs map[string]*ast.StructLit
}

func newNesting() *nesting {
	root := &ast.StructLit{}
	return &nesting{root: root, structs: map[string]*ast.StructLit{"": root}}
}

// at returns the struct at p, creating the fields down to it.
func (n *nesting) at(p cue.Path) *ast.StructLit {
	sels := p.Selectors()
	s := n.root
	for i := range sels {
		k := cue.MakePath(sels[:i+1]...).String()
		c, ok := n.structs[k]
		if !ok {
			c = &ast.StructLit{}
			n.structs[k] = c
			s.Elts = append(s.Elts, &ast.Field{Label: label(sels[i].Unquoted()), Value: c})
		}
		s = c
	}
	return s
}

// add puts the field p: val in place.
func (n *nesting) add(p cue.Path, val ast.Expr) *ast.Field {
	sels := p.Selectors()
	f := &ast.Field{Label: label(sels[len(sels)-1].Unquoted()), Value: val}
	parent := n.at(cue.MakePath(sels[:len(sels)-1]...))
	parent.Elts = append(parent.Elts, f)
	return f
}

// syntax renders the body's entries as a struct, nesting by target path.
func (b *dbody) syntax() *ast.StructLit {
	n := newNesting()
	for _, en := range b.entries {
		if en.implicit {
			continue
		}
		var val ast.Expr
		switch en.kind {
		case kindMove:
			val = ref(b.root, en.source)
		case kindConst:
			val = en.expr
		case kindDrop:
			val = ast.NewIdent(opDrop)
		case kindOrElse:
			fallback := en.fallback
			if root, p, ok := refPathAny(fallback); ok && root == b.root {
				fallback = ref(b.root, p)
			}
			primary := en.computed
			if primary == nil {
				primary = ref(b.root, en.source)
			}
			val = &ast.CallExpr{Fun: ast.NewIdent(opOrElse), Args: []ast.Expr{primary, fallback}}
		case kindComputed:
			val = en.computed
		case kindMatch:
			arms := &ast.StructLit{}
			for _, a := range en.arms {
				arms.Elts = append(arms.Elts, &ast.Field{Label: ast.NewString(a.key), Value: a.value})
			}
			if en.catchAll != nil {
				arms.Elts = append(arms.Elts, &ast.Field{Label: &ast.ListLit{Elts: []ast.Expr{ast.NewIdent("_")}}, Value: en.catchAll.value})
			}
			val = &ast.CallExpr{Fun: ast.NewIdent(opMatch), Args: []ast.Expr{ref(b.root, en.source), arms}}
		case kindEach:
			val = &ast.CallExpr{Fun: ast.NewIdent(opEach), Args: []ast.Expr{ref(b.root, en.source), en.body.syntax()}}
		}
		f := n.add(en.target, val)
		for _, cg := range en.comments {
			ast.AddComment(f, cg)
		}
	}
	return n.root
}

// lacunasSyntax renders the lens's lacunas field: a list per key, nesting by
// path, keys in order of first appearance.
func (b *dbody) lacunasSyntax() *ast.StructLit {
	n := newNesting()
	lists := make(map[string]*ast.ListLit)
	for _, l := range b.lacunas {
		list, ok := lists[l.path.String()]
		if !ok {
			list = &ast.ListLit{}
			lists[l.path.String()] = list
			n.add(l.path, list)
		}
		elts := []ast.Decl{
			&ast.Field{Label: ast.NewIdent(fieldType), Value: ast.NewIdent(l.typ)},
			&ast.Field{Label: ast.NewIdent(fieldMessage), Value: ast.NewString(l.msg)},
		}
		if l.when != nil {
			elts = append(elts, &ast.Field{Label: ast.NewIdent(fieldWhen), Value: l.when})
		}
		list.Elts = append(list.Elts, &ast.StructLit{Elts: elts})
	}
	return n.root
}
