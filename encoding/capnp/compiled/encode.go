package compiled

import (
	"fmt"

	"capnproto.org/go/capnp/v3"

	"github.com/sdboyer/thema/encoding/capnp/compiled/wire"
	"github.com/sdboyer/thema/internal/lensir"
	"github.com/sdboyer/thema/lacuna"
)

// Encode serializes c as the artifact message wire/compiled.capnp declares:
// a single-segment, unpacked Cap'n Proto message. The same Compiled encodes
// to the same bytes.
func Encode(c *Compiled) ([]byte, error) {
	msg, seg, err := capnp.NewMessage(capnp.SingleSegment(nil))
	if err != nil {
		return nil, err
	}
	a, err := wire.NewRootArtifact(seg)
	if err != nil {
		return nil, err
	}
	e := &encoder{seg: seg}
	e.artifact(a, c)
	if e.err != nil {
		return nil, e.err
	}
	return msg.Marshal()
}

// Decode reads an artifact message Encode produced back into a Compiled.
func Decode(b []byte) (*Compiled, error) {
	msg, err := capnp.Unmarshal(b)
	if err != nil {
		return nil, err
	}
	a, err := wire.ReadRootArtifact(msg)
	if err != nil {
		return nil, err
	}
	d := &decoder{}
	c := d.artifact(a)
	if d.err != nil {
		return nil, d.err
	}
	return c, nil
}

// The artifact's counts, ordinals and indices are narrower than Go's ints;
// a lineage's lists, ordinals and versions are far below their ranges.
func n32(n int) int32   { return int32(n) }  //nolint:gosec // see above
func u32(n int) uint32  { return uint32(n) } //nolint:gosec // see above
func v32(v uint) uint32 { return uint32(v) } //nolint:gosec // see above

// enum converts one of the Go model's enumerations, which count from 1 (zero
// being unset), to the artifact's, which count from 0; enum0 one that counts
// from 0 in both.
func enum[W ~uint16, G ~int](g G) W  { return W(g - 1) }
func enum0[W ~uint16, G ~int](g G) W { return W(g) }

// An encoder writes a Compiled into a message, keeping the first error.
type encoder struct {
	seg *capnp.Segment
	err error
}

func (e *encoder) check(err error) {
	if e.err == nil && err != nil {
		e.err = err
	}
}

func (e *encoder) artifact(a wire.Artifact, c *Compiled) {
	e.check(a.SetNamespace(c.Namespace))
	e.check(a.SetLineage(c.Lineage))
	e.check(a.SetSchema(c.Schema))
	if c.Layout != nil {
		e.check(a.SetLayout(c.Layout))
	}
	vs, err := a.NewVersions(n32(len(c.Versions)))
	e.check(err)
	for i, v := range c.Versions {
		w := vs.At(i)
		e.synver(w.NewVersion, [2]uint{v.Major, v.Minor})
		e.check(w.SetId(v.ID))
		w.SetRoot(v.Root)
		e.structs(w, v.Structs)
	}
	ns, err := a.NewNodes(n32(len(c.Nodes)))
	e.check(err)
	for i, n := range c.Nodes {
		e.node(ns.At(i), n)
	}
	es, err := a.NewEdges(n32(len(c.Edges)))
	e.check(err)
	for i, ed := range c.Edges {
		e.edge(es.At(i), ed)
	}
	cs, err := a.NewChecks(n32(len(c.Checks)))
	e.check(err)
	for i, ck := range c.Checks {
		w := cs.At(i)
		e.synver(w.NewVersion, ck.Version)
		w.SetNode(ck.Node)
		e.check(w.SetLabel(ck.Label))
		e.check(w.SetSource(ck.Source))
	}
	ms, err := a.NewMessages(n32(len(c.Messages)))
	e.check(err)
	for i, m := range c.Messages {
		w := ms.At(i)
		w.SetCause(enum[wire.AsideCause](m.Cause))
		w.SetForm(enum[wire.MessageForm](m.Form))
		e.check(w.SetType(string(m.Type)))
		e.check(w.SetText(m.Text))
	}
}

