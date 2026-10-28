package thema

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/cueview"
	"github.com/sdboyer/thema/lacuna"
)

// This file holds the part of translation that carries data no lens touches
// (#193, settled in #48). Around each step, translation takes from the
// instance:
//
//   - its extras: the fields its schema does not declare, which an open
//     struct's ... admitted;
//   - for a step with a lens, the fields its schema declares, the target
//     schema does not, and the lens does not map: neither reads nor reports
//     in a lacuna (a written _drop is a DroppedField lacuna).
//
// The lens runs on the instance without its extras, since a lens reads and
// writes only declared fields. Each field taken is then placed in the step's
// result at the same path, if the result is still an instance of the target
// schema with it there: carried where the target struct is open and does not
// declare it, promoted where the target declares it and the value is an
// instance of what it declares. A promoted value replaces whatever the lens
// wrote there: the lens wrote it for instances that lacked the field. Any
// other field is set aside: dropped, with a DroppedField lacuna for each
// struct and cause, naming each field and its value. Translation never fails
// because of a field taken this way (#57).

// A taken field is a field translation carries itself rather than leaving to
// a lens.
type taken struct {
	path  cue.Path
	value cue.Value
	// unmapped marks a field the source schema declares and the lens leaves
	// alone. Where it cannot be carried, the lens's leaving it is the lens's
	// own drop, as before carrying: no lacuna is added for it. (The authoring
	// form never leaves one so: its derived lens drops, with a DroppedField
	// lacuna, any such field it does not carry.)
	unmapped bool
}

// Why a taken field was set aside. Each struct gets one lacuna per cause.
type asideCause int

const (
	// asideClosed: the target does not declare the field, and its struct
	// there is closed.
	asideClosed asideCause = iota
	// asideConflict: the target declares the field, and the value is not an
	// instance of what it declares.
	asideConflict
	// asideNowhere: the result has no struct at the field's parent path to
	// carry it in.
	asideNowhere
)

// takeExtras returns data without the fields sch does not declare, and those
// fields. The fields are found as bind finds undeclared data
// (cuetil.UndeclaredFields): by name, at every depth, an open struct's ...
// declaring nothing.
func takeExtras(ctx *cue.Context, data, sch cue.Value) (cue.Value, []taken, error) {
	paths := cuetil.UndeclaredFields(ctx, data, sch)
	if len(paths) == 0 {
		return data, nil, nil
	}
	x, err := dataSyntax(data)
	if err != nil {
		return cue.Value{}, nil, err
	}
	out := make([]taken, 0, len(paths))
	for _, p := range paths {
		out = append(out, taken{path: p, value: data.LookupPath(p)})
		x = withoutAt(x, p.Selectors())
	}
	return ctx.BuildExpr(x), out, nil
}

// takeFields returns, in the order data holds them, extras (data's fields
// from does not declare, as takeExtras found them) and, for a step with a
// lens, the fields of data that from declares and to does not and that the
// step's result does not have: the candidates for carrying, marked unmapped,
// of which the caller keeps those the lens does not map. A field both
// declare is descended into; one only from declares is taken whole.
func takeFields(ctx *cue.Context, data cue.Value, extras []taken, result, from, to cue.Value, lens bool) []taken {
	if !lens {
		return extras
	}
	byPath := make(map[string]taken, len(extras))
	for _, x := range extras {
		byPath[x.path.String()] = x
	}
	var out []taken
	var walk func(v cue.Value, p cue.Path)
	walk = func(v cue.Value, p cue.Path) {
		switch v.IncompleteKind() {
		case cue.StructKind:
			iter, err := v.Fields()
			if err != nil {
				return
			}
			for iter.Next() {
				q := appendSel(p, iter.Selector())
				if x, ok := byPath[q.String()]; ok {
					out = append(out, x)
					continue
				}
				if !cuetil.DeclaresPath(ctx, from, q) {
					continue
				}
				if cuetil.DeclaresPath(ctx, to, q) {
					walk(iter.Value(), q)
					continue
				}
				if !hasDataPath(result, q) {
					out = append(out, taken{path: q, value: iter.Value(), unmapped: true})
				}
			}
		case cue.ListKind:
			iter, err := v.List()
			if err != nil {
				return
			}
			for i := 0; iter.Next(); i++ {
				walk(iter.Value(), appendSel(p, cue.Index(i)))
			}
		}
	}
	walk(data, cue.MakePath())
	return out
}

