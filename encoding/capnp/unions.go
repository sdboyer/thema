package capnp

import (
	"fmt"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/encoding/capnp/compiled"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/lensir"
)

// unionWrites lowers the writes of p that name an alternative of a
// disjunction to members of the compiled unions. A Copy's Alts pair each
// source alternative it carries with the target alternative that accepts it;
// the engine reads which source member is set, and sets the target member
// its alternative lands in. A Wrap's Alt is the target alternative its
// literal is an instance of; the engine sets that member. The IR numbers
// alternatives as compat.Branches lists them, as the type mapping does
// (vtype.alts), so each resolves to the member holding it.
//
// A write the compiler cannot place in the compiled unions, beneath a union
// it cannot follow, is a finding under unknown-union-write.
func (cp *compiler) unionWrites(p *lensir.Program) []compiled.UnionWrite {
	from, to := thema.SyntacticVersion(p.Edge.From), thema.SyntacticVersion(p.Edge.To)
	var out []compiled.UnionWrite
	// chosen holds the target alternative each enclosing Wrap with an Alt
	// writes, by its path, so that a write beneath it follows that member.
	chosen := map[string]int{}
	idx := 0
	var walk func(nodes []lensir.Node)
	walk = func(nodes []lensir.Node) {
		for _, n := range nodes {
			i := idx
			idx++
			switch n := n.(type) {
			case *lensir.Copy:
				if len(n.Alts) == 0 {
					continue
				}
				u, ok := cp.lowerCopy(from, to, n, chosen)
				if !ok {
					cp.addLens(p.Edge, n.To, Unknown, RuleUnknownUnionWrite,
						fmt.Sprintf("copies %s, a disjunction the compiler cannot place among the members of the compiled unions", inputRef(p.Edge, n.From)))
					continue
				}
				u.Node = i
				out = append(out, u)
			case *lensir.Wrap:
				if n.Alt != nil {
					t, ok := cp.unionAt(to, n.To, chosen)
					if !ok {
						cp.addLens(p.Edge, n.To, Unknown, RuleUnknownUnionWrite,
							"writes an alternative of a disjunction the compiler cannot place among the members of the compiled unions")
					} else {
						out = append(out, compiled.UnionWrite{Node: i, Target: []uint16{t.member(*n.Alt)}})
					}
					key := n.To.String()
					chosen[key] = *n.Alt
					walk(n.Children)
					delete(chosen, key)
					continue
				}
				walk(n.Children)
			case *lensir.Each:
				walk(n.Body)
			case *lensir.Branch:
				walk(n.Then)
			case *lensir.Head:
				walk(n.Then)
				walk(n.Else)
			case *lensir.Table:
				for _, e := range n.Entries {
					walk(e.Value)
				}
			}
		}
	}
	walk(p.Result)
	return out
}

// inputRef names the input at p as the lens does: v0_0.r.y.
func inputRef(e lensir.Edge, p lensir.Path) string {
	param := cuetil.LensParam(e.From[0], e.From[1])
	if len(p) == 0 {
		return param
	}
	return param + "." + p.String()
}

// lowerCopy resolves a copy's Alts: the source member of each source
// alternative, where the source is a union, and the target member of each
// target alternative.
func (cp *compiler) lowerCopy(from, to thema.SyntacticVersion, c *lensir.Copy, chosen map[string]int) (compiled.UnionWrite, bool) {
	var u compiled.UnionWrite
	t, ok := cp.unionAt(to, c.To, chosen)
	if !ok {
		return u, false
	}
	s, sok := cp.unionAt(from, c.From, nil)
	for _, a := range c.Alts {
		if sok {
			u.Source = append(u.Source, s.member(a.From))
		} else if a.From != 0 {
			// A source that is not a union has one alternative.
			return u, false
		}
		u.Target = append(u.Target, t.member(a.To))
	}
	return u, true
}

// A placedUnion is a disjunction at a path, as the type mapping saw it and as
// the compiled field holds it.
type placedUnion struct {
	vt      *vtype
	members []*compiled.Member
}