func (e *encoder) structs(w wire.Version, ss []StructShape) {
	ws, err := w.NewStructs(n32(len(ss)))
	e.check(err)
	for i, s := range ss {
		x := ws.At(i)
		e.path(x.NewPath, s.Path)
		x.SetNode(s.Node)
		ls, lerr := x.NewLabels(n32(len(s.Labels)))
		e.check(lerr)
		for j, l := range s.Labels {
			e.check(ls.Set(j, l))
		}
		x.SetOpen(s.Open)
	}
}

func (e *encoder) fate(w wire.Edge, f *Fate) {
	x, err := w.NewFate()
	e.check(err)
	rs, err := x.NewReads(n32(len(f.Reads)))
	e.check(err)
	for i, r := range f.Reads {
		e.path(rs.At(i).NewSteps, r)
	}
	e.pairs(x.NewReaders, f.Readers)
	e.pairs(x.NewCopies, f.Copies)
	ns, err := x.NewNamed(n32(len(f.Named)))
	e.check(err)
	for i, n := range f.Named {
		y := ns.At(i)
		e.path(y.NewPath, n.Path)
		y.SetLacuna(u32(n.Lacuna))
		y.SetField(u32(n.Field))
	}
}

func (e *encoder) pairs(newer func(int32) (wire.PathPair_List, error), ps []PathPair) {
	ws, err := newer(n32(len(ps)))
	e.check(err)
	for i, p := range ps {
		w := ws.At(i)
		e.path(w.NewResult, p.Result)
		e.path(w.NewInput, p.Input)
	}
}

func (e *encoder) synver(newer func() (wire.SynVer, error), v [2]uint) {
	w, err := newer()
	e.check(err)
	w.SetMajor(v32(v[0]))
	w.SetMinor(v32(v[1]))
}

func (e *encoder) node(w wire.Node, n *Node) {
	w.SetId(n.ID)
	e.check(w.SetName(n.Name))
	w.SetKind(enum[wire.NodeKind](n.Kind))
	if n.Role != 0 {
		w.SetRole(enum[wire.NodeRole](n.Role))
	}
	fs, err := w.NewFields(n32(len(n.Fields)))
	e.check(err)
	for i, f := range n.Fields {
		e.field(fs.At(i), f)
	}
	es, err := w.NewEnumerants(n32(len(n.Enumerants)))
	e.check(err)
	for i, en := range n.Enumerants {
		x := es.At(i)
		e.check(x.SetName(en.Name))
		e.check(x.SetValue(en.Value))
		x.SetOrdinal(en.Ordinal)
	}
}

func (e *encoder) field(w wire.Field, f *Field) {
	e.check(w.SetLabel(f.Label))
	e.check(w.SetName(f.Name))
	w.SetForm(enum0[wire.Form](f.Form))
	if f.Type != nil {
		t, err := w.NewType()
		e.check(err)
		e.typ(t, f.Type)
	}
	w.SetOrdinal(f.Ordinal)
	ms, err := w.NewUnion(n32(len(f.Union)))
	e.check(err)
	for i, m := range f.Union {
		e.member(ms.At(i), m)
	}
	if f.Default != nil {
		l, err := w.NewDefault()
		e.check(err)
		e.literal(l, *f.Default)
	}
}

func (e *encoder) member(w wire.Member, m *Member) {
	e.check(w.SetName(m.Name))
	w.SetRole(enum[wire.MemberRole](m.Role))
	w.SetOrdinal(m.Ordinal)
	if m.Type != nil {
		t, err := w.NewType()
		e.check(err)
		e.typ(t, m.Type)
	}
	e.check(w.SetKind(m.Kind))
	e.check(w.SetTagLabel(m.TagLabel))
	if m.Tag != nil {
		l, err := w.NewTag()
		e.check(err)
		e.literal(l, *m.Tag)
	}
}

func (e *encoder) typ(w wire.Type, t *Type) {
	w.SetKind(enum[wire.TypeKind](t.Kind))
	if t.Elem != nil {
		el, err := w.NewElem()
		e.check(err)
		e.typ(el, t.Elem)
	}
	w.SetNode(t.Node)
}

