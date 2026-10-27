package capnp

import (
	"fmt"
	"math/big"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/encoding/capnp/compiled"
	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/lenscheck"
	"github.com/sdboyer/thema/internal/lensir"
)

// This file is the type mapping: a structural walk over one schema version,
// querying it through the cue API, that produces the version's view of each
// value's capnp type (a vtype tree), or findings where a construct has none.
// The tree carries the schema's labels and values, never capnp names or
// ordinals: those are assigned by the generation the version joins
// (generations.go), by first appearance in the lineage's history.

// A vtype is one version's view of a value's type.
type vtype struct {
	// kind is the capnp type; 0 where the value is a discriminated
	// disjunction, held in union.
	kind compiled.TypeKind
	// elem is a List's element type.
	elem *vtype
	// strct is a Struct's shape.
	strct *vstruct
	// enum holds the string constants an Enum stands for, in declaration
	// order.
	enum []string
	// union holds a discriminated disjunction's alternatives.
	union []vmember
}

// A vstruct is one version's view of a struct.
type vstruct struct {
	role   compiled.NodeRole
	fields []vfield
}

// A vfield is one version's view of a field.
type vfield struct {
	// label is the schema's label; fixed names a field the compiler adds
	// (a map entry's key and value, a union box's union) and is then its
	// capnp name.
	label, fixed string
	form         compiled.Form
	typ          *vtype
	// def is the schema's default, where the field is Defaulted and the
	// default is a scalar.
	def *lensir.Literal
}

// boxField is the fixed name of a union box's one field, its anonymous
// union, which has no capnp name.
const boxField = "union"

// valueMember is the name of the member holding an optional field's value.
const valueMember = "value"

// key identifies the field within its struct across versions.
func (f vfield) key() string {
	if f.fixed != "" {
		return "\x00" + f.fixed
	}
	return f.label
}

// A vmember is one alternative of a discriminated disjunction.
type vmember struct {
	// name is the member's capnp name, from the kind or the tag.
	name     string
	role     compiled.MemberRole
	kind     string
	tagLabel string
	tag      *lensir.Literal
	// typ is the alternative's type; nil for a Void member.
	typ *vtype
}

// isUnion reports whether t is a disjunction, rendered as a union.
func (t *vtype) isUnion() bool { return t != nil && t.kind == 0 }

// walker maps one schema version.
type walker struct {
	ctx      *cue.Context
	version  thema.SyntacticVersion
	findings []Finding
	// pos is the positions of the values being walked, innermost last; a
	// finding on a value without a position (a list's element type) takes the
	// nearest enclosing one.
	pos []token.Pos
}

func (w *walker) add(p cue.Path, verdict Verdict, rule Rule, detail string, pos token.Pos) {
	if !pos.IsValid() && len(w.pos) > 0 {
		pos = w.pos[len(w.pos)-1]
	}
	w.findings = append(w.findings, Finding{Version: w.version, Path: p, Verdict: verdict, Rule: rule, Detail: detail, Pos: pos})
}

// bad is the type returned where a finding was recorded, so that the walk
// can go on and report every construct.
var bad = &vtype{kind: compiled.Void}

// schema maps the schema's top level, which bind has checked is a struct.
func (w *walker) schema(v cue.Value) *vstruct {
	return w.strct(v, cue.MakePath())
}

// strct maps a struct: its named fields, and either no pattern constraint or
// a lone [string]: T on a struct declaring nothing else, which is a map.
func (w *walker) strct(v cue.Value, p cue.Path) *vstruct {
	s := &vstruct{role: compiled.SchemaStruct}
	it, err := v.Fields(cue.Optional(true), cue.Patterns(true))
	if err != nil {
		return s
	}
	var patterns []cue.Selector
	for it.Next() {
		sel := it.Selector()
		if sel.ConstraintType() == cue.PatternConstraint {
			patterns = append(patterns, sel)
			continue
		}
		if !sel.IsString() {
			continue
		}
		s.fields = append(s.fields, w.field(sel, it.Value(), appendPath(p, cue.Str(sel.Unquoted()))))
	}
	if len(patterns) > 0 {
		var pats []string
		for _, pat := range patterns {
			pats = append(pats, pat.String())
		}
		w.add(p, Unknown, RuleUnknownPatternLabels,
			fmt.Sprintf("has pattern constraints %s beside its fields; the compiler carries only a lone [string]: T on a struct declaring no other field", strings.Join(pats, ", ")),
			posOf(v))
	}
	return s
}

