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
	"github.com/sdboyer/thema/internal/cueview"
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
			bwd.entries = stripDerived(bwd.entries, fwdEntries, v.schema, prev.schema)
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
	lacuna   *lacunaAttr
	// fallback is orelse's fallback; arms and catchAll are remap's table;
	// body is each's body.
	fallback ast.Expr
	arms     []arm
	catchAll *arm
	body     *dbody
}

// A dbody is a lens body read back from the normal form, in result order
// with drops last. root is the normal form's root for references (input or
// item); name is the authoring form's (v0_0 or item).
type dbody struct {
	root, name string
	entries    []dentry
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
	lens, ok := n.(*ast.StructLit)
	if !ok {
		return nil, fmt.Errorf("lens %s is outside the authoring form: it needs imports", key)
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
			case "result":
				result, _ = d.Value.(*ast.StructLit)
			case "lacunas":
				lacunas, _ = d.Value.(*ast.ListLit)
			}
		}
	}
	if result == nil {
		return nil, fmt.Errorf("lens %s is outside the authoring form: its result is not a struct literal", key)
	}
	b := &dbody{root: rootInput, name: paramName(sv)}
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

// walk reads a result struct's declarations into the body.
func (b *dbody) walk(decls []ast.Decl, tbase cue.Path, key string) error {
	var branches []branch
	for _, d := range decls {
		switch d := d.(type) {
		case *ast.CommentGroup:
		case *ast.Field:
			if err := b.field(d, tbase, key); err != nil {
				return err
			}
		case *ast.Comprehension:
			br, ok := singleBranch(d)
			if !ok {
				return fmt.Errorf("lens %s is outside the authoring form: a comprehension in its result that is not a guard or a branch", key)
			}
			branches = append(branches, br)
		default:
			return fmt.Errorf("lens %s is outside the authoring form: %T in its result", key, d)
		}
	}
	return b.branches(branches, tbase, key)
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
		if p.String() == tpath.String() {
			return nil // an implicit copy
		}
		en.kind, en.source = kindMove, p
	} else if isLiteral(f.Value) {
		en.kind = kindConst
	} else if each, ok := b.eachOf(f.Value, key); ok {
		var eerr error
		en.kind, en.source, en.body, eerr = kindEach, each.source, each.body, each.err
		if eerr != nil {
			return eerr
		}
	} else {
		return fmt.Errorf("lens %s is outside the authoring form: %s is computed", key, tpath)
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
	body := &dbody{root: rootItem, name: rootItem}
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

// branches interprets the comprehensions of one struct: a guarded copy or
// move, the two branches of an orelse, or the arms of a remap.
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
				return fmt.Errorf("lens %s is outside the authoring form: the branches of %s are not a remap", key, tpath)
			}
			en.kind, en.source = kindRemap, source
			for i, br := range arms {
				if !isLiteral(br.field.Value) {
					return fmt.Errorf("lens %s is outside the authoring form: remap arm %q of %s is not a constant", key, armKeys[i], tpath)
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
				return fmt.Errorf("lens %s is outside the authoring form: the branches of %s are not an orelse", key, tpath)
			}
			fb := absent[0].field.Value
			if _, _, isRef := refPathAny(fb); !isRef && !isLiteral(fb) {
				return fmt.Errorf("lens %s is outside the authoring form: the fallback of %s is computed", key, tpath)
			}
			en.kind, en.source, en.fallback = kindOrElse, source, fb
		case len(present) == 1 && len(absent) == 0:
			root, p, ok := refPathAny(present[0].field.Value)
			if !ok || root != b.root || p.String() != source.String() {
				return fmt.Errorf("lens %s is outside the authoring form: the guard on %s does not guard its own reference", key, tpath)
			}
			if p.String() == tpath.String() {
				continue // an implicit copy
			}
			en.kind, en.source, en.expr = kindMove, p, present[0].field.Value
		default:
			return fmt.Errorf("lens %s is outside the authoring form: the branches of %s", key, tpath)
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
		body, p := b, srcPaths[0]
		if srcEach != nil {
			// A drop inside an each body: find the each over that list.
			body = nil
			for i := range b.entries {
				if b.entries[i].kind == kindEach && b.entries[i].source.String() == srcEach.String() {
					body = b.entries[i].body
				}
			}
			if body == nil {
				return fmt.Errorf("lens %s is outside the authoring form: a lacuna over %s[], which no each maps", key, srcEach)
			}
		}
		en := dentry{kind: kindDrop, source: p, target: p}
		if msg != dropMessageFor(srcEach, p, tv) {
			en.lacuna = &lacunaAttr{msg: msg}
		}
		body.entries = append(body.entries, en)
		return nil
	}

	a := &lacunaAttr{typ: typ, msg: msg}
	switch {
	case len(tgtPaths) == 1:
		return b.attach(tgtPaths[0], false, a, cond, key)
	case len(srcPaths) == 1 && len(tgtPaths) == 0:
		return b.attach(srcPaths[0], true, a, cond, key)
	}
	return fmt.Errorf("lens %s is outside the authoring form: a lacuna over several fields", key)
}

