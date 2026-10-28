package lenscheck

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/internal/lensir"
	"github.com/sdboyer/thema/lacuna"
)

// irBuilder builds the lens's program (internal/lensir) as the walk proves
// it: each proof site emits the node for what it proved, into the branch,
// head arm, table entry or lacuna being walked. Nodes carry what the proof
// established (paths, patterns, resolved operands), never a second reading
// of the lens. Check keeps the program only where the lens is proven total;
// a construct the IR cannot yet carry is recorded in err, and the lens binds
// without a program.
type irBuilder struct {
	w *walker
	// stack holds the containers nodes are emitted into: the result at the
	// bottom, then each open branch, arm, entry or lacuna message.
	stack   []*[]lensir.Node
	lacunas []lensir.Lacuna
	// lacuna is the lacuna being walked, if any.
	lacuna *lensir.Lacuna
	err    error
}

func newIRBuilder(w *walker) *irBuilder {
	b := &irBuilder{w: w}
	b.push()
	return b
}

// failf records why the program cannot be built; the first reason is kept.
func (b *irBuilder) failf(format string, args ...any) {
	if b.err == nil {
		b.err = fmt.Errorf(format, args...)
	}
}

func (b *irBuilder) push() {
	b.stack = append(b.stack, new([]lensir.Node))
}

func (b *irBuilder) pop() []lensir.Node {
	top := b.stack[len(b.stack)-1]
	b.stack = b.stack[:len(b.stack)-1]
	return *top
}

// emit adds n to the open container. A hidden result field is not data, and
// nothing is emitted for it.
func (b *irBuilder) emit(n lensir.Node) {
	if b.w.hidden > 0 {
		return
	}
	top := b.stack[len(b.stack)-1]
	*top = append(*top, n)
}

// result returns the nodes of the result, once the walk is done.
func (b *irBuilder) result() []lensir.Node {
	return *b.stack[0]
}

// path converts a path within the input or the result; the open element of a
// list, inside an each, is Any.
func (b *irBuilder) path(p cue.Path) lensir.Path {
	out := make(lensir.Path, 0, len(p.Selectors()))
	for _, sel := range p.Selectors() {
		switch {
		case isAnyIndex(sel):
			out = append(out, lensir.Any())
		case sel.Type().LabelType() == cue.IndexLabel:
			out = append(out, lensir.Index(sel.Index()))
		case sel.IsString():
			out = append(out, lensir.Field(sel.Unquoted()))
		default:
			b.failf("path %s has a selector the IR does not carry: %s", p, sel)
		}
	}
	return out
}

// literal converts a scalar value.
func (b *irBuilder) literal(v cue.Value) lensir.Literal {
	switch v.Kind() {
	case cue.NullKind:
		return lensir.Literal{Kind: lensir.Null}
	case cue.BoolKind:
		t, _ := v.Bool()
		return lensir.Literal{Kind: lensir.Bool, Bool: t}
	case cue.IntKind:
		n, _ := v.Int(nil)
		return lensir.Literal{Kind: lensir.Int, Int: n.String()}
	case cue.FloatKind:
		return lensir.Literal{Kind: lensir.Float, Float: fmtv(v)}
	case cue.StringKind:
		s, _ := v.String()
		return lensir.Literal{Kind: lensir.String, Str: s}
	case cue.BytesKind:
		bs, _ := v.Bytes()
		return lensir.Literal{Kind: lensir.Bytes, Str: string(bs)}
	}
	b.failf("%s is not a scalar literal", fmtv(v))
	return lensir.Literal{}
}

// tests converts the patterns a write or lacuna is under.
func (b *irBuilder) tests(gs []pattern) []lensir.Test {
	out := make([]lensir.Test, 0, len(gs))
	for _, g := range gs {
		t := lensir.Test{Path: b.path(g.path), Neg: g.neg}
		switch g.kind {
		case presence:
			t.Kind = lensir.Presence
		case equality:
			t.Kind, t.Value, t.Safe = lensir.Equality, b.literal(g.val), g.safe
		case typeTest:
			t.Kind, t.Type = lensir.TypeTest, g.lit
		default:
			b.failf("condition %s is not a pattern", g.text)
			continue
		}
		out = append(out, t)
	}
	return out
}