// isMap reports whether v is a map: a struct declaring no named field and one
// pattern constraint matching every label.
func (w *walker) isMap(v cue.Value) (cue.Value, bool) {
	it, err := v.Fields(cue.Optional(true), cue.Patterns(true))
	if err != nil {
		return cue.Value{}, false
	}
	var pat cue.Value
	n := 0
	for it.Next() {
		sel := it.Selector()
		if sel.ConstraintType() != cue.PatternConstraint {
			if sel.IsString() {
				return cue.Value{}, false
			}
			continue
		}
		n++
		if sel.Pattern().Subsume(w.ctx.CompileString(kindString), cue.Raw()) != nil {
			return cue.Value{}, false
		}
		pat = it.Value()
	}
	return pat, n == 1
}

// field maps one field: its form, its default and its type.
func (w *walker) field(sel cue.Selector, v cue.Value, p cue.Path) vfield {
	f := vfield{label: sel.Unquoted(), form: compiled.Form(compat.FormOf(sel, v))}
	if f.form == compiled.Defaulted {
		d, _ := v.Default()
		if lit, ok := literalOf(d); ok {
			f.def = &lit
		}
	}
	f.typ = w.value(v, p)
	return f
}

// value maps a value by its alternatives: one is mapped as it is, several as
// a discriminated disjunction.
func (w *walker) value(v cue.Value, p cue.Path) *vtype {
	if pos := v.Pos(); pos.IsValid() {
		w.pos = append(w.pos, pos)
		defer func() { w.pos = w.pos[:len(w.pos)-1] }()
	}
	brs := compat.Branches(v)
	if len(brs) == 1 {
		return w.single(brs[0], p)
	}
	return w.disjunction(v, brs, p)
}

// single maps a value with one alternative, by its kind.
func (w *walker) single(v cue.Value, p cue.Path) *vtype {
	k := v.IncompleteKind()
	switch {
	case k == cue.TopKind:
		w.add(p, Rejected, RuleTop, "is _, which has no type", posOf(v))
		return bad
	case k == cue.NullKind:
		return &vtype{kind: compiled.Void}
	case k == cue.BoolKind:
		return &vtype{kind: compiled.Bool}
	case k == cue.IntKind:
		return w.integer(v, p)
	case k&cue.NumberKind != 0 && k&^cue.NumberKind == 0:
		w.add(p, Rejected, RuleFloat, fmt.Sprintf("is %s, a number that may be a float; floats are outside the compiled target", fmtv(v)), posOf(v))
		return bad
	case k == cue.StringKind:
		if v.IsConcrete() {
			s, _ := v.String()
			return &vtype{kind: compiled.Enum, enum: []string{s}}
		}
		return &vtype{kind: compiled.Text}
	case k == cue.BytesKind:
		return &vtype{kind: compiled.Data}
	case k == cue.ListKind:
		return w.list(v, p)
	case k == cue.StructKind:
		if val, ok := w.isMap(v); ok {
			return w.mapOf(val, p)
		}
		return &vtype{kind: compiled.Struct, strct: w.strct(v, p)}
	}
	w.add(p, Rejected, RuleDisjunctionUndiscriminated,
		fmt.Sprintf("is %s, whose alternatives of kinds %s the compiler cannot separate", fmtv(v), k), posOf(v))
	return bad
}

// integer maps an integer by its bounds to the smallest capnp integer type
// containing them: unsigned where the lower bound is not negative, at equal
// size.
func (w *walker) integer(v cue.Value, p cue.Path) *vtype {
	lo, hi, ok := lenscheck.IntegerBounds(v)
	if !ok || lo == nil || hi == nil {
		w.add(p, Rejected, RuleIntUnbounded,
			fmt.Sprintf("is %s, an integer with no finite bounds; capnp integers are sized (int32, uint8, >=0 & <=100)", fmtv(v)), posOf(v))
		return bad
	}
	if k, ok := intType(lo, hi); ok {
		return &vtype{kind: k}
	}
	w.add(p, Rejected, RuleIntUnbounded,
		fmt.Sprintf("is %s, an integer whose bounds exceed 64 bits", fmtv(v)), posOf(v))
	return bad
}

// intType is the smallest capnp integer type containing [lo, hi]: at equal
// size, unsigned where lo is not negative.
func intType(lo, hi *big.Int) (compiled.TypeKind, bool) {
	for _, c := range intTypes {
		if lo.Cmp(c.lo) >= 0 && hi.Cmp(c.hi) <= 0 {
			return c.kind, true
		}
	}
	return 0, false
}