func (e *encoder) literal(w wire.Literal, l lensir.Literal) {
	w.SetKind(enum[wire.LiteralKind](l.Kind))
	switch l.Kind {
	case lensir.Bool:
		w.SetBool(l.Bool)
	case lensir.Int:
		e.check(w.SetInt(l.Int))
	case lensir.Float:
		e.check(w.SetFloat(l.Float))
	case lensir.String:
		e.check(w.SetStr(l.Str))
	case lensir.Bytes:
		e.check(w.SetBytes([]byte(l.Str)))
	}
}

func (e *encoder) edge(w wire.Edge, ed *Edge) {
	e.synver(w.NewFrom, ed.From)
	e.synver(w.NewTo, ed.To)
	w.SetSynthesized(ed.Synthesized)
	p := ed.Program
	rs, err := w.NewResult(n32(len(p.Result)))
	e.check(err)
	e.nodes(rs, p.Result)
	ls, err := w.NewLacunas(n32(len(p.Lacunas)))
	e.check(err)
	for i, l := range p.Lacunas {
		e.lacuna(ls.At(i), l)
	}
	cs, err := w.NewCel(n32(len(ed.CEL)))
	e.check(err)
	for i, c := range ed.CEL {
		x := cs.At(i)
		x.SetRole(enum[wire.CelRole](c.Role))
		x.SetNode(u32(c.Node))
		x.SetLacuna(u32(c.Lacuna))
		x.SetField(u32(c.Field))
		e.check(x.SetSource(c.Source))
		vs, err := x.NewVars(n32(len(c.Vars)))
		e.check(err)
		for j, v := range c.Vars {
			y := vs.At(j)
			e.check(y.SetName(v.Name))
			e.path(y.NewPath, v.Path)
			y.SetRole(enum[wire.VarRole](v.Role))
			e.check(y.SetType(v.Type))
		}
	}
	if ed.Fate != nil {
		e.fate(w, ed.Fate)
	}
	if len(ed.Unions) == 0 {
		// Left null: an edge without union writes encodes as it did before
		// the field was appended.
		return
	}
	us, uerr := w.NewUnions(n32(len(ed.Unions)))
	e.check(uerr)
	for i, u := range ed.Unions {
		x := us.At(i)
		x.SetNode(u32(u.Node))
		if len(u.Source) > 0 {
			ss, serr := x.NewSource(n32(len(u.Source)))
			e.check(serr)
			for j, o := range u.Source {
				ss.Set(j, o)
			}
		}
		ts, terr := x.NewTarget(n32(len(u.Target)))
		e.check(terr)
		for j, o := range u.Target {
			ts.Set(j, o)
		}
	}
}

func (e *encoder) path(newer func(int32) (wire.Step_List, error), p lensir.Path) {
	ss, err := newer(n32(len(p)))
	e.check(err)
	for i, st := range p {
		w := ss.At(i)
		switch {
		case st.IsAny:
			w.SetAny()
		case st.IsIndex:
			w.SetIndex(u32(st.Index))
		default:
			e.check(w.SetLabel(st.Label))
		}
	}
}

func (e *encoder) nodes(ws wire.IRNode_List, ns []lensir.Node) {
	for i, n := range ns {
		e.irnode(ws.At(i), n)
	}
}

func (e *encoder) nodeList(newer func(int32) (wire.IRNode_List, error), ns []lensir.Node) {
	ws, err := newer(n32(len(ns)))
	e.check(err)
	e.nodes(ws, ns)
}

func (e *encoder) tests(newer func(int32) (wire.Test_List, error), ts []lensir.Test) {
	ws, err := newer(n32(len(ts)))
	e.check(err)
	for i, t := range ts {
		w := ws.At(i)
		w.SetKind(enum[wire.TestKind](t.Kind))
		e.path(w.NewPath, t.Path)
		w.SetNeg(t.Neg)
		if t.Kind == lensir.Equality {
			l, err := w.NewValue()
			e.check(err)
			e.literal(l, t.Value)
		}
		e.check(w.SetType(t.Type))
		w.SetSafe(t.Safe)
	}
}

