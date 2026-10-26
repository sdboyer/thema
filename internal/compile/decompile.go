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
				fwd, err = decompileLens(l, fkey, v.v)
				if err != nil {
					return "", nil, err
				}
				fwdEntries = fwd.entries
			}
			l, ok := lenses[bkey]
			if !ok {
				return "", nil, fmt.Errorf("no lens at %s", bkey)
			}
			bwd, err = decompileLens(l, bkey, prev.v)
			if err != nil {
				return "", nil, err
			}
			bwd.entries = stripDerived(bwd.entries, fwdEntries, v.schema, prev.schema)
		}
		text, err := versionText(v, fwd, bwd)
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
}

// A dbody is a lens body read back from the normal form, in result order
// with drops last.
type dbody struct {
	entries []dentry
}

// decompileLens reads the mappings of the lens at key, whose target version
// is tv.
func decompileLens(l cue.Value, key string, tv thema.SyntacticVersion) (*dbody, error) {
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
	var lit *ast.StructLit
	for _, d := range lens.Elts {
		if f, ok := d.(*ast.Field); ok {
			if name, _, _ := ast.LabelName(f.Label); name == "result" {
				lit, _ = f.Value.(*ast.StructLit)
			}
		}
	}
	if lit == nil {
		return nil, fmt.Errorf("lens %s is outside the authoring form: its result is not a struct literal", key)
	}
	b := &dbody{}
	if werr := b.walk(lit.Elts, cue.MakePath(), key); werr != nil {
		return nil, werr
	}

	lacunas, err := l.LookupPath(cue.MakePath(cue.Str("lacunas"))).List()
	if err == nil {
		for lacunas.Next() {
			if err := b.lacuna(lacunas.Value(), key, tv); err != nil {
				return nil, err
			}
		}
	}
	return b, nil
}

func (b *dbody) walk(decls []ast.Decl, tbase cue.Path, key string) error {
	for _, d := range decls {
		f, ok := d.(*ast.Field)
		if !ok {
			if c, ok := d.(*ast.Comprehension); ok {
				f, ok = guardedField(c)
				if !ok {
					return fmt.Errorf("lens %s is outside the authoring form: a comprehension other than an optional field's guard", key)
				}
			} else {
				if _, ok := d.(*ast.CommentGroup); ok {
					continue
				}
				return fmt.Errorf("lens %s is outside the authoring form: %T in its result", key, d)
			}
		}
		name, _, err := ast.LabelName(f.Label)
		if err != nil {
			return fmt.Errorf("lens %s is outside the authoring form: a field whose label is not a name", key)
		}
		tpath := appendPath(tbase, cue.Str(name))
		if s, ok := f.Value.(*ast.StructLit); ok {
			if err := b.walk(s.Elts, tpath, key); err != nil {
				return err
			}
			continue
		}
		en := dentry{target: tpath, expr: f.Value, comments: ast.Comments(f)}
		if p, ok := refPath(f.Value); ok {
			if p.String() == tpath.String() {
				continue // an implicit copy
			}
			en.kind, en.source = kindMove, p
		} else if isLiteral(f.Value) {
			en.kind = kindConst
		} else {
			return fmt.Errorf("lens %s is outside the authoring form: %s is computed", key, tpath)
		}
		b.entries = append(b.entries, en)
	}
	return nil
}

// guardedField returns the field of `if input.p != _|_ {q: input.p}`.
func guardedField(c *ast.Comprehension) (*ast.Field, bool) {
	if len(c.Clauses) != 1 {
		return nil, false
	}
	ifc, ok := c.Clauses[0].(*ast.IfClause)
	if !ok {
		return nil, false
	}
	bin, ok := ifc.Condition.(*ast.BinaryExpr)
	if !ok || bin.Op != token.NEQ {
		return nil, false
	}
	if _, isBottom := bin.Y.(*ast.BottomLit); !isBottom {
		return nil, false
	}
	guard, ok := refPath(bin.X)
	if !ok {
		return nil, false
	}
	s, ok := c.Value.(*ast.StructLit)
	if !ok || len(s.Elts) != 1 {
		return nil, false
	}
	f, ok := s.Elts[0].(*ast.Field)
	if !ok {
		return nil, false
	}
	if p, ok := refPath(f.Value); !ok || p.String() != guard.String() {
		return nil, false
	}
	return f, true
}

