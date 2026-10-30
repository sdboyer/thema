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

// This file holds the part of translation that decides what becomes of the
// fields no lens maps (#193; the rules are docs/invariants.md, "Fields no lens
// maps"). Around each step, translation takes from the instance:
//
//   - its extras: the fields its schema does not declare, which an open
//     struct's ... admitted;
//   - for a step with a lens, the fields its schema declares that the lens
//     leaves alone: it reads neither them nor anything that holds them, and no
//     lacuna it lists names them;
//   - for a step with a lens, the fields its schema declares that the lens
//     reads, where none of the result fields that read them (that copy them,
//     or compute from them) was written for the instance: such as a field the
//     lens moves into a struct it writes only when another field is present.
//
// The lens runs on the instance without its extras, since a lens reads and
// writes only declared fields. Then:
//
//   - An extra follows its struct: where the lens takes the contents of a
//     struct of its result from one struct of its input as a whole (info:
//     v0_0.meta, info: {v0_0.meta, x: 1}), the input struct's extras go with
//     it (cuetil.LensSyntax.Copies). Elsewhere an extra keeps its path;
//     within a list, element i is taken to be element i, so only the extras
//     of elements past the result's length have nowhere to go.
//   - An extra is placed at its path if the result is still an instance of
//     the target with it: carried where the target's struct is open and does
//     not declare it, promoted, over any default, where the target declares
//     it and the value is an instance of what it declares. Where the lens
//     writes a field the target declares, the lens's value stands.
//   - A field the source declares and the lens leaves alone, or reads to no
//     effect, is placed the same way where the target does not declare it.
//     Where the target declares it, the field is not copied: the lens did not
//     write it.
//
// A field not placed is dropped with a lacuna: a LossyFieldMapping for a value
// not an instance of the field the target declares for it, otherwise a
// DroppedField for each struct and cause. Dropping an empty struct or list
// loses nothing, and raises no lacuna. Translation never fails because of
// these fields (#57).
//
// Go's Translate defines this; the kernel's CUE Translate, evaluated alone,
// neither carries nor sets aside anything (#198).

// A taken field is a field translation decides the fate of itself, rather
// than leaving it to a lens.
type taken struct {
	path  cue.Path
	value cue.Value
	kind  takenKind
}

type takenKind int

const (
	// takenExtra: the source schema does not declare the field.
	takenExtra takenKind = iota
	// takenUnmapped: the source schema declares the field, the target does
	// not, and the lens leaves it alone.
	takenUnmapped
	// takenUnwritten: both schemas declare the field, and the lens neither
	// maps nor writes it.
	takenUnwritten
	// takenLost: the source schema declares the field, the lens reads it, and
	// none of the result fields that read it was written for this instance.
	takenLost
)

// Why a taken field was set aside with a DroppedField. Each struct gets one
// lacuna per cause.
type asideCause int

const (
	// asideClosed: the target does not declare the field, and its struct
	// there is closed.
	asideClosed asideCause = iota
	// asideNowhere: the result has no struct at the field's parent path to
	// carry it in.
	asideNowhere
	// asideWritten: the target declares the field, and the lens wrote its own
	// value for it.
	asideWritten
	// asideUnwritten: the target declares the field, and the lens neither
	// maps nor writes it.
	asideUnwritten
	// asideLost: the lens reads the field, but wrote none of the result
	// fields that read it, and it could not be carried.
	asideLost
)

// stepLens is what translation reads of a step's lens. A step without a lens
// has a nil *stepLens.
type stepLens struct {
	// result is the lens's own result, before translation closes it against
	// the target: what the lens wrote.
	result cue.Value
	// reads are the input paths its result reads.
	reads []cue.Path
	// readers are the fields of its result that read each of those paths:
	// that copy the value there, or compute from it.
	readers []cuetil.Reader
	// named are the input paths any lacuna it lists names in its
	// sourceFields, whether or not the lacuna applies (a written _drop is a
	// DroppedField lacuna).
	named []cue.Path
	// copies are the fields of its result that copy a value of its input
	// whole.
	copies []cuetil.Copy
}