func (e *encoder) irnode(w wire.IRNode, n lensir.Node) {
	switch n := n.(type) {
	case *lensir.Copy:
		w.SetCopy()
		g := w.Copy()
		e.path(g.NewTo, n.To)
		e.path(g.NewFrom, n.From)
		if len(n.Alts) > 0 {
			as, err := g.NewAlts(n32(len(n.Alts)))
			e.check(err)
			for i, a := range n.Alts {
				as.At(i).SetFrom(u32(a.From))
				as.At(i).SetTo(u32(a.To))
			}
		}
	case *lensir.Constant:
		w.SetConstant()
		g := w.Constant()
		e.path(g.NewTo, n.To)
		l, err := g.NewValue()
		e.check(err)
		e.literal(l, n.Value)
	case *lensir.Wrap:
		w.SetWrap()
		g := w.Wrap()
		e.path(g.NewTo, n.To)
		g.SetKind(enum[wire.WrapKind](n.Kind))
		g.SetLen(u32(n.Len))
		if n.Alt != nil {
			g.SetAlt(n32(*n.Alt))
		}
		e.nodeList(g.NewChildren, n.Children)
	case *lensir.Each:
		w.SetEach()
		g := w.Each()
		e.path(g.NewTo, n.To)
		e.path(g.NewSource, n.Source)
		e.nodeList(g.NewBody, n.Body)
	case *lensir.Branch:
		w.SetBranch()
		g := w.Branch()
		e.tests(g.NewWhen, n.When)
		e.nodeList(g.NewThen, n.Then)
	case *lensir.Head:
		w.SetHead()
		g := w.Head()
		e.path(g.NewTo, n.To)
		e.tests(g.NewWhen, n.When)
		e.nodeList(g.NewThen, n.Then)
		e.nodeList(g.NewElse, n.Else)
	case *lensir.Table:
		w.SetTable()
		g := w.Table()
		e.path(g.NewTo, n.To)
		e.path(g.NewKey, n.Key)
		es, err := g.NewEntries(n32(len(n.Entries)))
		e.check(err)
		for i, en := range n.Entries {
			x := es.At(i)
			e.check(x.SetKey(en.Key))
			e.nodeList(x.NewValue, en.Value)
		}
	case *lensir.Computed:
		w.SetComputed()
		g := w.Computed()
		e.path(g.NewTo, n.To)
		x, err := g.NewExpr()
		e.check(err)
		e.expr(x, n.Expr)
	default:
		e.check(fmt.Errorf("lens IR node of type %T has no artifact encoding", n))
	}
}

func (e *encoder) exprInto(newer func() (wire.Expr, error), x lensir.Expr) {
	w, err := newer()
	e.check(err)
	e.expr(w, x)
}

func (e *encoder) expr(w wire.Expr, x lensir.Expr) {
	switch x := x.(type) {
	case lensir.Literal:
		l, err := w.NewLiteral()
		e.check(err)
		e.literal(l, x)
	case *lensir.Ref:
		e.path(w.NewRef, x.Path)
	case *lensir.ResultRef:
		e.path(w.NewResultRef, x.Path)
	case *lensir.Unary:
		w.SetUnary()
		g := w.Unary()
		e.check(g.SetOp(x.Op))
		e.exprInto(g.NewX, x.X)
	case *lensir.Binary:
		w.SetBinary()
		g := w.Binary()
		e.check(g.SetOp(x.Op))
		e.exprInto(g.NewX, x.X)
		e.exprInto(g.NewY, x.Y)
	case *lensir.Interp:
		ps, err := w.NewInterp(n32(len(x.Parts)))
		e.check(err)
		for i, p := range x.Parts {
			e.expr(ps.At(i), p)
		}
	case *lensir.Call:
		w.SetCall()
		g := w.Call()
		e.check(g.SetFn(x.Fn))
		as, err := g.NewArgs(n32(len(x.Args)))
		e.check(err)
		for i, a := range x.Args {
			e.expr(as.At(i), a)
		}
	case *lensir.Match:
		e.tests(w.NewMatch, x.Tests)
	case *lensir.Cond:
		w.SetCond()
		g := w.Cond()
		e.tests(g.NewWhen, x.When)
		e.exprInto(g.NewThen, x.Then)
		e.exprInto(g.NewElse, x.Else)
	default:
		e.check(fmt.Errorf("lens IR expression of type %T has no artifact encoding", x))
	}
}