// intRange is a capnp integer type and the values it holds.
type intRange struct {
	kind   compiled.TypeKind
	lo, hi *big.Int
}

// intTypes lists the capnp integer types smallest first, unsigned before
// signed at each size: unsigned [0, 2^n-1], signed [-2^(n-1), 2^(n-1)-1].
var intTypes = func() []intRange {
	sizes := []struct {
		bits             uint
		unsigned, signed compiled.TypeKind
	}{
		{bits: 8, unsigned: compiled.UInt8, signed: compiled.Int8},    //nolint:mnd // a capnp integer width
		{bits: 16, unsigned: compiled.UInt16, signed: compiled.Int16}, //nolint:mnd // a capnp integer width
		{bits: 32, unsigned: compiled.UInt32, signed: compiled.Int32}, //nolint:mnd // a capnp integer width
		{bits: 64, unsigned: compiled.UInt64, signed: compiled.Int64}, //nolint:mnd // a capnp integer width
	}
	one, zero := big.NewInt(1), big.NewInt(0)
	var out []intRange
	for _, sz := range sizes {
		umax := new(big.Int).Sub(new(big.Int).Lsh(one, sz.bits), one)
		half := new(big.Int).Lsh(one, sz.bits-1)
		out = append(out,
			intRange{sz.unsigned, zero, umax},
			intRange{sz.signed, new(big.Int).Neg(half), new(big.Int).Sub(half, one)})
	}
	return out
}()

// list maps a list: an open list by its element constraint, a closed one by
// its elements, which must all map to one type.
func (w *walker) list(v cue.Value, p cue.Path) *vtype {
	ep := appendPath(p, cue.AnyIndex)
	var elem *vtype
	if anyv := v.LookupPath(cue.MakePath(cue.AnyIndex)); anyv.Exists() {
		elem = w.value(anyv, ep)
	} else {
		it, err := v.List()
		if err != nil {
			return bad
		}
		var elems []*vtype
		for it.Next() {
			elems = append(elems, w.value(it.Value(), appendPath(p, it.Selector())))
		}
		if len(elems) == 0 {
			return &vtype{kind: compiled.List, elem: &vtype{kind: compiled.Void}}
		}
		elem = elems[0]
		for _, e := range elems[1:] {
			if !sameVType(elem, e) {
				w.add(p, Unknown, RuleUnknownListHeterogeneous,
					fmt.Sprintf("is the closed list %s, whose elements do not all map to one type; capnp lists are homogeneous", fmtv(v)), posOf(v))
				return bad
			}
		}
	}
	if elem.isUnion() {
		// capnp lists cannot hold unions: box the element.
		elem = &vtype{kind: compiled.Struct, strct: &vstruct{role: compiled.UnionBox, fields: []vfield{{fixed: boxField, typ: elem}}}}
	}
	return &vtype{kind: compiled.List, elem: elem}
}

// mapOf maps a [string]: T struct to a list of entries.
func (w *walker) mapOf(val cue.Value, p cue.Path) *vtype {
	entry := &vstruct{role: compiled.MapEntry, fields: []vfield{
		{fixed: "key", typ: &vtype{kind: compiled.Text}},
		{fixed: valueMember, typ: w.value(val, appendPath(p, cue.AnyString))},
	}}
	return &vtype{kind: compiled.List, elem: &vtype{kind: compiled.Struct, strct: entry}}
}

// kindNames name the alternative of each kind in a union.
var kindNames = map[cue.Kind]string{
	cue.NullKind: kindNull, cue.BoolKind: "bool", cue.IntKind: "int", cue.StringKind: kindString,
	cue.BytesKind: kindBytes, cue.StructKind: "struct", cue.ListKind: kindList,
}