// wrote reports whether the lens wrote a field or element at p.
func (l *stepLens) wrote(p cue.Path) bool {
	return l != nil && hasDataPath(l.result, p)
}

// How a lens treats a value of its input.
type readFate int

const (
	// unread: nothing reads or names the value, anything holding it, or
	// anything beneath it.
	unread readFate = iota
	// readBeneath: whether the value is carried is decided beneath it.
	readBeneath
	// readCarried: a lacuna the lens lists names the value or something
	// holding it, a result field that reads it was written for this
	// instance, or it is read only by a comprehension's clauses, which
	// decide whether fields exist rather than carry it.
	readCarried
	// readLost: result fields read the value, and none of them was written
	// for this instance.
	readLost
)

// read returns how the lens treats the value at the input path p.
func (l *stepLens) read(p cue.Path) readFate {
	np := len(p.Selectors())
	beneath := false
	for _, m := range l.named {
		if !pathsOverlap(m, p) {
			continue
		}
		if len(m.Selectors()) <= np {
			return readCarried
		}
		beneath = true
	}
	covered := false
	for _, m := range l.reads {
		if !pathsOverlap(m, p) {
			continue
		}
		if len(m.Selectors()) <= np {
			covered = true
		} else {
			beneath = true
		}
	}
	if !covered {
		if beneath {
			return readBeneath
		}
		return unread
	}
	readers := false
	for _, r := range l.readers {
		if len(r.Input.Selectors()) > np || !pathsOverlap(r.Input, p) {
			continue
		}
		if l.wrote(r.Result) {
			return readCarried
		}
		readers = true
	}
	switch {
	case beneath:
		// What holds the value went unwritten, or only a clause read it:
		// what is beneath it may still be carried.
		return readBeneath
	case readers:
		return readLost
	}
	return readCarried
}

// targets returns the paths in the step's result the extra at p goes to: the
// paths of the copies of the deepest struct holding it that the lens copies
// whole, or, where the lens copies none, p itself.
func (l *stepLens) targets(p cue.Path) []cue.Path {
	if l == nil {
		return []cue.Path{p}
	}
	ps := p.Selectors()
	best := -1
	var out []cue.Path
	for _, c := range l.copies {
		cs := c.Input.Selectors()
		if len(cs) >= len(ps) || len(cs) < best || !pathsOverlap(c.Input, p) {
			continue
		}
		if len(cs) > best {
			best, out = len(cs), nil
		}
		out = append(out, cue.MakePath(append(c.Result.Selectors(), ps[len(cs):]...)...))
	}
	if best < 0 {
		return []cue.Path{p}
	}
	return out
}

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
// lens, the fields of data that from declares and the lens leaves alone:
// those to does not declare and result, the step's result, does not have
// (takenUnmapped), and those to declares and the lens did not write
// (takenUnwritten), and those it reads but wrote nothing that carries for this
// instance (takenLost). List elements are taken as fields are. A field the
// lens carries is the lens's. A field holding one the lens reads, or one both
// schemas declare and the lens wrote, is descended into.
func takeFields(ctx *cue.Context, data cue.Value, extras []taken, lens *stepLens, result, from, to cue.Value) []taken {
	if lens == nil {
		return extras
	}
	byPath := make(map[string]taken, len(extras))
	for _, x := range extras {
		byPath[x.path.String()] = x
	}
	var out []taken
	// walk takes the fields and elements of v, at p; beneath one the lens
	// carries, it takes only extras.
	var walk func(v cue.Value, p cue.Path, mapped bool)
	walk = func(v cue.Value, p cue.Path, mapped bool) {
		type child struct {
			path  cue.Path
			value cue.Value
		}
		var children []child
		switch v.IncompleteKind() {
		case cue.StructKind:
			iter, err := v.Fields()
			if err != nil {
				return
			}
			for iter.Next() {
				children = append(children, child{appendSel(p, iter.Selector()), iter.Value()})
			}
		case cue.ListKind:
			iter, err := v.List()
			if err != nil {
				return
			}
			for i := 0; iter.Next(); i++ {
				children = append(children, child{appendSel(p, cue.Index(i)), iter.Value()})
			}
		}
		for _, c := range children {
			q := c.path
			if x, ok := byPath[q.String()]; ok {
				out = append(out, x)
				continue
			}
			if !cuetil.DeclaresPath(ctx, from, q) {
				continue
			}
			if mapped {
				walk(c.value, q, true)
				continue
			}
			fate := lens.read(q)
			switch fate {
			case readCarried:
				walk(c.value, q, true)
				continue
			case readLost:
				if !hasDataPath(result, q) || !result.LookupPath(q).Equals(c.value) {
					out = append(out, taken{path: q, value: c.value, kind: takenLost})
				}
				continue
			}
			declared := cuetil.DeclaresPath(ctx, to, q)
			if fate == readBeneath || (declared && lens.wrote(q)) {
				walk(c.value, q, false)
				continue
			}
			switch {
			case declared:
				out = append(out, taken{path: q, value: c.value, kind: takenUnwritten})
			case !hasDataPath(result, q):
				out = append(out, taken{path: q, value: c.value, kind: takenUnmapped})
			}
		}
	}
	walk(data, cue.MakePath(), false)
	return out
}