// attach puts a declared lacuna on the entry producing p (or dropping p),
// at the arm or branch its condition selects.
func (b *dbody) attach(p cue.Path, drop bool, a *lacunaAttr, cond ast.Expr, key string) error {
	for i := range b.entries {
		en := &b.entries[i]
		if (en.kind == kindDrop) != drop {
			continue
		}
		if drop && en.source.String() != p.String() || !drop && en.target.String() != p.String() {
			continue
		}
		var ts []test
		if cond != nil {
			var ok bool
			if ts, ok = b.tests(cond); !ok {
				return fmt.Errorf("lens %s is outside the authoring form: a lacuna on %s with a condition the authoring form cannot express", key, p)
			}
		}
		switch en.kind {
		case kindOrElse:
			if len(ts) != 1 || ts[0].operand != nil || ts[0].present {
				return fmt.Errorf("lens %s is outside the authoring form: a lacuna on %s that is not on its fallback", key, p)
			}
		case kindRemap:
			if len(ts) == 1 && ts[0].present {
				if k, isKey := ts[0].key(); isKey {
					for j := range en.arms {
						if en.arms[j].key == k {
							en.arms[j].lacuna = a
							return nil
						}
					}
					return fmt.Errorf("lens %s is outside the authoring form: a lacuna on %s for an arm it does not have", key, p)
				}
			}
			if len(ts) > 0 && ts[len(ts)-1].operand != nil && !ts[len(ts)-1].present && en.catchAll != nil {
				en.catchAll.lacuna = a
				return nil
			}
			return fmt.Errorf("lens %s is outside the authoring form: a lacuna on %s that is not on an arm", key, p)
		default:
			if len(ts) > 0 {
				return fmt.Errorf("lens %s is outside the authoring form: a lacuna on %s with a condition", key, p)
			}
		}
		if en.lacuna != nil {
			return fmt.Errorf("lens %s is outside the authoring form: two lacunas on %s", key, p)
		}
		en.lacuna = a
		return nil
	}
	return fmt.Errorf("lens %s is outside the authoring form: a lacuna on %s, which no mapping produces", key, p)
}

func stringLit(x ast.Expr) (string, error) {
	lit, ok := x.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", fmt.Errorf("not a string")
	}
	return literal.Unquote(lit.Value)
}