func (e *encoder) lacuna(w wire.Lacuna, l lensir.Lacuna) {
	if l.When != nil {
		e.exprInto(w.NewWhen, l.When)
	}
	e.fieldRefs(w.NewSourceFields, l.SourceFields)
	e.fieldRefs(w.NewTargetFields, l.TargetFields)
	if l.Message != nil {
		e.exprInto(w.NewMsg, l.Message)
	}
	e.check(w.SetType(string(l.Type)))
}

func (e *encoder) fieldRefs(newer func(int32) (wire.FieldRef_List, error), frs []lensir.FieldRef) {
	ws, err := newer(n32(len(frs)))
	e.check(err)
	for i, fr := range frs {
		w := ws.At(i)
		e.check(w.SetPath(fr.Path))
		e.tests(w.NewWhen, fr.When)
		if fr.Value != nil {
			e.exprInto(w.NewValue, fr.Value)
		}
	}
}

// A decoder reads a message back into a Compiled, keeping the first error.
type decoder struct {
	err error
}

func (d *decoder) check(err error) {
	if d.err == nil && err != nil {
		d.err = err
	}
}

func (d *decoder) str(s string, err error) string {
	d.check(err)
	return s
}

func (d *decoder) artifact(a wire.Artifact) *Compiled {
	c := &Compiled{
		Namespace: d.str(a.Namespace()),
		Lineage:   d.str(a.Lineage()),
		Schema:    d.str(a.Schema()),
	}
	if a.HasLayout() {
		b, err := a.Layout()
		d.check(err)
		c.Layout = append([]byte(nil), b...)
	}
	vs, err := a.Versions()
	d.check(err)
	for i := 0; i < vs.Len(); i++ {
		w := vs.At(i)
		sv, serr := w.Version()
		d.check(serr)
		c.Versions = append(c.Versions, Version{
			Major: uint(sv.Major()), Minor: uint(sv.Minor()),
			ID: d.str(w.Id()), Root: w.Root(),
			Structs: d.structs(w.Structs()),
		})
	}
	ns, err := a.Nodes()
	d.check(err)
	for i := 0; i < ns.Len(); i++ {
		c.Nodes = append(c.Nodes, d.node(ns.At(i)))
	}
	es, err := a.Edges()
	d.check(err)
	for i := 0; i < es.Len(); i++ {
		c.Edges = append(c.Edges, d.edge(es.At(i)))
	}
	cs, err := a.Checks()
	d.check(err)
	for i := 0; i < cs.Len(); i++ {
		w := cs.At(i)
		c.Checks = append(c.Checks, &Check{
			Version: d.synver(w.Version()), Node: w.Node(),
			Label: d.str(w.Label()), Source: d.str(w.Source()),
		})
	}
	ms, err := a.Messages()
	d.check(err)
	for i := 0; i < ms.Len(); i++ {
		w := ms.At(i)
		c.Messages = append(c.Messages, AsideMessage{
			Cause: AsideCause(w.Cause() + 1), Form: MessageForm(w.Form() + 1),
			Type: lacuna.Type(d.str(w.Type())), Text: d.str(w.Text()),
		})
	}
	return c
}

func (d *decoder) structs(ws wire.StructShape_List, err error) []StructShape {
	d.check(err)
	var out []StructShape
	for i := 0; i < ws.Len(); i++ {
		x := ws.At(i)
		s := StructShape{Path: d.path(x.Path()), Node: x.Node(), Open: x.Open()}
		ls, lerr := x.Labels()
		d.check(lerr)
		for j := 0; j < ls.Len(); j++ {
			s.Labels = append(s.Labels, d.str(ls.At(j)))
		}
		out = append(out, s)
	}
	return out
}

func (d *decoder) fate(x wire.Fate, err error) *Fate {
	d.check(err)
	f := &Fate{}
	rs, rerr := x.Reads()
	d.check(rerr)
	for i := 0; i < rs.Len(); i++ {
		f.Reads = append(f.Reads, d.path(rs.At(i).Steps()))
	}
	f.Readers = d.pairs(x.Readers())
	f.Copies = d.pairs(x.Copies())
	ns, nerr := x.Named()
	d.check(nerr)
	for i := 0; i < ns.Len(); i++ {
		y := ns.At(i)
		f.Named = append(f.Named, Named{Path: d.path(y.Path()), Lacuna: int(y.Lacuna()), Field: int(y.Field())})
	}
	return f
}