// carry places each field of fields in result, an instance of to's schema
// produced by the step from version fromV through lens, and returns the
// result and the lacunas for the fields not placed.
func carry(ctx *cue.Context, result cue.Value, lens *stepLens, to *schemaDef, fromV SyntacticVersion, fields []taken) (cue.Value, []lacuna.Lacuna, error) {
	if len(fields) == 0 {
		return result, nil, nil
	}
	rx, err := dataSyntax(result)
	if err != nil {
		return cue.Value{}, nil, diag.InternalErr(err)
	}
	closed := to.closed()

	type group struct {
		parent cue.Path
		cause  asideCause
		refs   []lacuna.FieldRef
	}
	// The lacunas, in the order of the first field each concerns: a
	// LossyFieldMapping is one field's, a group a struct's for one cause.
	type entry struct {
		g   *group
		lac lacuna.Lacuna
	}
	var entries []entry
	byKey := map[string]*group{}
	ref := func(f taken) (lacuna.FieldRef, error) {
		var val interface{}
		if err := f.value.Decode(&val); err != nil {
			return lacuna.FieldRef{}, diag.InternalErr(err)
		}
		return lacuna.FieldRef{Path: f.path.String(), Value: val}, nil
	}
	aside := func(f taken, parent cue.Path, cause asideCause) error {
		if isEmpty(f.value) {
			return nil
		}
		r, err := ref(f)
		if err != nil {
			return err
		}
		key := fmt.Sprintf("%d %s", cause, parent)
		g, ok := byKey[key]
		if !ok {
			g = &group{parent: parent, cause: cause}
			byKey[key] = g
			entries = append(entries, entry{g: g})
		}
		g.refs = append(g.refs, r)
		return nil
	}

	placed := false
	for _, f := range fields {
		if f.kind == takenUnwritten {
			if err := aside(f, parentOf(f.path), asideUnwritten); err != nil {
				return cue.Value{}, nil, err
			}
			continue
		}
		if f.kind == takenLost {
			// Carried as a field the target does not declare, where it can be:
			// then it is not lost. Never into a field the target declares,
			// which is the lens's to write.
			parent := parentOf(f.path)
			if isStructLit(lookupSyntax(rx, parent.Selectors())) && !cuetil.DeclaresPath(ctx, to.def, f.path) {
				vx, verr := dataSyntax(f.value)
				if verr != nil {
					return cue.Value{}, nil, diag.InternalErr(verr)
				}
				cand := withAt(rx, f.path.Selectors(), vx)
				if closed.Unify(ctx.BuildExpr(cand)).Validate(cue.Concrete(true)) == nil {
					rx, placed = cand, true
					continue
				}
			}
			if err := aside(f, parent, asideLost); err != nil {
				return cue.Value{}, nil, err
			}
			continue
		}
		dests := []cue.Path{f.path}
		if f.kind == takenExtra {
			dests = lens.targets(f.path)
		}
		for _, dest := range dests {
			sels := dest.Selectors()
			parent := parentOf(dest)
			if !isStructLit(lookupSyntax(rx, parent.Selectors())) {
				if err := aside(f, parent, asideNowhere); err != nil {
					return cue.Value{}, nil, err
				}
				continue
			}
			declared := cuetil.DeclaresPath(ctx, to.def, dest)
			if declared && lens.wrote(dest) {
				// The lens's value stands. Nothing is lost if it is the same.
				if result.LookupPath(dest).Equals(f.value) {
					continue
				}
				if err := aside(f, parent, asideWritten); err != nil {
					return cue.Value{}, nil, err
				}
				continue
			}
			vx, verr := dataSyntax(f.value)
			if verr != nil {
				return cue.Value{}, nil, diag.InternalErr(verr)
			}
			cand := withAt(rx, sels, vx)
			if closed.Unify(ctx.BuildExpr(cand)).Validate(cue.Concrete(true)) == nil {
				rx, placed = cand, true
				continue
			}
			if !declared {
				if err := aside(f, parent, asideClosed); err != nil {
					return cue.Value{}, nil, err
				}
				continue
			}
			src, rerr := ref(f)
			if rerr != nil {
				return cue.Value{}, nil, rerr
			}
			// The target field as it ended up: its default, or absent, with no
			// value.
			tgt := lacuna.FieldRef{Path: dest.String()}
			if hasDataPath(result, dest) {
				if err := result.LookupPath(dest).Decode(&tgt.Value); err != nil {
					return cue.Value{}, nil, diag.InternalErr(err)
				}
			}
			entries = append(entries, entry{lac: lacuna.Lacuna{
				SourceFields: []lacuna.FieldRef{src},
				TargetFields: []lacuna.FieldRef{tgt},
				Type:         lacuna.LossyFieldMapping,
				Message:      lossyMessage(f.path, dest, to.Version()),
			}})
		}
	}

	if placed {
		result = ctx.BuildExpr(rx)
	}
	lacs := make([]lacuna.Lacuna, 0, len(entries))
	for _, e := range entries {
		if e.g == nil {
			lacs = append(lacs, e.lac)
			continue
		}
		lacs = append(lacs, lacuna.Lacuna{
			SourceFields: e.g.refs,
			Type:         lacuna.DroppedField,
			Message:      asideMessage(e.g.refs, e.g.parent, e.g.cause, fromV, to.Version()),
		})
	}
	return result, lacs, nil
}

