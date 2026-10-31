package capnp

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/encoding/capnp/compiled"
)

// This file turns each version's type tree (mapping.go) into struct
// generations: one struct per major version while successive minors are
// capnp-append-compatible, a new struct from the first minor that is not.
// Names, ordinals and type IDs are assigned here, by first appearance in the
// lineage's history, so that appending a version never changes an earlier
// one's.

// generations holds the structs of a lineage as they are built.
type generations struct {
	ns, lineage string
	gens        []*generation
	roots       map[thema.SyntacticVersion]uint64
	byID        map[uint64]*gnode
	// version is the version being added, and field the field being named,
	// for the findings a name collision raises.
	version  thema.SyntacticVersion
	field    *vfield
	findings []Finding
}

// collide records that name, claimed for what, is held by other in the same
// scope: two of the author's labels or values map to one capnp name.
func (gs *generations) collide(name, what, other string) {
	var p cue.Path
	var pos token.Pos
	if gs.field != nil {
		p, pos = gs.field.path, gs.field.pos
	}
	gs.findings = append(gs.findings, Finding{
		Version: gs.version, Path: p, Verdict: Rejected, Rule: RuleNameCollision, Pos: pos,
		Detail: fmt.Sprintf("collides: %s and %s are both named %s in Cap'n Proto, which drops underscores and joins words in camel case; the names in one struct must differ", other, what, name),
	})
}

// collideReserved records that what, one of the author's labels, has the
// name of the field the compiler adds to every schema struct for undeclared
// data. Renaming the label would leave a reader no way to tell it is the
// author's.
func (gs *generations) collideReserved(name, what string) {
	var p cue.Path
	var pos token.Pos
	if gs.field != nil {
		p, pos = gs.field.path, gs.field.pos
	}
	gs.findings = append(gs.findings, Finding{
		Version: gs.version, Path: p, Verdict: Rejected, Rule: RuleNameCollision, Pos: pos,
		Detail: fmt.Sprintf("collides: %s is named %s in Cap'n Proto, the name of the field every struct reserves for undeclared data", what, name),
		Help:   fmt.Sprintf("rename it: %s is reserved for undeclared data in the Cap'n Proto encoding", name),
	})
}

// name claims a field's or enumerant's name for what in s, reporting a
// collision with another of the author's names or with the reserved extras
// field.
func (gs *generations) name(s *scope, name, what string) string {
	if other, ok := s.claim(name, what); !ok {
		if other == "\x00"+reserved {
			gs.collideReserved(name, what)
			return name
		}
		gs.collide(name, what, other)
	}
	return name
}

// A generation is one root struct and the versions read through it.
type generation struct {
	major, first uint
	versions     []thema.SyntacticVersion
	root         *gnode
}

// A gnode is a node under construction: the compiled node, and the indexes
// extension needs.
type gnode struct {
	n       *compiled.Node
	next    uint16
	names   *scope
	types   *scope
	byKey   map[string]*compiled.Field
	members map[*compiled.Field]map[string]*compiled.Member
	enums   map[string]bool
	// children are the nodes nested in this one, in declaration order.
	children []*gnode
}

func newGenerations(ns, lineage string) *generations {
	return &generations{ns: ns, lineage: lineage, roots: map[thema.SyntacticVersion]uint64{}, byID: map[uint64]*gnode{}}
}

// add places version v, whose schema maps to tree, in the current generation
// of its major where the generation can be extended to it, and otherwise in
// a new one.
func (gs *generations) add(v thema.SyntacticVersion, tree *vstruct) {
	var g *generation
	if n := len(gs.gens); n > 0 && gs.gens[n-1].major == v[0] {
		g = gs.gens[n-1]
	}
	if g == nil || !gs.compatible(g.root, tree) {
		g = &generation{major: v[0], first: v[1]}
		name := typeName(gs.lineage) + fmt.Sprintf("V%d", v[0])
		if v[1] != 0 {
			name += fmt.Sprintf("M%d", v[1])
		}
		g.root = gs.newNode(nil, name, hashID(gs.ns, gs.lineage, fmt.Sprintf("v%d", v[0]), fmt.Sprintf("m%d", v[1])), compiled.SchemaStruct)
		gs.gens = append(gs.gens, g)
	}
	g.versions = append(g.versions, v)
	gs.version = v
	gs.extend(g.root, tree)
	gs.roots[v] = g.root.n.ID
}