// value converts the nodes written for a scalar position (a lacuna's
// message) to the expression they evaluate.
func (b *irBuilder) value(nodes []lensir.Node) lensir.Expr {
	if len(nodes) != 1 {
		b.failf("a lacuna's message is written %d times", len(nodes))
		return nil
	}
	switch n := nodes[0].(type) {
	case *lensir.Copy:
		return &lensir.Ref{Path: n.From}
	case *lensir.Constant:
		return n.Value
	case *lensir.Computed:
		return n.Expr
	}
	b.failf("a lacuna's message is a %T, which the IR carries only as a result field", nodes[0])
	return nil
}

// operandRes is what the proof resolved an operand to: a selection into the
// result, or the input paths and expressions its reference reached.
type operandRes struct {
	result *cue.Path
	alts   []operandAlt
}

type operandAlt struct {
	input *cue.Path
	expr  ast.Expr
}

// text names the alternative, for telling duplicates apart.
func (a operandAlt) text() string {
	if a.input != nil {
		return "input:" + a.input.String()
	}
	return text(a.expr)
}

// expr converts the computed expression x, as the proof analyzed it (c):
// operators are read from x, operands from what the proof resolved them to,
// and a comparison the proof classified as a pattern is a Match.
func (b *irBuilder) expr(x ast.Expr, c *exprCtx) lensir.Expr {
	if r, ok := c.resolved[x]; ok {
		return b.operand(x, r, c)
	}
	switch e := x.(type) {
	case *ast.ParenExpr:
		return b.expr(e.X, c)
	case *ast.BasicLit:
		return b.literal(b.w.ctx.BuildExpr(e))
	case *ast.Ident:
		if b.w.predeclared(e) && isBoolName(e.Name) {
			return lensir.Literal{Kind: lensir.Bool, Bool: e.Name == trueName}
		}
	case *ast.UnaryExpr:
		return &lensir.Unary{Op: e.Op.String(), X: b.expr(e.X, c)}
	case *ast.BinaryExpr:
		if gs, ok := c.matches[e]; ok {
			return &lensir.Match{Tests: b.tests(gs)}
		}
		return &lensir.Binary{Op: e.Op.String(), X: b.expr(e.X, c), Y: b.expr(e.Y, c)}
	case *ast.Interpolation:
		return b.interpolation(e, c)
	case *ast.CallExpr:
		call := &lensir.Call{}
		switch fun := e.Fun.(type) {
		case *ast.Ident:
			call.Fn = fun.Name
		case *ast.SelectorExpr:
			pkg, ok := fun.X.(*ast.Ident)
			if !ok {
				break
			}
			spec, ok := pkg.Node.(*ast.ImportSpec)
			if !ok {
				break
			}
			path, _ := literal.Unquote(spec.Path.Value)
			name, _, err := ast.LabelName(fun.Sel)
			if err == nil {
				call.Fn = path + "." + name
			}
		}
		if call.Fn == "" {
			break
		}
		for _, a := range e.Args {
			call.Args = append(call.Args, b.expr(a, c))
		}
		return call
	}
	b.failf("%s is not an expression the IR carries", text(x))
	return nil
}

// operand converts an operand from what the proof resolved it to: a result
// field is read from the result; a single input path or expression is what it
// is; several that are the same (a field declared twice with one value) are
// that; otherwise the operand has no one value the IR can name.
func (b *irBuilder) operand(x ast.Expr, r operandRes, c *exprCtx) lensir.Expr {
	if r.result != nil {
		return &lensir.ResultRef{Path: b.path(*r.result)}
	}
	if len(r.alts) == 0 {
		b.failf("%s resolves to nothing", text(x))
		return nil
	}
	for _, a := range r.alts[1:] {
		if a.text() != r.alts[0].text() {
			b.failf("%s resolves to several values (%s and %s), which the IR carries only as one", text(x), r.alts[0].text(), a.text())
			return nil
		}
	}
	a := r.alts[0]
	if a.input != nil {
		return &lensir.Ref{Path: b.path(*a.input)}
	}
	return b.expr(a.expr, c)
}

