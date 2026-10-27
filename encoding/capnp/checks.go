package capnp

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"cel.dev/cel-go/cel"
	"cuelang.org/go/cue"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/encoding/capnp/compiled"
	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/lenscheck"
)

// checks renders the extras acceptance checks (#48). Data validated open may
// carry fields its version did not declare, which travel in each struct's
// extras. When a later version declares one of those labels, the extra is
// promoted into the field only if it is an instance of it; the check is a
// CEL predicate over x, the extra's value, rendered from the CUE constraint
// at the field. One check per label a version newly declares at a struct the
// version before also had; a constraint the renderer cannot express is a
// finding under extras-check-unrenderable.
func (cp *compiler) checks() []*compiled.Check {
	var out []*compiled.Check
	var prev thema.Schema
	for _, sch := range cp.lin.All() {
		if prev != nil {
			v := sch.Version()
			root := cp.c.Node(cp.gens.rootOf(v))
			cp.checkStruct(v, cue.MakePath(), cp.trees[prev.Version()], cp.trees[v], cp.schemas[v], root, &out)
		}
		prev = sch
	}
	return out
}

// checkStruct walks a struct of version v beside the same struct of the
// version before: a label the earlier struct lacks gets a check; one both
// declare is followed into nested structs.
func (cp *compiler) checkStruct(v thema.SyntacticVersion, p cue.Path, old, cur *vstruct, curVal cue.Value, node *compiled.Node, out *[]*compiled.Check) {
	if old == nil || cur == nil || node == nil {
		return
	}
	for i := range cur.fields {
		f := &cur.fields[i]
		if f.fixed != "" {
			continue
		}
		fp := appendPath(p, cue.Str(f.label))
		fv := lookup(curVal, cue.MakePath(cue.Str(f.label)))
		of := old.field(f.label)
		if of == nil {
			if src, ok := cp.renderCheck(v, fp, fv); ok {
				*out = append(*out, &compiled.Check{Version: [2]uint(v), Node: node.ID, Label: f.label, Source: src})
			}
			continue
		}
		cf := node.Field(f.label)
		if cf == nil {
			continue
		}
		cp.checkType(v, fp, of.typ, f.typ, fv, cf.Type, cf.Union, out)
	}
}

// checkType follows a field's type into the structs beneath it, where both
// versions have one: a struct, a list's elements, a map's values, and the
// struct alternatives of a union.
func (cp *compiler) checkType(v thema.SyntacticVersion, p cue.Path, old, cur *vtype, curVal cue.Value, ct *compiled.Type, union []*compiled.Member, out *[]*compiled.Check) {
	if old == nil || cur == nil {
		return
	}
	if cur.isUnion() {
		if !old.isUnion() {
			return
		}
		for i := range cur.union {
			m := &cur.union[i]
			var om *vmember
			for j := range old.union {
				if old.union[j].name == m.name {
					om = &old.union[j]
				}
			}
			var cm *compiled.Member
			for _, c := range union {
				if c.Name == m.name {
					cm = c
				}
			}
			if om == nil || cm == nil || m.typ == nil || om.typ == nil {
				continue
			}
			cp.checkType(v, p, om.typ, m.typ, cp.alternative(curVal, m), cm.Type, nil, out)
		}
		return
	}
	if old.isUnion() {
		return
	}
	if ct == nil && len(union) > 0 {
		for _, c := range union {
			if c.Role == compiled.Value {
				ct = c.Type
			}
		}
	}
	if ct == nil || cur.kind != old.kind {
		return
	}
	switch cur.kind {
	case compiled.Struct:
		if cur.strct == nil || old.strct == nil || cur.strct.role != compiled.SchemaStruct {
			return
		}
		cp.checkStruct(v, p, old.strct, cur.strct, curVal, cp.c.Node(ct.Node), out)
	case compiled.List:
		if cur.elem == nil || old.elem == nil || ct.Elem == nil {
			return
		}
		if cur.elem.strct != nil && cur.elem.strct.role == compiled.MapEntry {
			if old.elem.strct == nil || old.elem.strct.role != compiled.MapEntry {
				return
			}
			entry := cp.c.Node(ct.Elem.Node)
			if entry == nil {
				return
			}
			val := entry.Fields[1]
			cp.checkType(v, appendPath(p, cue.AnyString), old.elem.strct.fields[1].typ, cur.elem.strct.fields[1].typ,
				curVal.LookupPath(cue.MakePath(cue.AnyString)), val.Type, val.Union, out)
			return
		}
		if cur.elem.strct != nil && cur.elem.strct.role == compiled.UnionBox {
			if old.elem.strct == nil || old.elem.strct.role != compiled.UnionBox {
				return
			}
			box := cp.c.Node(ct.Elem.Node)
			if box == nil {
				return
			}
			cp.checkType(v, appendPath(p, cue.AnyIndex), old.elem.strct.fields[0].typ, cur.elem.strct.fields[0].typ,
				curVal.LookupPath(cue.MakePath(cue.AnyIndex)), nil, box.Fields[0].Union, out)
			return
		}
		cp.checkType(v, appendPath(p, cue.AnyIndex), old.elem, cur.elem, curVal.LookupPath(cue.MakePath(cue.AnyIndex)), ct.Elem, nil, out)
	}
}

