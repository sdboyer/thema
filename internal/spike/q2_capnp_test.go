package spike

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"capnproto.org/go/capnp/v3"
	"capnproto.org/go/capnp/v3/std/capnp/schema"
)

// A field as the engine would see it: everything from the CodeGeneratorRequest.
type field struct {
	name     string
	typ      schema.Type_Which
	off      uint32 // in units of the type's size (data) or pointer index
	disc     uint16 // Field_noDiscriminant if not a union member
	def      schema.Value
	explicit bool
	group    uint64 // group type id if a group
}

type layout struct {
	name        string
	id          uint64
	dataWords   uint16
	ptrs        uint16
	discCount   uint16
	discOff     uint32 // in 16-bit units
	fields      []field
	size        capnp.ObjectSize
	groupParent *layout
}

func loadLayouts(t *testing.T) map[string]*layout {
	t.Helper()
	b, err := os.ReadFile("schema/forms.cgr")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := capnp.Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	req, err := schema.ReadRootCodeGeneratorRequest(msg)
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := req.Nodes()
	byID := map[uint64]*layout{}
	byName := map[string]*layout{}
	for i := 0; i < nodes.Len(); i++ {
		n := nodes.At(i)
		if n.Which() != schema.Node_Which_structNode {
			continue
		}
		sn := n.StructNode()
		dn, _ := n.DisplayName()
		l := &layout{name: dn[n.DisplayNamePrefixLength():], id: n.Id(), dataWords: sn.DataWordCount(), ptrs: sn.PointerCount(),
			discCount: sn.DiscriminantCount(), discOff: sn.DiscriminantOffset(),
			size: capnp.ObjectSize{DataSize: capnp.Size(sn.DataWordCount()) * 8, PointerCount: sn.PointerCount()}}
		fs, _ := sn.Fields()
		for j := 0; j < fs.Len(); j++ {
			f := fs.At(j)
			name, _ := f.Name()
			fl := field{name: name, disc: f.DiscriminantValue()}
			switch f.Which() {
			case schema.Field_Which_slot:
				ty, _ := f.Slot().Type()
				fl.typ = ty.Which()
				fl.off = f.Slot().Offset()
				fl.def, _ = f.Slot().DefaultValue()
				fl.explicit = f.Slot().HadExplicitDefault()
			case schema.Field_Which_group:
				fl.group = f.Group().TypeId()
			}
			l.fields = append(l.fields, fl)
		}
		byID[n.Id()] = l
		byName[l.name] = l
	}
	// Groups: name them parent.field and record their parent (a group shares
	// the parent's storage).
	for _, l := range byName {
		for _, f := range l.fields {
			if f.group != 0 {
				g := byID[f.group]
				g.groupParent = l
				byName[l.name+"."+f.name] = g
			}
		}
	}
	return byName
}

func (l *layout) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s id=%#x dataWords=%d ptrs=%d disc(count=%d, off16=%d)", l.name, l.id, l.dataWords, l.ptrs, l.discCount, l.discOff)
	for _, f := range l.fields {
		if f.group != 0 {
			fmt.Fprintf(&sb, "\n    %s: group %#x", f.name, f.group)
			continue
		}
		d := ""
		if f.explicit {
			switch f.def.Which() {
			case schema.Value_Which_int64:
				d = fmt.Sprintf(" default=%d", f.def.Int64())
			case schema.Value_Which_int32:
				d = fmt.Sprintf(" default=%d", f.def.Int32())
			case schema.Value_Which_text:
				s, _ := f.def.Text()
				d = fmt.Sprintf(" default=%q", s)
			}
		}
		disc := ""
		if f.disc != schema.Field_noDiscriminant {
			disc = fmt.Sprintf(" disc=%d", f.disc)
		}
		fmt.Fprintf(&sb, "\n    %s: %v @off %d%s%s", f.name, f.typ, f.off, disc, d)
	}
	return sb.String()
}

// --- raw reading with defaults applied from the schema, as generated code does ---

func (l *layout) field(name string) field {
	for _, f := range l.fields {
		if f.name == name {
			return f
		}
	}
	panic("no field " + name)
}

// readInt reads an integer field through l, XORing the schema default, exactly
// as generated accessors do. For the data-section size, go-capnp returns 0 for
// reads past the struct's actual data size.
func (l *layout) readInt(s capnp.Struct, name string) int64 {
	f := l.field(name)
	switch f.typ {
	case schema.Type_Which_int32:
		return int64(int32(s.Uint32(capnp.DataOffset(f.off*4)) ^ uint32(f.def.Int32())))
	case schema.Type_Which_int64:
		return int64(s.Uint64(capnp.DataOffset(f.off*8)) ^ uint64(f.def.Int64()))
	}
	panic("not int")
}