// interpolation converts an interpolation: its string fragments, unquoted as
// CUE unquotes them (each fragment carries the quotes or the \( and ) around
// it), alternating with its expressions.
func (b *irBuilder) interpolation(e *ast.Interpolation, c *exprCtx) lensir.Expr {
	first, last := e.Quotes()
	info, prefixLen, _, err := literal.ParseQuotes(first.Value, last.Value)
	if err != nil {
		b.failf("interpolation %s: %v", text(e), err)
		return nil
	}
	kind := lensir.String
	if !info.IsDouble() {
		kind = lensir.Bytes
	}
	out := &lensir.Interp{}
	for i := 0; i < len(e.Elts); i += 2 {
		l, ok := e.Elts[i].(*ast.BasicLit)
		if !ok || len(l.Value) < prefixLen {
			b.failf("interpolation %s has a fragment the IR cannot read: %s", text(e), text(e.Elts[i]))
			return nil
		}
		s, err := info.Unquote(l.Value[prefixLen:])
		if err != nil {
			b.failf("interpolation %s: %v", text(e), err)
			return nil
		}
		out.Parts = append(out.Parts, lensir.Literal{Kind: kind, Str: s})
		if i+1 < len(e.Elts) {
			out.Parts = append(out.Parts, b.expr(e.Elts[i+1], c))
		}
		prefixLen = 1 // the ) closing the preceding \(
	}
	return out
}

// branch emits the nodes walked under the patterns gs, which were pushed
// before the walk, as a Branch.
func (b *irBuilder) branch(gs []pattern) {
	b.emit(&lensir.Branch{When: b.tests(gs), Then: b.pop()})
}

// beginLacuna starts the lacuna at lp; endLacuna finishes it, reading its
// type from the lens, which the walk has checked is a literal.
func (b *irBuilder) beginLacuna() {
	b.lacuna = &lensir.Lacuna{}
}

func (b *irBuilder) endLacuna(lp cue.Path) {
	tp := appendPath(lp, cue.Str("type"))
	s, err := b.w.lens.LookupPath(tp).String()
	if err != nil {
		b.failf("%s is not a string: %v", tp, err)
	}
	b.lacuna.Type = lacuna.Type(s)
	b.lacunas = append(b.lacunas, *b.lacuna)
	b.lacuna = nil
}

// condition records a lacuna's condition from the parts of it the walk
// proved: a literal false means never; patterns and guards are conjoined;
// nothing means always.
func (b *irBuilder) condition(when []lensir.Expr, never bool) {
	if never {
		b.lacuna.When = lensir.Literal{Kind: lensir.Bool}
		return
	}
	for _, x := range when {
		if b.lacuna.When == nil {
			b.lacuna.When = x
			continue
		}
		b.lacuna.When = &lensir.Binary{Op: token.LAND.String(), X: b.lacuna.When, Y: x}
	}
}

// fieldRef records a field a lacuna reports, in the list name, with the path
// the lens declares at rp.
func (b *irBuilder) fieldRef(name string, rp cue.Path, value lensir.Expr) {
	pp := appendPath(rp, cue.Str("path"))
	p, err := b.w.lens.LookupPath(pp).String()
	if err != nil {
		b.failf("%s is not a string: %v", pp, err)
	}
	b.addFieldRef(name, lensir.FieldRef{Path: p, Value: value})
}

// fieldRefUnder records a field a lacuna reports only where the patterns gs
// hold (an entry written as an if comprehension). The entry does not evaluate
// without an input, so its path is read from the syntax, where it must be a
// string literal.
func (b *irBuilder) fieldRefUnder(name string, rp cue.Path, path ast.Expr, gs []pattern, value lensir.Expr) {
	lit, ok := path.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		b.failf("%s has a path that is not a string literal, which the IR carries only for an entry written unconditionally", rp)
		return
	}
	p, err := literal.Unquote(lit.Value)
	if err != nil {
		b.failf("%s: %v", appendPath(rp, cue.Str("path")), err)
		return
	}
	b.addFieldRef(name, lensir.FieldRef{Path: p, When: b.tests(gs), Value: value})
}

func (b *irBuilder) addFieldRef(name string, ref lensir.FieldRef) {
	if strings.HasPrefix(name, "source") {
		b.lacuna.SourceFields = append(b.lacuna.SourceFields, ref)
	} else {
		b.lacuna.TargetFields = append(b.lacuna.TargetFields, ref)
	}
}