// alternative finds the branch of a disjunction value that a union member
// holds.
func (cp *compiler) alternative(v cue.Value, m *vmember) cue.Value {
	for _, b := range compat.Branches(v) {
		switch m.role {
		case compiled.KindMember:
			if kindNames[b.IncompleteKind()] == m.kind {
				return b
			}
		case compiled.TagMember:
			if lit, ok := literalOf(b.LookupPath(cue.MakePath(cue.Str(m.tagLabel)))); ok && m.tag != nil && sameLiteral(&lit, m.tag) {
				return b
			}
		}
	}
	return cue.Value{}
}

// field finds a struct's field by label.
func (s *vstruct) field(label string) *vfield {
	for i := range s.fields {
		if f := &s.fields[i]; f.fixed == "" && f.label == label {
			return f
		}
	}
	return nil
}

// renderCheck renders the acceptance check for the field at p of version v,
// whose value is fv, and type-checks it.
func (cp *compiler) renderCheck(v thema.SyntacticVersion, p cue.Path, fv cue.Value) (string, bool) {
	cr := &checkRenderer{cp: cp, version: v, path: p}
	src := cr.value("x", fv)
	if cr.failed {
		return "", false
	}
	env, err := cel.NewEnv(cel.Variable("x", cel.DynType))
	if err != nil {
		cr.fail(fmt.Sprintf("renders to %s, whose environment cel-go rejects: %v", src, err))
		return "", false
	}
	ast, iss := env.Compile(src)
	if iss.Err() != nil {
		cr.fail(fmt.Sprintf("renders to %s, which cel-go rejects: %v", src, iss.Err()))
		return "", false
	}
	if got := ast.OutputType(); !isDyn(got) && !got.IsEquivalentType(cel.BoolType) {
		cr.fail(fmt.Sprintf("renders to %s, of CEL type %s rather than bool", src, got))
		return "", false
	}
	return src, true
}

// A checkRenderer renders one field's constraint as a CEL predicate.
type checkRenderer struct {
	cp      *compiler
	version thema.SyntacticVersion
	path    cue.Path
	depth   int
	failed  bool
}

func (cr *checkRenderer) fail(detail string) string {
	if !cr.failed {
		cr.failed = true
		cr.cp.add(cr.version, cr.path, Rejected, RuleExtrasCheckUnrenderable, "is newly declared, and its acceptance check "+detail, posOf(lookup(cr.cp.schemas[cr.version], cr.path)))
	}
	return celFalse
}

// value renders whether the value in the CEL expression x is an instance of
// v: each alternative of a disjunction in turn, a constant as an equality,
// a kind as a type test with its bounds or regular expressions, a list
// element-wise and a struct field-wise, closed.
func (cr *checkRenderer) value(x string, v cue.Value) string {
	brs := compat.Branches(v)
	if len(brs) > 1 {
		parts := make([]string, 0, len(brs))
		for _, b := range brs {
			parts = append(parts, cr.value(x, b))
		}
		return group(parts, " || ")
	}
	v = brs[0]
	if lit, ok := literalOf(v); ok {
		r := &renderer{}
		return "(" + x + " == " + r.literal(lit) + ")"
	}
	switch v.IncompleteKind() {
	case cue.TopKind:
		return cr.fail("cannot express _")
	case cue.NullKind:
		return "(" + x + " == null)"
	case cue.BoolKind:
		return "(type(" + x + ") == bool)"
	case cue.IntKind:
		lo, hi, ok := lenscheck.IntegerBounds(v)
		if !ok {
			return cr.fail(fmt.Sprintf("cannot read the bounds of %s", fmtv(v)))
		}
		// A CEL int is 64 bits, so bounds at int64's ends say nothing.
		parts := []string{"(type(" + x + ") == int)"}
		if lo != nil && lo.Cmp(minInt64) != 0 {
			parts = append(parts, "("+x+" >= "+lo.String()+")")
		}
		if hi != nil && hi.Cmp(maxInt64) != 0 {
			parts = append(parts, "("+x+" <= "+hi.String()+")")
		}
		return group(parts, " && ")
	case cue.StringKind:
		parts := []string{"(type(" + x + ") == string)"}
		cr.stringConstraints(x, v, &parts)
		return group(parts, " && ")
	case cue.BytesKind:
		return "(type(" + x + ") == bytes)"
	case cue.ListKind:
		return cr.list(x, v)
	case cue.StructKind:
		return cr.strct(x, v)
	}
	return cr.fail(fmt.Sprintf("cannot express %s", fmtv(v)))
}