// carry places each field of fields, taken from in, into result, an instance
// of to's schema produced by the step from version fromV, and returns the
// result and the lacunas for the fields set aside.
func carry(ctx *cue.Context, in, result cue.Value, to *schemaDef, fromV SyntacticVersion, fields []taken) (cue.Value, []lacuna.Lacuna, error) {
	if len(fields) == 0 {
		return result, nil, nil
	}
	rx, err := dataSyntax(result)
	if err != nil {
		return cue.Value{}, nil, diag.InternalErr(err)
	}
	inx, err := dataSyntax(in)
	if err != nil {
		return cue.Value{}, nil, diag.InternalErr(err)
	}
	closed := to.closed()

	type group struct {
		parent cue.Path
		cause  asideCause
		refs   []lacuna.FieldRef
	}
	var groups []*group
	byKey := map[string]*group{}
	placed := false
	for _, f := range fields {
		sels := f.path.Selectors()
		parent := sels[:len(sels)-1]
		cause := asideNowhere
		if sameLists(inx, rx, parent) && isStructLit(lookupSyntax(rx, parent)) {
			vx, err := dataSyntax(f.value)
			if err != nil {
				return cue.Value{}, nil, diag.InternalErr(err)
			}
			cand := withAt(rx, sels, vx)
			if closed.Unify(ctx.BuildExpr(cand)).Validate(cue.Concrete(true)) == nil {
				rx, placed = cand, true
				continue
			}
			cause = asideClosed
			if cuetil.DeclaresPath(ctx, to.def, f.path) {
				cause = asideConflict
			}
		}
		if f.unmapped {
			continue
		}
		var val interface{}
		if err := f.value.Decode(&val); err != nil {
			return cue.Value{}, nil, diag.InternalErr(err)
		}
		key := fmt.Sprintf("%d %s", cause, cue.MakePath(parent...))
		g, ok := byKey[key]
		if !ok {
			g = &group{parent: cue.MakePath(parent...), cause: cause}
			byKey[key] = g
			groups = append(groups, g)
		}
		g.refs = append(g.refs, lacuna.FieldRef{Path: f.path.String(), Value: val})
	}

	if placed {
		result = ctx.BuildExpr(rx)
	}
	lacs := make([]lacuna.Lacuna, 0, len(groups))
	for _, g := range groups {
		lacs = append(lacs, lacuna.Lacuna{
			SourceFields: g.refs,
			Type:         lacuna.DroppedField,
			Message:      asideMessage(g.refs, g.parent, g.cause, fromV, to.Version()),
		})
	}
	return result, lacs, nil
}

// asideMessage says why the fields refs, under parent, were set aside on the
// step from fromV to toV.
func asideMessage(refs []lacuna.FieldRef, parent cue.Path, cause asideCause, fromV, toV SyntacticVersion) string {
	names := make([]string, len(refs))
	for i, r := range refs {
		names[i] = r.Path
	}
	subject := strings.Join(names, ", ")
	where := "the instance"
	if len(parent.Selectors()) > 0 {
		where = parent.String()
	}
	plural := len(refs) > 1
	pick := func(one, many string) string {
		if plural {
			return many
		}
		return one
	}
	switch cause {
	case asideClosed:
		return fmt.Sprintf("%s set aside: %s does not declare %s in %s, which it does not leave open", subject, toV, pick("it", "them"), where)
	case asideConflict:
		return fmt.Sprintf("%s set aside: %s %s not %s of what %s declares for %s", subject, pick("its value", "their values"), pick("is", "are"), pick("an instance", "instances"), toV, pick("it", "them"))
	}
	return fmt.Sprintf("%s set aside: translating from %s to %s produces no %s to carry %s in", subject, fromV, toV, where, pick("it", "them"))
}

// closed returns the schema closed recursively, as Validate checks data
// against it.
func (sch *schemaDef) closed() cue.Value {
	sch.rt().rl()
	defer sch.rt().ru()
	return cuetil.CloseRecursive(sch.rt().Context(), sch.def)
}

// pathsOverlap reports whether one of a and b is a prefix of the other, a
// list's open element (cue.AnyIndex) matching every index.
func pathsOverlap(a, b cue.Path) bool {
	as, bs := a.Selectors(), b.Selectors()
	n := min(len(as), len(bs))
	for i := 0; i < n; i++ {
		if !selMatch(as[i], bs[i]) {
			return false
		}
	}
	return true
}

func selMatch(a, b cue.Selector) bool {
	if a.Type().LabelType() == cue.IndexLabel && b.Type().LabelType() == cue.IndexLabel {
		if isAnyIndex(a) || isAnyIndex(b) {
			return true
		}
		return a.Index() == b.Index()
	}
	if a.IsString() && b.IsString() {
		return a.Unquoted() == b.Unquoted()
	}
	return false
}

func isAnyIndex(sel cue.Selector) bool {
	return sel.Type().LabelType() == cue.IndexLabel && sel.ConstraintType() == cue.PatternConstraint
}

func appendSel(p cue.Path, sel cue.Selector) cue.Path {
	return cue.MakePath(append(p.Selectors(), sel)...)
}

// hasDataPath reports whether the data v has a regular field or element at
// every step of p. Unlike LookupPath, it never finds a name in a struct that
// merely admits it.
func hasDataPath(v cue.Value, p cue.Path) bool {
	for _, sel := range p.Selectors() {
		switch {
		case sel.IsString():
			iter, err := v.Fields()
			if err != nil {
				return false
			}
			found := false
			for iter.Next() {
				if s := iter.Selector(); s.IsString() && s.Unquoted() == sel.Unquoted() {
					v, found = iter.Value(), true
					break
				}
			}
			if !found {
				return false
			}
		case sel.Type().LabelType() == cue.IndexLabel:
			n, err := v.Len().Int64()
			if err != nil || int64(sel.Index()) >= n {
				return false
			}
			v = v.LookupPath(cue.MakePath(sel))
		default:
			return false
		}
	}
	return true
}