func (d *decoder) pairs(ws wire.PathPair_List, err error) []PathPair {
	d.check(err)
	var out []PathPair
	for i := 0; i < ws.Len(); i++ {
		w := ws.At(i)
		out = append(out, PathPair{Result: d.path(w.Result()), Input: d.path(w.Input())})
	}
	return out
}

func (d *decoder) synver(w wire.SynVer, err error) [2]uint {
	d.check(err)
	return [2]uint{uint(w.Major()), uint(w.Minor())}
}

func (d *decoder) node(w wire.Node) *Node {
	n := &Node{ID: w.Id(), Name: d.str(w.Name()), Kind: NodeKind(w.Kind() + 1)}
	if n.Kind == StructNode {
		n.Role = NodeRole(w.Role() + 1)
	}
	fs, err := w.Fields()
	d.check(err)
	for i := 0; i < fs.Len(); i++ {
		n.Fields = append(n.Fields, d.field(fs.At(i)))
	}
	es, err := w.Enumerants()
	d.check(err)
	for i := 0; i < es.Len(); i++ {
		x := es.At(i)
		n.Enumerants = append(n.Enumerants, Enumerant{Name: d.str(x.Name()), Value: d.str(x.Value()), Ordinal: x.Ordinal()})
	}
	return n
}

func (d *decoder) field(w wire.Field) *Field {
	f := &Field{Label: d.str(w.Label()), Name: d.str(w.Name()), Form: Form(w.Form()), Ordinal: w.Ordinal()}
	if w.HasType() {
		t, err := w.Type()
		d.check(err)
		f.Type = d.typ(t)
	}
	ms, err := w.Union()
	d.check(err)
	for i := 0; i < ms.Len(); i++ {
		f.Union = append(f.Union, d.member(ms.At(i)))
	}
	if w.HasDefault() {
		l, err := w.Default()
		d.check(err)
		lit := d.literal(l)
		f.Default = &lit
	}
	return f
}

func (d *decoder) member(w wire.Member) *Member {
	m := &Member{Name: d.str(w.Name()), Role: MemberRole(w.Role() + 1), Ordinal: w.Ordinal(), Kind: d.str(w.Kind()), TagLabel: d.str(w.TagLabel())}
	if w.HasType() {
		t, err := w.Type()
		d.check(err)
		m.Type = d.typ(t)
	}
	if w.HasTag() {
		l, err := w.Tag()
		d.check(err)
		lit := d.literal(l)
		m.Tag = &lit
	}
	return m
}

func (d *decoder) typ(w wire.Type) *Type {
	t := &Type{Kind: TypeKind(w.Kind() + 1), Node: w.Node()}
	if w.HasElem() {
		el, err := w.Elem()
		d.check(err)
		t.Elem = d.typ(el)
	}
	return t
}

func (d *decoder) literal(w wire.Literal) lensir.Literal {
	l := lensir.Literal{Kind: lensir.Kind(w.Kind() + 1)}
	switch l.Kind {
	case lensir.Bool:
		l.Bool = w.Bool()
	case lensir.Int:
		l.Int = d.str(w.Int())
	case lensir.Float:
		l.Float = d.str(w.Float())
	case lensir.String:
		l.Str = d.str(w.Str())
	case lensir.Bytes:
		b, err := w.Bytes()
		d.check(err)
		l.Str = string(b)
	}
	return l
}