// member is the ordinal of the compiled member holding alternative alt.
func (u placedUnion) member(alt int) uint16 {
	name := u.vt.union[u.vt.alts[alt]].name
	for _, m := range u.members {
		if m.Name == name {
			return m.Ordinal
		}
	}
	panic(fmt.Sprintf("capnp: union member %s of alternative %d is not compiled", name, alt))
}

// unionAt finds the disjunction at p in version v: the type the mapping gave
// it, walked from the version's root struct, beside the compiled field
// holding it, walked from the version's root node. A union in the middle of
// p is followed where chosen names the alternative written at its path (a
// write beneath a Wrap with an Alt); otherwise it cannot be.
func (cp *compiler) unionAt(v thema.SyntacticVersion, p lensir.Path, chosen map[string]int) (placedUnion, bool) {
	vt := &vtype{kind: compiled.Struct, strct: cp.trees[v]}
	ct := &compiled.Type{Kind: compiled.Struct, Node: cp.gens.rootOf(v)}
	var members []*compiled.Member
	for k, st := range p {
		if vt.isUnion() {
			alt, ok := chosen[p[:k].String()]
			if !ok || alt >= len(vt.alts) {
				return placedUnion{}, false
			}
			m := placedUnion{vt: vt, members: members}.member(alt)
			vt = vt.union[vt.alts[alt]].typ
			ct = memberType(members, m)
		}
		if vt == nil || ct == nil {
			return placedUnion{}, false
		}
		members = nil
		switch {
		case vt.kind == compiled.List && vt.elem != nil && vt.elem.strct != nil && vt.elem.strct.role == compiled.MapEntry && !st.IsAny && !st.IsIndex:
			entry := cp.c.Node(ct.Elem.Node)
			f := fieldNamed(entry, valueMember)
			if f == nil {
				return placedUnion{}, false
			}
			vt, ct, members = vt.elem.strct.fields[1].typ, f.Type, f.Union
		case vt.kind == compiled.List && (st.IsAny || st.IsIndex):
			vt, ct = vt.elem, ct.Elem
			if vt != nil && vt.strct != nil && vt.strct.role == compiled.UnionBox {
				box := cp.c.Node(ct.Node)
				if box == nil || len(box.Fields) != 1 {
					return placedUnion{}, false
				}
				vt, ct, members = vt.strct.fields[0].typ, box.Fields[0].Type, box.Fields[0].Union
			}
		case vt.kind == compiled.Struct && vt.strct != nil && !st.IsAny && !st.IsIndex:
			vf := vt.strct.field(st.Label)
			f := fieldLabeled(cp.c.Node(ct.Node), st.Label)
			if vf == nil || f == nil {
				return placedUnion{}, false
			}
			vt, ct, members = vf.typ, f.Type, f.Union
			if ct == nil && !vt.isUnion() {
				// An optional field: its value is the value member.
				ct = memberType(f.Union, valueOrdinal(f.Union))
			}
		default:
			return placedUnion{}, false
		}
	}
	if !vt.isUnion() || len(members) == 0 {
		return placedUnion{}, false
	}
	return placedUnion{vt: vt, members: members}, true
}

// fieldLabeled finds the field of n declared for label.
func fieldLabeled(n *compiled.Node, label string) *compiled.Field {
	if n == nil {
		return nil
	}
	for _, f := range n.Fields {
		if f.Label == label {
			return f
		}
	}
	return nil
}

// fieldNamed finds the field of n with the capnp name name.
func fieldNamed(n *compiled.Node, name string) *compiled.Field {
	if n == nil {
		return nil
	}
	for _, f := range n.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// memberType is the type of the member with ordinal o; nil where there is none.
func memberType(ms []*compiled.Member, o uint16) *compiled.Type {
	for _, m := range ms {
		if m.Ordinal == o {
			return m.Type
		}
	}
	return nil
}

// valueOrdinal is the ordinal of an optional field's value member.
func valueOrdinal(ms []*compiled.Member) uint16 {
	for _, m := range ms {
		if m.Role == compiled.Value {
			return m.Ordinal
		}
	}
	return 0
}