func (gs *generations) rootOf(v thema.SyntacticVersion) uint64 {
	return gs.roots[v]
}

// nodes returns every node, roots in generation order and nested nodes
// depth-first beneath their parent.
func (gs *generations) nodes() []*compiled.Node {
	var out []*compiled.Node
	var walk func(n *gnode)
	walk = func(n *gnode) {
		out = append(out, n.n)
		for _, c := range n.children {
			walk(c)
		}
	}
	for _, g := range gs.gens {
		walk(g.root)
	}
	return out
}

// hashID derives a type ID from its parts: 64 bits of SHA-256 over the parts
// separated by NUL, with the high bit set as capnp requires.
func hashID(parts ...string) uint64 {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return binary.BigEndian.Uint64(h[:8]) | 1<<63
}

// nestedID is the ID of the node named name nested in parent.
func nestedID(parent uint64, name string) uint64 {
	return hashID(fmt.Sprintf("%016x", parent), name)
}

// newNode creates a struct node nested in parent (nil for a root), with the
// extras side-channel at ordinal 0 where it stands for a schema struct.
func (gs *generations) newNode(parent *gnode, name string, id uint64, role compiled.NodeRole) *gnode {
	n := &gnode{
		n:       &compiled.Node{ID: id, Name: name, Kind: compiled.StructNode, Role: role},
		names:   newScope(),
		types:   newScope(),
		byKey:   map[string]*compiled.Field{},
		members: map[*compiled.Field]map[string]*compiled.Member{},
	}
	if parent != nil {
		n.n.Name = parent.n.Name + "." + name
		parent.children = append(parent.children, n)
	}
	gs.byID[id] = n
	if role == compiled.SchemaStruct {
		n.names.claim(reserved, "\x00"+reserved)
		f := &compiled.Field{Name: reserved, Type: &compiled.Type{Kind: compiled.Data}, Ordinal: n.next}
		n.next++
		n.n.Fields = append(n.n.Fields, f)
	}
	return n
}

// newEnum creates an enum node nested in parent.
func (gs *generations) newEnum(parent *gnode, name string, values []string) *gnode {
	id := nestedID(parent.n.ID, name)
	n := &gnode{
		n:     &compiled.Node{ID: id, Name: parent.n.Name + "." + name, Kind: compiled.EnumNode},
		names: newScope(),
		enums: map[string]bool{},
	}
	parent.children = append(parent.children, n)
	gs.byID[id] = n
	gs.extendEnum(n, values)
	return n
}

func (gs *generations) extendEnum(n *gnode, values []string) {
	for _, v := range values {
		if n.enums[v] {
			continue
		}
		n.enums[v] = true
		n.n.Enumerants = append(n.n.Enumerants, compiled.Enumerant{Name: gs.name(n.names, fieldName(v), fmt.Sprintf("the value %q", v)), Value: v, Ordinal: n.next})
		n.next++
	}
}

// members lists the union members a field has in a version: unset where it
// is optional, then its alternatives, or its one value. nil for a plain
// field.
func members(f vfield) []vmember {
	var out []vmember
	if f.form == compiled.Optional {
		out = append(out, vmember{name: "unset", role: compiled.Unset})
	}
	switch {
	case f.typ.isUnion():
		out = append(out, f.typ.union...)
	case f.form == compiled.Optional:
		out = append(out, vmember{name: valueMember, role: compiled.Value, typ: f.typ})
	}
	return out
}