func (d *decoder) edge(w wire.Edge) *Edge {
	ed := &Edge{From: d.synver(w.From()), To: d.synver(w.To()), Synthesized: w.Synthesized()}
	p := &lensir.Program{Edge: lensir.Edge{
		From: lacuna.SyntacticVersion(ed.From), To: lacuna.SyntacticVersion(ed.To),
	}}
	p.Result = d.nodes(w.Result())
	ls, err := w.Lacunas()
	d.check(err)
	for i := 0; i < ls.Len(); i++ {
		p.Lacunas = append(p.Lacunas, d.lacuna(ls.At(i)))
	}
	ed.Program = p
	cs, err := w.Cel()
	d.check(err)
	for i := 0; i < cs.Len(); i++ {
		x := cs.At(i)
		c := CEL{Role: CELRole(x.Role() + 1), Node: int(x.Node()), Lacuna: int(x.Lacuna()), Field: int(x.Field()), Source: d.str(x.Source())}
		vs, err := x.Vars()
		d.check(err)
		for j := 0; j < vs.Len(); j++ {
			y := vs.At(j)
			c.Vars = append(c.Vars, Var{Name: d.str(y.Name()), Path: d.path(y.Path()), Role: VarRole(y.Role() + 1), Type: d.str(y.Type())})
		}
		ed.CEL = append(ed.CEL, c)
	}
	if w.HasFate() {
		ed.Fate = d.fate(w.Fate())
	}
	us, uerr := w.Unions()
	d.check(uerr)
	for i := 0; i < us.Len(); i++ {
		x := us.At(i)
		u := UnionWrite{Node: int(x.Node())}
		ss, serr := x.Source()
		d.check(serr)
		for j := 0; j < ss.Len(); j++ {
			u.Source = append(u.Source, ss.At(j))
		}
		ts, terr := x.Target()
		d.check(terr)
		for j := 0; j < ts.Len(); j++ {
			u.Target = append(u.Target, ts.At(j))
		}
		ed.Unions = append(ed.Unions, u)
	}
	return ed
}

func (d *decoder) path(ss wire.Step_List, err error) lensir.Path {
	d.check(err)
	if ss.Len() == 0 {
		return nil
	}
	p := make(lensir.Path, ss.Len())
	for i := range p {
		w := ss.At(i)
		switch w.Which() {
		case wire.Step_Which_label:
			p[i] = lensir.Field(d.str(w.Label()))
		case wire.Step_Which_index:
			p[i] = lensir.Index(int(w.Index()))
		case wire.Step_Which_any:
			p[i] = lensir.Any()
		}
	}
	return p
}

func (d *decoder) nodes(ws wire.IRNode_List, err error) []lensir.Node {
	d.check(err)
	if ws.Len() == 0 {
		return nil
	}
	ns := make([]lensir.Node, ws.Len())
	for i := range ns {
		ns[i] = d.irnode(ws.At(i))
	}
	return ns
}

func (d *decoder) tests(ws wire.Test_List, err error) []lensir.Test {
	d.check(err)
	if ws.Len() == 0 {
		return nil
	}
	ts := make([]lensir.Test, ws.Len())
	for i := range ts {
		w := ws.At(i)
		t := lensir.Test{Kind: lensir.TestKind(w.Kind() + 1), Path: d.path(w.Path()), Neg: w.Neg(), Type: d.str(w.Type()), Safe: w.Safe()}
		if w.HasValue() {
			l, err := w.Value()
			d.check(err)
			t.Value = d.literal(l)
		}
		ts[i] = t
	}
	return ts
}

func (d *decoder) irnode(w wire.IRNode) lensir.Node {
	switch w.Which() {
	case wire.IRNode_Which_copy:
		g := w.Copy()
		c := &lensir.Copy{To: d.path(g.To()), From: d.path(g.From())}
		as, err := g.Alts()
		d.check(err)
		for i := 0; i < as.Len(); i++ {
			c.Alts = append(c.Alts, lensir.Alt{From: int(as.At(i).From()), To: int(as.At(i).To())})
		}
		return c
	case wire.IRNode_Which_constant:
		g := w.Constant()
		l, err := g.Value()
		d.check(err)
		return &lensir.Constant{To: d.path(g.To()), Value: d.literal(l)}
	case wire.IRNode_Which_wrap:
		g := w.Wrap()
		wr := &lensir.Wrap{To: d.path(g.To()), Kind: lensir.WrapKind(g.Kind() + 1), Len: int(g.Len()), Children: d.nodes(g.Children())}
		if a := g.Alt(); a >= 0 {
			alt := int(a)
			wr.Alt = &alt
		}
		return wr
	case wire.IRNode_Which_each:
		g := w.Each()
		return &lensir.Each{To: d.path(g.To()), Source: d.path(g.Source()), Body: d.nodes(g.Body())}
	case wire.IRNode_Which_branch:
		g := w.Branch()
		return &lensir.Branch{When: d.tests(g.When()), Then: d.nodes(g.Then())}
	case wire.IRNode_Which_head:
		g := w.Head()
		return &lensir.Head{To: d.path(g.To()), When: d.tests(g.When()), Then: d.nodes(g.Then()), Else: d.nodes(g.Else())}
	case wire.IRNode_Which_table:
		g := w.Table()
		t := &lensir.Table{To: d.path(g.To()), Key: d.path(g.Key())}
		es, err := g.Entries()
		d.check(err)
		for i := 0; i < es.Len(); i++ {
			x := es.At(i)
			t.Entries = append(t.Entries, lensir.Entry{Key: d.str(x.Key()), Value: d.nodes(x.Value())})
		}
		return t
	case wire.IRNode_Which_computed:
		g := w.Computed()
		x, err := g.Expr()
		d.check(err)
		return &lensir.Computed{To: d.path(g.To()), Expr: d.expr(x)}
	}
	d.check(fmt.Errorf("artifact has a lens IR node of unknown kind %d", w.Which()))
	return nil
}