// dataSyntax returns the concrete data v as syntax, to be edited and built
// again: the Data view, which keeps field order and the kinds of values
// (bytes, floats) that JSON would not.
func dataSyntax(v cue.Value) (ast.Expr, error) {
	n, err := cueview.Data(v)
	if err != nil {
		return nil, err
	}
	x, ok := n.(ast.Expr)
	if !ok {
		return nil, fmt.Errorf("data exported as %T, not an expression", n)
	}
	return x, nil
}

// lookupSyntax returns the value of data syntax x at the path sels, or nil.
func lookupSyntax(x ast.Expr, sels []cue.Selector) ast.Expr {
	for _, sel := range sels {
		switch e := x.(type) {
		case *ast.StructLit:
			_, f := fieldOf(e, sel)
			if f == nil {
				return nil
			}
			x = f.Value
		case *ast.ListLit:
			if sel.Type().LabelType() != cue.IndexLabel || sel.Index() >= len(e.Elts) {
				return nil
			}
			x = e.Elts[sel.Index()]
		default:
			return nil
		}
	}
	return x
}

// sameLists reports whether every list along sels has as many elements in a
// as in b, so that an index means the same element in both.
func sameLists(a, b ast.Expr, sels []cue.Selector) bool {
	for i, sel := range sels {
		if sel.Type().LabelType() != cue.IndexLabel {
			continue
		}
		la, aok := lookupSyntax(a, sels[:i]).(*ast.ListLit)
		lb, bok := lookupSyntax(b, sels[:i]).(*ast.ListLit)
		if !aok || !bok || len(la.Elts) != len(lb.Elts) {
			return false
		}
	}
	return true
}

func isStructLit(x ast.Expr) bool {
	_, ok := x.(*ast.StructLit)
	return ok
}

// fieldOf returns the index and declaration of the field sel names in s.
func fieldOf(s *ast.StructLit, sel cue.Selector) (int, *ast.Field) {
	if !sel.IsString() {
		return -1, nil
	}
	for i, d := range s.Elts {
		f, ok := d.(*ast.Field)
		if !ok {
			continue
		}
		if name, _, err := ast.LabelName(f.Label); err == nil && name == sel.Unquoted() {
			return i, f
		}
	}
	return -1, nil
}

// withAt returns data syntax x with the value at sels set to val: replaced
// where it exists, otherwise added as the last field of its struct. Each
// struct and list along sels is copied, so x itself is unchanged; sels's
// parent must exist.
func withAt(x ast.Expr, sels []cue.Selector, val ast.Expr) ast.Expr {
	if len(sels) == 0 {
		return val
	}
	sel := sels[0]
	switch e := x.(type) {
	case *ast.StructLit:
		out := *e
		out.Elts = append([]ast.Decl(nil), e.Elts...)
		if i, f := fieldOf(e, sel); f != nil {
			nf := *f
			nf.Value = withAt(f.Value, sels[1:], val)
			out.Elts[i] = &nf
		} else if len(sels) == 1 {
			out.Elts = append(out.Elts, &ast.Field{Label: ast.NewString(sel.Unquoted()), Value: val})
		}
		return &out
	case *ast.ListLit:
		if sel.Type().LabelType() != cue.IndexLabel || sel.Index() >= len(e.Elts) {
			return x
		}
		out := *e
		out.Elts = append([]ast.Expr(nil), e.Elts...)
		out.Elts[sel.Index()] = withAt(e.Elts[sel.Index()], sels[1:], val)
		return &out
	}
	return x
}

// withoutAt returns data syntax x without the field at sels, copying each
// struct and list along it as withAt does.
func withoutAt(x ast.Expr, sels []cue.Selector) ast.Expr {
	if len(sels) == 0 {
		return x
	}
	sel := sels[0]
	switch e := x.(type) {
	case *ast.StructLit:
		i, f := fieldOf(e, sel)
		if f == nil {
			return x
		}
		out := *e
		if len(sels) == 1 {
			out.Elts = append(append([]ast.Decl(nil), e.Elts[:i]...), e.Elts[i+1:]...)
			return &out
		}
		out.Elts = append([]ast.Decl(nil), e.Elts...)
		nf := *f
		nf.Value = withoutAt(f.Value, sels[1:])
		out.Elts[i] = &nf
		return &out
	case *ast.ListLit:
		if sel.Type().LabelType() != cue.IndexLabel || sel.Index() >= len(e.Elts) {
			return x
		}
		out := *e
		out.Elts = append([]ast.Expr(nil), e.Elts...)
		out.Elts[sel.Index()] = withoutAt(e.Elts[sel.Index()], sels[1:])
		return &out
	}
	return x
}