// lacuna reads one lacuna: a derived DroppedField becomes a drop, and a
// declared lacuna attaches to the mapping it names.
func (b *dbody) lacuna(l cue.Value, key string, tv thema.SyntacticVersion) error {
	derived := false
	for _, a := range l.Attributes(cue.ValueAttr) {
		if a.Name() == "thema" {
			derived, _ = a.Flag(0, "derived")
		}
	}
	typ, err := l.LookupPath(cue.MakePath(cue.Str("type"))).String()
	if err != nil {
		return fmt.Errorf("lens %s: a lacuna's type is not a string", key)
	}
	msg, err := l.LookupPath(cue.MakePath(cue.Str("message"))).String()
	if err != nil {
		return fmt.Errorf("lens %s: a lacuna's message is not a string", key)
	}
	sources, err := fieldPaths(l.LookupPath(cue.MakePath(cue.Str("sourceFields"))))
	if err != nil {
		return fmt.Errorf("lens %s: %w", key, err)
	}
	targets, err := fieldPaths(l.LookupPath(cue.MakePath(cue.Str("targetFields"))))
	if err != nil {
		return fmt.Errorf("lens %s: %w", key, err)
	}
	cond := l.LookupPath(cue.MakePath(cue.Str("condition")))
	_, hasDefault := cond.Default()
	if !cond.Exists() {
		hasDefault = true
	}

	switch {
	case derived && typ == lacunaDropped && len(sources) == 1 && len(targets) == 0:
		en := dentry{kind: kindDrop, source: sources[0], target: sources[0]}
		if msg != dropMessage(sources[0], tv) {
			en.lacuna = &lacunaAttr{msg: msg}
		}
		b.entries = append(b.entries, en)
		return nil
	case derived:
		return fmt.Errorf("lens %s is outside the authoring form: a derived lacuna of type %s", key, typ)
	case !hasDefault && cond.Validate(cue.Concrete(true)) != nil:
		return fmt.Errorf("lens %s is outside the authoring form: a lacuna with a condition", key)
	case len(targets) == 1 && len(sources) == 0:
		return b.attach(targets[0], false, &lacunaAttr{typ: typ, msg: msg}, key)
	case len(sources) == 1 && len(targets) == 0:
		return b.attach(sources[0], true, &lacunaAttr{typ: typ, msg: msg}, key)
	}
	return fmt.Errorf("lens %s is outside the authoring form: a lacuna over several fields", key)
}

func (b *dbody) attach(p cue.Path, drop bool, a *lacunaAttr, key string) error {
	for i := range b.entries {
		en := &b.entries[i]
		if (en.kind == kindDrop) != drop {
			continue
		}
		if (drop && en.source.String() == p.String()) || (!drop && en.target.String() == p.String()) {
			if en.lacuna != nil {
				return fmt.Errorf("lens %s is outside the authoring form: two lacunas on %s", key, p)
			}
			en.lacuna = a
			return nil
		}
	}
	return fmt.Errorf("lens %s is outside the authoring form: a lacuna on %s, which no mapping produces", key, p)
}

func fieldPaths(list cue.Value) ([]cue.Path, error) {
	if !list.Exists() {
		return nil, nil
	}
	iter, err := list.List()
	if err != nil {
		return nil, fmt.Errorf("a lacuna's fields are not a list")
	}
	var out []cue.Path
	for iter.Next() {
		s, err := iter.Value().LookupPath(cue.MakePath(cue.Str("path"))).String()
		if err != nil {
			return nil, fmt.Errorf("a lacuna field's path is not a string")
		}
		out = append(out, cue.ParsePath(s))
	}
	return out, nil
}

func dropMessage(p cue.Path, tv thema.SyntacticVersion) string {
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

// versionText renders a version file.
func versionText(v dversion, fwd, bwd *dbody) (string, error) {
	n, err := cueview.Documented(v.schema)
	if err != nil {
		return "", fmt.Errorf("%s: schema: %w", v.v, err)
	}
	expr, ok := n.(ast.Expr)
	if !ok {
		return "", fmt.Errorf("%s: schema is outside the authoring form: it needs imports", v.v)
	}
	// The exported syntax has no layout; give every field a line of its own.
	ast.Walk(expr, func(n ast.Node) bool {
		if f, ok := n.(*ast.Field); ok {
			ast.SetPos(f, token.NoPos.WithRel(token.Newline))
		}
		return true
	}, nil)
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
			val = block.b.syntax()
		}
		text, err := format.Node(&ast.Field{Label: ast.NewIdent(block.name), Value: val})
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
				nf := &ast.Field{Label: ast.NewIdent(sels[i].Unquoted()), Value: c}
				ast.SetPos(nf, token.NoPos.WithRel(token.Newline))
				s.Elts = append(s.Elts, nf)
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
			val = inputRef(en.source)
		case kindConst:
			val = en.expr
		case kindDrop:
			val = ast.NewIdent("drop")
		}
		f := &ast.Field{Label: ast.NewIdent(sels[len(sels)-1].Unquoted()), Value: val}
		ast.SetPos(f, token.NoPos.WithRel(token.Newline))
		if en.lacuna != nil {
			text := "@lacuna(" + literal.String.Quote(en.lacuna.msg) + ")"
			if en.lacuna.typ != "" {
				text = "@lacuna(" + en.lacuna.typ + ", " + literal.String.Quote(en.lacuna.msg) + ")"
			}
			f.Attrs = append(f.Attrs, &ast.Attribute{Text: text})
		}
		for _, cg := range en.comments {
			ast.AddComment(f, cg)
		}
		parent.Elts = append(parent.Elts, f)
	}
	return root
}