func (d *decoder) exprOf(w wire.Expr, err error) lensir.Expr {
	d.check(err)
	return d.expr(w)
}

func (d *decoder) expr(w wire.Expr) lensir.Expr {
	switch w.Which() {
	case wire.Expr_Which_literal:
		l, err := w.Literal()
		d.check(err)
		return d.literal(l)
	case wire.Expr_Which_ref:
		return &lensir.Ref{Path: d.path(w.Ref())}
	case wire.Expr_Which_resultRef:
		return &lensir.ResultRef{Path: d.path(w.ResultRef())}
	case wire.Expr_Which_unary:
		g := w.Unary()
		return &lensir.Unary{Op: d.str(g.Op()), X: d.exprOf(g.X())}
	case wire.Expr_Which_binary:
		g := w.Binary()
		return &lensir.Binary{Op: d.str(g.Op()), X: d.exprOf(g.X()), Y: d.exprOf(g.Y())}
	case wire.Expr_Which_interp:
		ps, err := w.Interp()
		d.check(err)
		in := &lensir.Interp{}
		for i := 0; i < ps.Len(); i++ {
			in.Parts = append(in.Parts, d.expr(ps.At(i)))
		}
		return in
	case wire.Expr_Which_call:
		g := w.Call()
		c := &lensir.Call{Fn: d.str(g.Fn())}
		as, err := g.Args()
		d.check(err)
		for i := 0; i < as.Len(); i++ {
			c.Args = append(c.Args, d.expr(as.At(i)))
		}
		return c
	case wire.Expr_Which_match:
		return &lensir.Match{Tests: d.tests(w.Match())}
	case wire.Expr_Which_cond:
		g := w.Cond()
		return &lensir.Cond{When: d.tests(g.When()), Then: d.exprOf(g.Then()), Else: d.exprOf(g.Else())}
	}
	d.check(fmt.Errorf("artifact has a lens IR expression of unknown kind %d", w.Which()))
	return nil
}

func (d *decoder) lacuna(w wire.Lacuna) lensir.Lacuna {
	l := lensir.Lacuna{Type: lacuna.Type(d.str(w.Type()))}
	if w.HasWhen() {
		l.When = d.exprOf(w.When())
	}
	l.SourceFields = d.fieldRefs(w.SourceFields())
	l.TargetFields = d.fieldRefs(w.TargetFields())
	if w.HasMsg() {
		l.Message = d.exprOf(w.Msg())
	}
	return l
}

func (d *decoder) fieldRefs(ws wire.FieldRef_List, err error) []lensir.FieldRef {
	d.check(err)
	if ws.Len() == 0 {
		return nil
	}
	frs := make([]lensir.FieldRef, ws.Len())
	for i := range frs {
		w := ws.At(i)
		fr := lensir.FieldRef{Path: d.str(w.Path()), When: d.tests(w.When())}
		if w.HasValue() {
			fr.Value = d.exprOf(w.Value())
		}
		frs[i] = fr
	}
	return frs
}