var (
	minInt64 = big.NewInt(math.MinInt64)
	maxInt64 = big.NewInt(math.MaxInt64)
)

// group joins the parts with op, in parentheses where there are several.
func group(parts []string, op string) string {
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, op) + ")"
}

// stringConstraints adds the regular expressions a string value must match;
// a validator call is not expressed.
func (cr *checkRenderer) stringConstraints(x string, v cue.Value, parts *[]string) {
	op, args := v.Expr()
	switch op {
	case cue.NoOp:
	case cue.AndOp:
		for _, a := range args {
			cr.stringConstraints(x, a, parts)
		}
	case cue.RegexMatchOp, cue.NotRegexMatchOp:
		re, err := args[0].String()
		if err != nil {
			cr.fail(fmt.Sprintf("cannot read the regular expression in %s", fmtv(v)))
			return
		}
		m := x + ".matches(" + strconv.Quote(re) + ")"
		if op == cue.NotRegexMatchOp {
			m = "!" + m
		}
		*parts = append(*parts, m)
	default:
		cr.fail(fmt.Sprintf("cannot express %s", fmtv(v)))
	}
}

// list renders a list: an open list by its element constraint over every
// element, a closed one by its length and each element.
func (cr *checkRenderer) list(x string, v cue.Value) string {
	parts := []string{"(type(" + x + ") == list)"}
	if anyv := v.LookupPath(cue.MakePath(cue.AnyIndex)); anyv.Exists() {
		cr.depth++
		e := "e" + strconv.Itoa(cr.depth)
		parts = append(parts, "("+x+".all("+e+", "+cr.value(e, anyv)+"))")
		cr.depth--
		return group(parts, " && ")
	}
	it, err := v.List()
	if err != nil {
		return cr.fail(fmt.Sprintf("cannot express %s", fmtv(v)))
	}
	n := 0
	for it.Next() {
		parts = append(parts, cr.value(x+"["+strconv.Itoa(n)+"]", it.Value()))
		n++
	}
	parts = append(parts, "(size("+x+") == "+strconv.Itoa(n)+")")
	return group(parts, " && ")
}

// strct renders a struct: a map by its value constraint over every entry;
// otherwise each declared field, required ones present, and no other label.
func (cr *checkRenderer) strct(x string, v cue.Value) string {
	if op, _ := v.Expr(); op != cue.NoOp {
		return cr.fail(fmt.Sprintf("cannot express %s", fmtv(v)))
	}
	parts := []string{"(type(" + x + ") == map)"}
	w := &walker{ctx: cr.cp.ctx}
	if val, ok := w.isMap(v); ok {
		cr.depth++
		k := "k" + strconv.Itoa(cr.depth)
		parts = append(parts, "("+x+".all("+k+", "+cr.value(x+"["+k+"]", val)+"))")
		cr.depth--
		return group(parts, " && ")
	}
	it, err := v.Fields(cue.Optional(true), cue.Patterns(true))
	if err != nil {
		return cr.fail(fmt.Sprintf("cannot express %s", fmtv(v)))
	}
	var labels []string
	for it.Next() {
		sel := it.Selector()
		if sel.ConstraintType() == cue.PatternConstraint {
			return cr.fail(fmt.Sprintf("cannot express the pattern constraint %s", sel))
		}
		if !sel.IsString() {
			continue
		}
		label := strconv.Quote(sel.Unquoted())
		labels = append(labels, label)
		has := "(" + label + " in " + x + ")"
		check := cr.value(x+"["+label+"]", it.Value())
		if compat.FormOf(sel, it.Value()) == compat.Required {
			parts = append(parts, "("+has+" && "+check+")")
		} else {
			parts = append(parts, "(!"+has+" || "+check+")")
		}
	}
	cr.depth++
	k := "k" + strconv.Itoa(cr.depth)
	parts = append(parts, "("+x+".all("+k+", "+k+" in ["+strings.Join(labels, ", ")+"]))")
	cr.depth--
	return group(parts, " && ")
}