// disjunction maps a value with several alternatives. Alternatives of one
// kind collapse where they are all constants of that kind (an enum, a bounded
// integer, a bool) or all structs discriminated by a tag; the collapsed
// alternatives form a union by kind, or are the type itself where there is
// one kind.
func (w *walker) disjunction(v cue.Value, brs []cue.Value, p cue.Path) *vtype {
	type group struct {
		kind cue.Kind
		brs  []cue.Value
	}
	var groups []*group
	for _, b := range brs {
		k := b.IncompleteKind()
		if k == cue.TopKind {
			w.add(p, Rejected, RuleTop, fmt.Sprintf("is %s, whose alternative _ has no type", fmtv(v)), posOf(v))
			return bad
		}
		if _, ok := kindNames[k]; !ok {
			if k&cue.NumberKind != 0 && k&^cue.NumberKind == 0 {
				w.add(p, Rejected, RuleFloat, fmt.Sprintf("has the alternative %s, a number that may be a float; floats are outside the compiled target", fmtv(b)), posOf(v))
				return bad
			}
			w.add(p, Rejected, RuleDisjunctionUndiscriminated,
				fmt.Sprintf("is %s, whose alternative %s has the kinds %s, which the compiler cannot separate", fmtv(v), fmtv(b), k), posOf(v))
			return bad
		}
		var g *group
		for _, gg := range groups {
			if gg.kind == k {
				g = gg
			}
		}
		if g == nil {
			g = &group{kind: k}
			groups = append(groups, g)
		}
		g.brs = append(g.brs, b)
	}

	var members []vmember
	for _, g := range groups {
		if g.kind == cue.StructKind && len(g.brs) > 1 {
			tags, ok := w.tags(v, g.brs, p)
			if !ok {
				return bad
			}
			members = append(members, tags...)
			continue
		}
		t, ok := w.collapse(v, g.kind, g.brs, p)
		if !ok {
			return bad
		}
		if len(groups) == 1 {
			return t
		}
		m := vmember{name: "as" + upperFirst(kindNames[g.kind]), role: compiled.KindMember, kind: kindNames[g.kind], typ: t}
		if g.kind == cue.NullKind {
			m.typ = nil
		}
		members = append(members, m)
	}
	return &vtype{union: members}
}

// collapse maps the alternatives of one kind to one type: a single
// alternative as it is; several string constants as an enum, several integer
// constants as the integer containing them, several bools as bool, nulls as
// null. Several alternatives of another kind, or of a kind with a non-constant
// among them, have no discriminant.
func (w *walker) collapse(v cue.Value, k cue.Kind, brs []cue.Value, p cue.Path) (*vtype, bool) {
	if len(brs) == 1 {
		return w.single(brs[0], p), true
	}
	undiscriminated := func() (*vtype, bool) {
		w.add(p, Rejected, RuleDisjunctionUndiscriminated,
			fmt.Sprintf("is %s, with several alternatives of kind %s that are not all constants; no discriminant can be derived from a value", fmtv(v), k), posOf(v))
		return bad, false
	}
	switch k {
	case cue.NullKind:
		return &vtype{kind: compiled.Void}, true
	case cue.BoolKind:
		return &vtype{kind: compiled.Bool}, true
	case cue.StringKind:
		var vals []string
		for _, b := range brs {
			if !b.IsConcrete() {
				return undiscriminated()
			}
			s, _ := b.String()
			vals = append(vals, s)
		}
		return &vtype{kind: compiled.Enum, enum: vals}, true
	case cue.IntKind:
		var lo, hi *big.Int
		for _, b := range brs {
			if !b.IsConcrete() {
				return undiscriminated()
			}
			n, err := b.Int(nil)
			if err != nil {
				return undiscriminated()
			}
			if lo == nil || n.Cmp(lo) < 0 {
				lo = n
			}
			if hi == nil || n.Cmp(hi) > 0 {
				hi = n
			}
		}
		if kind, ok := intType(lo, hi); ok {
			return &vtype{kind: kind}, true
		}
		w.add(p, Rejected, RuleIntUnbounded, fmt.Sprintf("is %s, whose integer alternatives exceed 64 bits", fmtv(v)), posOf(v))
		return bad, false
	}
	return undiscriminated()
}

// tags discriminates struct alternatives by a tag: a label every alternative
// declares as a regular or required field holding a scalar constant, with a
// different constant in each. The first such label of the first alternative,
// in declaration order, is the tag.
func (w *walker) tags(v cue.Value, brs []cue.Value, p cue.Path) ([]vmember, bool) {
	it, err := brs[0].Fields()
	if err != nil {
		return nil, false
	}
	for it.Next() {
		if !it.Selector().IsString() {
			continue
		}
		label := it.Selector().Unquoted()
		lits := make([]lensir.Literal, 0, len(brs))
		seen := map[string]bool{}
		ok := true
		for _, b := range brs {
			tv := b.LookupPath(cue.MakePath(cue.Str(label)))
			lit, isLit := literalOf(tv)
			if !tv.Exists() || !compat.ConstantTarget(tv) || !isLit || seen[litKey(lit)] {
				ok = false
				break
			}
			seen[litKey(lit)] = true
			lits = append(lits, lit)
		}
		if !ok {
			continue
		}
		members := make([]vmember, 0, len(brs))
		for i, b := range brs {
			lit := lits[i]
			members = append(members, vmember{
				name:     fieldName(label + " " + litText(lit)),
				role:     compiled.TagMember,
				tagLabel: label,
				tag:      &lit,
				typ:      &vtype{kind: compiled.Struct, strct: w.strct(b, p)},
			})
		}
		return members, true
	}
	w.add(p, Rejected, RuleDisjunctionUndiscriminated,
		fmt.Sprintf("is %s, whose struct alternatives share no label holding a distinct constant in each; no discriminant can be derived from a value", fmtv(v)), posOf(v))
	return nil, false
}