// fieldPaths reads the paths of a lacuna's sourceFields or targetFields: a
// list of {path: "..."} structs, each possibly guarded, or a reference to a
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
// the default-message drop of each field the newer version added.
func stripDerived(entries, fwd []dentry, newer, older cue.Value) []dentry {
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
			if en.lacuna == nil && lookup(newer, en.source).Exists() && !lookup(older, en.source).Exists() {
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
	expr, ok := n.(ast.Expr)
	if !ok {
		return "", fmt.Errorf("%s: schema is outside the authoring form: it needs imports", v.v)
	}
	var parts []string
	for _, block := range []struct {
		name string
		b    *dbody
	}{{fieldSchema, nil}, {fieldForward, fwd}, {fieldBackward, bwd}} {
		val := expr
		if block.name != fieldSchema {
			if block.b == nil || len(block.b.entries) == 0 {
				continue
			}
			from, to := prev, v.v
			if block.name == fieldBackward {
				from, to = v.v, prev
			}
			param := &ast.Field{Label: ast.NewIdent(paramName(from)), Value: ast.NewIdent("_")}
			result := &ast.Field{Label: ast.NewIdent(paramName(to)), Value: block.b.syntax()}
			val = &ast.StructLit{Elts: []ast.Decl{param, result}}
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

// syntax renders the body's entries as a struct, nesting by target path.
func (b *dbody) syntax() *ast.StructLit {
	root := &ast.StructLit{}
	structs := map[string]*ast.StructLit{"": root}
	at := func(p cue.Path) *ast.StructLit {
		sels := p.Selectors()
		s := root
		for i := range sels {
			k := cue.MakePath(sels[:i+1]...).String()
			c, ok := structs[k]
			if !ok {
				c = &ast.StructLit{}
				structs[k] = c
				s.Elts = append(s.Elts, &ast.Field{Label: ast.NewIdent(sels[i].Unquoted()), Value: c})
			}
			s = c
		}
		return s
	}
	for _, en := range b.entries {
		sels := en.target.Selectors()
		parent := at(cue.MakePath(sels[:len(sels)-1]...))
		var val ast.Expr
		switch en.kind {
		case kindMove:
			val = ref(b.name, en.source)
		case kindConst:
			val = en.expr
		case kindDrop:
			val = ast.NewIdent(opDrop)
		case kindOrElse:
			fallback := en.fallback
			if root, p, ok := refPathAny(fallback); ok && root == b.root {
				fallback = ref(b.name, p)
			}
			val = &ast.CallExpr{Fun: ast.NewIdent(opOrElse), Args: []ast.Expr{ref(b.name, en.source), fallback}}
		case kindRemap:
			table := &ast.StructLit{}
			for _, a := range en.arms {
				f := &ast.Field{Label: ast.NewString(a.key), Value: a.value}
				addLacuna(f, a.lacuna)
				table.Elts = append(table.Elts, f)
			}
			if en.catchAll != nil {
				f := &ast.Field{Label: &ast.ListLit{Elts: []ast.Expr{ast.NewIdent("_")}}, Value: en.catchAll.value}
				addLacuna(f, en.catchAll.lacuna)
				table.Elts = append(table.Elts, f)
			}
			val = &ast.CallExpr{Fun: ast.NewIdent(opRemap), Args: []ast.Expr{ref(b.name, en.source), table}}
		case kindEach:
			val = &ast.CallExpr{Fun: ast.NewIdent(opEach), Args: []ast.Expr{ref(b.name, en.source), en.body.syntax()}}
		}
		f := &ast.Field{Label: ast.NewIdent(sels[len(sels)-1].Unquoted()), Value: val}
		addLacuna(f, en.lacuna)
		for _, cg := range en.comments {
			ast.AddComment(f, cg)
		}
		parent.Elts = append(parent.Elts, f)
	}
	return root
}

func addLacuna(f *ast.Field, a *lacunaAttr) {
	if a == nil {
		return
	}
	text := "@lacuna(" + literal.String.Quote(a.msg) + ")"
	if a.typ != "" {
		text = "@lacuna(" + a.typ + ", " + literal.String.Quote(a.msg) + ")"
	}
	f.Attrs = append(f.Attrs, &ast.Attribute{Text: text})
}