// compatible reports whether the struct n can be extended to the version's
// view vs: every field it has is still there with a type its readers can
// read the old bytes through, and every new field reads as absent or as its
// default from old bytes. The spike (#83) measured each transition.
func (gs *generations) compatible(n *gnode, vs *vstruct) bool {
	for _, vf := range vs.fields {
		f, ok := n.byKey[vf.key()]
		if !ok {
			// A new field: old bytes read it as unset, or as its declared
			// default. A union carries no declared default, so a new
			// defaulted union would read as its first member.
			if vf.form == compiled.Defaulted && vf.typ.isUnion() {
				return false
			}
			continue
		}
		switch {
		case f.Form == vf.form:
		case f.Form == compiled.Required && vf.form == compiled.Optional:
			// The field joins a union at its ordinal, unset at a new one.
		case f.Form == compiled.Required && vf.form == compiled.Defaulted:
			// A default on a pointer field substitutes for null; one on a
			// data field is XORed into old bytes.
			if f.Type == nil || !f.Type.Kind.Pointer() {
				return false
			}
		default:
			// optional → defaulted: a union cannot become a field. The other
			// transitions compat forbids.
			return false
		}
		if f.Form == compiled.Defaulted && vf.form == compiled.Defaulted && !sameLiteral(f.Default, vf.def) {
			return false
		}
		if !gs.fieldCompatible(f, vf) {
			return false
		}
	}
	return true
}

// fieldCompatible compares an existing field's members or type with the
// version's.
func (gs *generations) fieldCompatible(f *compiled.Field, vf vfield) bool {
	want := members(vf)
	if f.Union == nil && len(want) == 0 {
		return gs.typeCompatible(f.Type, vf.typ)
	}
	// Existing members, a plain field counting as its one value.
	have := map[string]*compiled.Type{}
	if f.Union == nil {
		have[valueMember] = f.Type
	} else {
		for _, m := range f.Union {
			have[m.Name] = m.Type
		}
	}
	seen := map[string]bool{}
	for _, m := range want {
		seen[m.name] = true
		t, ok := have[m.name]
		if !ok {
			continue
		}
		if (t == nil) != (m.typ == nil) {
			return false
		}
		if t != nil && !gs.typeCompatible(t, m.typ) {
			return false
		}
	}
	for name := range have {
		if !seen[name] {
			return false
		}
	}
	return true
}

// typeCompatible reports whether old bytes of type t read correctly as the
// version's view vt: the same scalar, a list of a compatible element, an
// enum keeping its values, a struct that is compatible in turn.
func (gs *generations) typeCompatible(t *compiled.Type, vt *vtype) bool {
	if vt.isUnion() || t.Kind != vt.kind {
		return false
	}
	switch t.Kind {
	case compiled.List:
		return gs.typeCompatible(t.Elem, vt.elem)
	case compiled.Enum:
		n := gs.byID[t.Node]
		have := map[string]bool{}
		for _, v := range vt.enum {
			have[v] = true
		}
		for v := range n.enums {
			if !have[v] {
				return false
			}
		}
		return true
	case compiled.Struct:
		n := gs.byID[t.Node]
		return n.n.Role == vt.strct.role && gs.compatible(n, vt.strct)
	}
	return true
}

// extend adds the version's view vs to the struct n, which compatible has
// accepted or which is new: new fields, members and enum values take the
// next ordinals; a field that became optional joins a union at its own.
func (gs *generations) extend(n *gnode, vs *vstruct) {
	outer := gs.field
	defer func() { gs.field = outer }()
	for i := range vs.fields {
		vf := vs.fields[i]
		if vf.fixed == "" {
			gs.field = &vs.fields[i]
		}
		f, ok := n.byKey[vf.key()]
		if !ok {
			f = &compiled.Field{Label: vf.label, Form: vf.form, Default: vf.def}
			switch {
			case vf.fixed == boxField:
				f.Name = ""
			case vf.fixed != "":
				f.Name = gs.name(n.names, vf.fixed, "\x00"+vf.fixed)
			default:
				f.Name = gs.name(n.names, fieldName(vf.label), fmt.Sprintf("the label %q", vf.label))
			}
			n.byKey[vf.key()] = f
			n.n.Fields = append(n.n.Fields, f)
			if ms := members(vf); len(ms) > 0 {
				n.members[f] = map[string]*compiled.Member{}
				for _, m := range ms {
					gs.addMember(n, f, m)
				}
			} else {
				f.Type = gs.typeOf(n, typeName(f.Name), vf.typ)
				f.Ordinal = n.next
				n.next++
			}
			continue
		}

		if f.Form == compiled.Required && vf.form == compiled.Optional && f.Union == nil {
			// The value keeps its ordinal as the first member.
			f.Union = []*compiled.Member{{Name: valueMember, Role: compiled.Value, Ordinal: f.Ordinal, Type: f.Type}}
			n.members[f] = map[string]*compiled.Member{valueMember: f.Union[0]}
			f.Type, f.Ordinal = nil, 0
		}
		f.Form = vf.form
		if vf.form == compiled.Defaulted {
			f.Default = vf.def
		}
		if f.Union == nil {
			gs.extendType(f.Type, typeName(f.Name), vf.typ)
			continue
		}
		for _, m := range members(vf) {
			if have, ok := n.members[f][m.name]; ok {
				if have.Type != nil {
					gs.extendType(have.Type, memberBase(f, m), m.typ)
				}
				continue
			}
			gs.addMember(n, f, m)
		}
	}
}