// literalOf converts a concrete scalar to a literal.
func literalOf(v cue.Value) (lensir.Literal, bool) {
	if !v.Exists() || !v.IsConcrete() {
		return lensir.Literal{}, false
	}
	switch v.Kind() {
	case cue.NullKind:
		return lensir.Literal{Kind: lensir.Null}, true
	case cue.BoolKind:
		b, _ := v.Bool()
		return lensir.Literal{Kind: lensir.Bool, Bool: b}, true
	case cue.IntKind:
		n, err := v.Int(nil)
		if err != nil {
			return lensir.Literal{}, false
		}
		return lensir.Literal{Kind: lensir.Int, Int: n.String()}, true
	case cue.FloatKind:
		return lensir.Literal{Kind: lensir.Float, Float: fmtv(v)}, true
	case cue.StringKind:
		s, _ := v.String()
		return lensir.Literal{Kind: lensir.String, Str: s}, true
	case cue.BytesKind:
		b, _ := v.Bytes()
		return lensir.Literal{Kind: lensir.Bytes, Str: string(b)}, true
	}
	return lensir.Literal{}, false
}

// litText is the literal's value as text, for naming.
func litText(l lensir.Literal) string {
	switch l.Kind {
	case lensir.Null:
		return kindNull
	case lensir.Bool:
		return fmt.Sprint(l.Bool)
	case lensir.Int:
		return l.Int
	case lensir.Float:
		return l.Float
	}
	return l.Str
}

// litKey identifies a literal's value, kind included.
func litKey(l lensir.Literal) string {
	return fmt.Sprintf("%d:%s", l.Kind, litText(l))
}

// sameLiteral reports whether two optional literals are the same value.
func sameLiteral(a, b *lensir.Literal) bool {
	if a == nil || b == nil {
		return a == b
	}
	return litKey(*a) == litKey(*b)
}

// sameVType reports whether two views of a type are the same.
func sameVType(a, b *vtype) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.kind != b.kind || !sameVType(a.elem, b.elem) || len(a.enum) != len(b.enum) || len(a.union) != len(b.union) {
		return false
	}
	for i := range a.enum {
		if a.enum[i] != b.enum[i] {
			return false
		}
	}
	for i := range a.union {
		if a.union[i].name != b.union[i].name || !sameVType(a.union[i].typ, b.union[i].typ) {
			return false
		}
	}
	if (a.strct == nil) != (b.strct == nil) {
		return false
	}
	if a.strct != nil {
		if a.strct.role != b.strct.role || len(a.strct.fields) != len(b.strct.fields) {
			return false
		}
		for i := range a.strct.fields {
			af, bf := a.strct.fields[i], b.strct.fields[i]
			if af.key() != bf.key() || af.form != bf.form || !sameLiteral(af.def, bf.def) || !sameVType(af.typ, bf.typ) {
				return false
			}
		}
	}
	return true
}

func posOf(vs ...cue.Value) token.Pos {
	for _, v := range vs {
		if v.Exists() && v.Pos().IsValid() {
			return v.Pos()
		}
	}
	return token.NoPos
}

// fmtv formats a value for a message, on one line.
func fmtv(v cue.Value) string {
	return strings.Join(strings.Fields(fmt.Sprint(v)), " ")
}

func appendPath(p cue.Path, sel cue.Selector) cue.Path {
	return cue.MakePath(append(append([]cue.Selector(nil), p.Selectors()...), sel)...)
}

// lookup finds the value at p beneath v, whether each field on the way is
// regular or optional: a plain string selector does not find an optional
// field, and the lineage's paths do not say which form a field has.
func lookup(v cue.Value, p cue.Path) cue.Value {
	sels := p.Selectors()
	conv := make([]cue.Selector, len(sels))
	for i, sel := range sels {
		if sel.Type() == cue.StringLabel {
			sel = sel.Optional()
		}
		conv[i] = sel
	}
	return v.LookupPath(cue.MakePath(conv...))
}