// lossyMessage says why the value at src was not carried to dest, which toV
// declares.
func lossyMessage(src, dest cue.Path, toV SyntacticVersion) string {
	if src.String() == dest.String() {
		return fmt.Sprintf("%s not carried: its value is not an instance of what %s declares for it", src, toV)
	}
	return fmt.Sprintf("%s not carried: its value is not an instance of what %s declares for %s", src, toV, dest)
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
	case asideWritten:
		return fmt.Sprintf("%s set aside: the lens from %s to %s writes %s for %s", subject, fromV, toV, pick("its own value", "its own values"), pick("it", "them"))
	case asideUnwritten:
		return fmt.Sprintf("%s set aside: the lens from %s to %s neither maps nor writes %s", subject, fromV, toV, pick("it", "them"))
	case asideLost:
		return fmt.Sprintf("%s set aside: the lens from %s to %s reads %s, but wrote nothing that carries %s for this instance", subject, fromV, toV, pick("it", "them"), pick("it", "them"))
	}
	return fmt.Sprintf("%s set aside: translating from %s to %s produces no %s to carry %s in", subject, fromV, toV, where, pick("it", "them"))
}

// isEmpty reports whether v is an empty struct or list.
func isEmpty(v cue.Value) bool {
	switch v.IncompleteKind() {
	case cue.StructKind:
		iter, err := v.Fields()
		return err == nil && !iter.Next()
	case cue.ListKind:
		n, err := v.Len().Int64()
		return err == nil && n == 0
	}
	return false
}

func parentOf(p cue.Path) cue.Path {
	sels := p.Selectors()
	return cue.MakePath(sels[:len(sels)-1]...)
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