func (l *layout) writeInt(s capnp.Struct, name string, v int64) {
	f := l.field(name)
	switch f.typ {
	case schema.Type_Which_int32:
		s.SetUint32(capnp.DataOffset(f.off*4), uint32(int32(v))^uint32(f.def.Int32()))
	case schema.Type_Which_int64:
		s.SetUint64(capnp.DataOffset(f.off*8), uint64(v)^uint64(f.def.Int64()))
	}
}

func (l *layout) readText(s capnp.Struct, name string) string {
	f := l.field(name)
	p, _ := s.Ptr(uint16(f.off))
	if !p.IsValid() {
		d, _ := f.def.Text()
		return d + " (default)"
	}
	return p.Text()
}

func (l *layout) which(s capnp.Struct) string {
	d := s.Uint16(capnp.DataOffset(l.discOff * 2))
	for _, f := range l.fields {
		if f.disc == d {
			return f.name
		}
	}
	return fmt.Sprintf("<unknown disc %d>", d)
}

func TestQ2Q5Capnp(t *testing.T) {
	var rep strings.Builder
	L := loadLayouts(t)
	fmt.Fprintln(&rep, "== Q2: layouts from capnp compile -o- ==")
	for _, n := range []string{"Old", "AppendOptional", "AppendOptional.opt", "AppendDefaulted", "RequiredToOptional", "RequiredToOptional.a", "RequiredToOptionalUnsetFirst", "RequiredToOptionalUnsetFirst.a", "RequiredToDefaultedData", "RequiredToDefaultedPtr", "Widened", "Outer0", "Outer1"} {
		fmt.Fprintf(&rep, "  %s\n", L[n])
	}

	// Encode an Old{a: 3, t: "x"} and Old{a: -1, t: "x"} with raw accessors.
	encodeOld := func(a int64) ([]byte, capnp.Struct) {
		msg, seg, _ := capnp.NewMessage(capnp.SingleSegment(nil))
		s, _ := capnp.NewRootStruct(seg, L["Old"].size)
		L["Old"].writeInt(s, "a", a)
		s.SetNewText(uint16(L["Old"].field("t").off), "x")
		b, _ := msg.Marshal()
		return b, s
	}
	decode := func(b []byte) capnp.Struct {
		msg, err := capnp.Unmarshal(b)
		if err != nil {
			t.Fatal(err)
		}
		p, _ := msg.Root()
		return p.Struct()
	}

	fmt.Fprintln(&rep, "\n== Q5: old-minor bytes read through the newer struct (CUE expectation in brackets) ==")
	for _, a := range []int64{3, -1} {
		b, _ := encodeOld(a)
		s := decode(b)
		fmt.Fprintf(&rep, "Old{a: %d, t: \"x\"} is %d bytes; struct size seen on the wire: %v\n", a, len(b), s.Size())
		l := L["AppendOptional"]
		fmt.Fprintf(&rep, "  (a) AppendOptional:   a=%d t=%s opt.which=%s   [CUE: opt absent]\n", l.readInt(s, "a"), l.readText(s, "t"), L["AppendOptional.opt"].which(s))
		l = L["AppendDefaulted"]
		fmt.Fprintf(&rep, "  (b)(c) AppendDefaulted: a=%d t=%s d=%d s=%s   [CUE: d=7, s=\"foo\"]\n", l.readInt(s, "a"), l.readText(s, "t"), l.readInt(s, "d"), l.readText(s, "s"))
		l = L["RequiredToOptional"]
		fmt.Fprintf(&rep, "  (d) RequiredToOptional (value first): a.which=%s a.value=%d t=%s   [CUE: a=%d present]\n", L["RequiredToOptional.a"].which(s), L["RequiredToOptional.a"].readInt(s, "value"), l.readText(s, "t"), a)
		fmt.Fprintf(&rep, "  (d') RequiredToOptionalUnsetFirst: a.which=%s   [CUE: a=%d present]  <- #82's unset-first shape misreads old data\n", L["RequiredToOptionalUnsetFirst.a"].which(s), a)
		l = L["RequiredToDefaultedData"]
		fmt.Fprintf(&rep, "  (e) RequiredToDefaultedData: a=%d   [CUE: a=%d]  <- XOR default corrupts existing data\n", l.readInt(s, "a"), a)
		l = L["RequiredToDefaultedPtr"]
		fmt.Fprintf(&rep, "  (f) RequiredToDefaultedPtr: t=%s   [CUE: t=\"x\"]\n", l.readText(s, "t"))
		l = L["Widened"]
		fmt.Fprintf(&rep, "  (g) Widened int32->int64: a=%d   [CUE: a=%d]\n", l.readInt(s, "a"), a)
	}
	// A freshly written AppendDefaulted with d unset: does the default come back?
	{
		msg, seg, _ := capnp.NewMessage(capnp.SingleSegment(nil))
		s, _ := capnp.NewRootStruct(seg, L["AppendDefaulted"].size)
		l := L["AppendDefaulted"]
		fmt.Fprintf(&rep, "  fresh AppendDefaulted, nothing set: d=%d s=%s; after writeInt(d, 0): ", l.readInt(s, "d"), l.readText(s, "s"))
		l.writeInt(s, "d", 0)
		fmt.Fprintf(&rep, "d=%d (raw word=%#x)\n", l.readInt(s, "d"), s.Uint64(8))
		_ = msg
	}

	fmt.Fprintln(&rep, "\n== Q2: deep copy between layouts ==")
	{
		b, _ := encodeOld(3)
		src := decode(b)
		// identical layout
		msg, seg, _ := capnp.NewMessage(capnp.SingleSegment(nil))
		dst, _ := capnp.NewRootStruct(seg, L["Old"].size)
		err := dst.CopyFrom(src)
		fmt.Fprintf(&rep, "  CopyFrom same layout: err=%v a=%d t=%s\n", err, L["Old"].readInt(dst, "a"), L["Old"].readText(dst, "t"))
		// bigger layout: Old -> AppendDefaulted
		msg2, seg2, _ := capnp.NewMessage(capnp.SingleSegment(nil))
		dst2, _ := capnp.NewRootStruct(seg2, L["AppendDefaulted"].size)
		err = dst2.CopyFrom(src)
		fmt.Fprintf(&rep, "  CopyFrom Old -> AppendDefaulted (bigger): err=%v size=%v a=%d t=%s d=%d s=%s\n", err, dst2.Size(), L["AppendDefaulted"].readInt(dst2, "a"), L["AppendDefaulted"].readText(dst2, "t"), L["AppendDefaulted"].readInt(dst2, "d"), L["AppendDefaulted"].readText(dst2, "s"))
		// smaller layout: AppendDefaulted{a:3,t:x,d:9,s:bar} -> Old
		msg3, seg3, _ := capnp.NewMessage(capnp.SingleSegment(nil))
		big, _ := capnp.NewRootStruct(seg3, L["AppendDefaulted"].size)
		L["AppendDefaulted"].writeInt(big, "a", 3)
		L["AppendDefaulted"].writeInt(big, "d", 9)
		big.SetNewText(uint16(L["AppendDefaulted"].field("t").off), "x")
		big.SetNewText(uint16(L["AppendDefaulted"].field("s").off), "bar")
		msg4, seg4, _ := capnp.NewMessage(capnp.SingleSegment(nil))
		small, _ := capnp.NewRootStruct(seg4, L["Old"].size)
		err = small.CopyFrom(big)
		b4, _ := msg4.Marshal()
		fmt.Fprintf(&rep, "  CopyFrom AppendDefaulted -> Old (smaller): err=%v size=%v bytes=%d a=%d t=%s (extra fields truncated? raw d word=%#x)\n", err, small.Size(), len(b4), L["Old"].readInt(small, "a"), L["Old"].readText(small, "t"), small.Uint64(8))
		// pointer-level copy: set a nested struct pointer to a struct of another layout
		msg5, seg5, _ := capnp.NewMessage(capnp.SingleSegment(nil))
		outer, _ := capnp.NewRootStruct(seg5, L["Outer1"].size)
		err = outer.SetPtr(0, src.ToPtr()) // Old into an Outer1.inner (:AppendDefaulted)
		inner, _ := outer.Ptr(0)
		fmt.Fprintf(&rep, "  SetPtr(Old struct) into Outer1.inner: err=%v inner.size=%v d=%d s=%s (reader applies defaults; a 'copy' is a pointer copy, layout travels with the data)\n", err, inner.Struct().Size(), L["AppendDefaulted"].readInt(inner.Struct(), "d"), L["AppendDefaulted"].readText(inner.Struct(), "s"))
		_, _, _ = msg, msg2, msg3
		_ = msg5
	}
	os.WriteFile("q2q5_report.txt", []byte(rep.String()), 0o644)
	t.Log("wrote q2q5_report.txt")
}