// memberBase is the base of the names of the types a member's alternative
// derives: the field's for its value or its one alternative of a kind, the
// field's and the tag's for a tagged alternative.
func memberBase(f *compiled.Field, m vmember) string {
	if f.Name == "" {
		// The anonymous union of a box: the member's name alone.
		return typeName(m.name)
	}
	base := typeName(f.Name)
	if m.role == compiled.TagMember {
		base += typeName(m.name)
	}
	return base
}

// addMember adds a member to f's union at the next ordinal.
func (gs *generations) addMember(n *gnode, f *compiled.Field, m vmember) {
	cm := &compiled.Member{Name: m.name, Role: m.role, Ordinal: n.next, Kind: m.kind, TagLabel: m.tagLabel, Tag: m.tag}
	n.next++
	if m.typ != nil {
		cm.Type = gs.typeOf(n, memberBase(f, m), m.typ)
	}
	f.Union = append(f.Union, cm)
	n.members[f][m.name] = cm
}

// typeOf creates the capnp type of vt, declaring the nodes it needs nested
// in n under names derived from base.
func (gs *generations) typeOf(n *gnode, base string, vt *vtype) *compiled.Type {
	switch vt.kind {
	case compiled.List:
		suffix := "Item"
		if vt.elem.strct != nil && vt.elem.strct.role == compiled.MapEntry {
			suffix = "Entry"
		}
		return &compiled.Type{Kind: compiled.List, Elem: gs.typeOf(n, base+suffix, vt.elem)}
	case compiled.Enum:
		e := gs.newEnum(n, gs.typeName(n, base), vt.enum)
		return &compiled.Type{Kind: compiled.Enum, Node: e.n.ID}
	case compiled.Struct:
		name := gs.typeName(n, base)
		s := gs.newNode(n, name, nestedID(n.n.ID, name), vt.strct.role)
		gs.extend(s, vt.strct)
		return &compiled.Type{Kind: compiled.Struct, Node: s.n.ID}
	}
	return &compiled.Type{Kind: vt.kind}
}

// typeName claims base for a type nested in n, standing for the field being
// named. Two fields whose derived type names agree (a field kindA beside a
// field kind with an alternative tagged a) collide.
func (gs *generations) typeName(n *gnode, base string) string {
	what := base
	if gs.field != nil {
		what = "the type of " + gs.field.path.String()
	}
	if other, ok := n.types.claim(base, what); !ok {
		gs.collide(base, what, other)
	}
	return base
}

// extendType extends the nodes beneath an existing type to the version's
// view, which typeCompatible has accepted.
func (gs *generations) extendType(t *compiled.Type, base string, vt *vtype) {
	switch t.Kind {
	case compiled.List:
		suffix := "Item"
		if vt.elem.strct != nil && vt.elem.strct.role == compiled.MapEntry {
			suffix = "Entry"
		}
		gs.extendType(t.Elem, base+suffix, vt.elem)
	case compiled.Enum:
		gs.extendEnum(gs.byID[t.Node], vt.enum)
	case compiled.Struct:
		gs.extend(gs.byID[t.Node], vt.strct)
	}
}
