// Package compiled is the Go model of a lineage compiled to Cap'n Proto: the
// struct and enum nodes the compiler (encoding/capnp) emits, how each maps
// back to the lineage's labels and values, the versions and which struct each
// is, the .capnp text and the layout the capnp tool computed for it.
//
// This package does not depend on CUE, and must not come to: it is the form
// an engine that never links CUE loads (#90), and the artifact serializes. A
// test enforces it. Everything here is output of the compiler, never
// consulted to decide what a lineage means.
package compiled

import "github.com/sdboyer/thema/internal/lensir"

// Compiled is a lineage compiled to Cap'n Proto.
type Compiled struct {
	// Namespace is the hash namespace the type IDs were derived under
	// (encoding/capnp.Namespace); the lineage's name alone by default.
	Namespace string
	// Lineage is the lineage's name.
	Lineage string
	// Schema is the .capnp text declaring every node, deterministic for a
	// given lineage and namespace.
	Schema string
	// Layout is the CodeGeneratorRequest message capnp compile -o- produced
	// for Schema, carrying the authoritative layout of every struct. It is
	// nil where the compiler ran without the capnp tool.
	Layout []byte
	// Versions are the lineage's schema versions, in order, each with the
	// struct it is read through.
	Versions []Version
	// Nodes are every struct and enum node, root structs in version order and
	// nested nodes depth-first in declaration order beneath their parent.
	Nodes []*Node
}

// Node returns the node with the given ID, or nil.
func (c *Compiled) Node(id uint64) *Node {
	for _, n := range c.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

// A Version is one schema version of the lineage.
type Version struct {
	Major, Minor uint
	// ID is the version's content-hash identity (internal/contenthash):
	// <schema digest>:<lens digest>.
	ID string
	// Root is the ID of the struct node instances of this version are read
	// through. Successive minors share a root while they are capnp-append-
	// compatible.
	Root uint64
}

// NodeKind is what a Node declares.
type NodeKind int

// The kinds of Node.
const (
	// StructNode is a struct: a schema struct, a map entry or a union box.
	StructNode NodeKind = iota + 1
	// EnumNode is an enum, standing for a set of string constants.
	EnumNode
)

// NodeRole says what a struct node stands for in the lineage.
type NodeRole int

// The roles of a struct node.
const (
	// SchemaStruct is a struct the schema declares; it carries extras, the
	// side-channel for fields the schema does not declare.
	SchemaStruct NodeRole = iota + 1
	// MapEntry is an entry of a [string]: T field: key @0 :Text, value @1 :T.
	MapEntry
	// UnionBox holds a disjunction where capnp has no union: a list element or
	// a map value. Its only field is an anonymous union.
	UnionBox
)

// A Node is one struct or enum node of the schema.
type Node struct {
	ID uint64
	// Name is the node's display name: the root struct's name, then each
	// nesting level joined with ".", as "ShipV0.Inner".
	Name string
	Kind NodeKind
	Role NodeRole
	// Fields are a struct's fields in order of first appearance in the
	// lineage's history, which is also the order of their first ordinal.
	Fields []*Field
	// Enumerants are an enum's values in ordinal order.
	Enumerants []Enumerant
}

// Field finds a struct field by its CUE label.
func (n *Node) Field(label string) *Field {
	for _, f := range n.Fields {
		if f.Label == label {
			return f
		}
	}
	return nil
}

// An Enumerant is one value of an enum: the string the lineage declares and
// the name capnp knows it by.
type Enumerant struct {
	Name    string
	Value   string
	Ordinal uint16
}

// Form is a field's form in the schema (docs/invariants.md, Field forms).
type Form int

// The forms.
const (
	Required Form = iota
	Optional
	Defaulted
)

func (f Form) String() string {
	return [...]string{"required", "optional", "defaulted"}[f]
}

// A Field is one field of a struct node.
type Field struct {
	// Label is the schema's label; "" for a field the compiler adds (extras,
	// a map entry's key and value, a union box's union).
	Label string
	// Name is the field's capnp name, camelCase as capnp requires.
	Name string
	Form Form
	// Type is the field's type where the field is plain; nil where it is a
	// union.
	Type *Type
	// Ordinal is the plain field's ordinal.
	Ordinal uint16
	// Union holds the field's union members where the field is optional, a
	// discriminated disjunction, or both: unset, the value, or one member per
	// alternative. nil for a plain field.
	Union []*Member
	// Default is the schema's default where the field is Defaulted and the
	// default is a scalar. For a plain Bool, integer, Text, Data or enum
	// field it is declared in the schema and applied by readers; a union
	// field carries it here only, for a translation to materialize.
	Default *lensir.Literal
}

// MemberRole is why a union member exists.
type MemberRole int

// The roles of a union member.
const (
	// Unset marks an optional field absent. It is Void.
	Unset MemberRole = iota + 1
	// Value is the one value of an optional field whose value is not itself
	// a disjunction.
	Value
	// KindMember holds the alternative of a given kind of a disjunction with
	// pairwise distinct kinds.
	KindMember
	// TagMember holds one struct alternative of a disjunction discriminated by
	// a tag label whose constant differs per alternative.
	TagMember
)

// A Member is one member of a field's union.
type Member struct {
	Name    string
	Role    MemberRole
	Ordinal uint16
	// Type is the member's type; nil for a Void member (Unset, or the null
	// alternative).
	Type *Type
	// Kind names the alternative's kind for a KindMember: null, bool, int,
	// string, bytes, struct or list.
	Kind string
	// TagLabel and Tag are the discriminating label and this alternative's
	// constant for a TagMember.
	TagLabel string
	Tag      *lensir.Literal
}

// TypeKind is a capnp type.
type TypeKind int

// The capnp types a value maps to.
const (
	Void TypeKind = iota + 1
	Bool
	Int8
	Int16
	Int32
	Int64
	UInt8
	UInt16
	UInt32
	UInt64
	Text
	Data
	List
	Enum
	Struct
)

var typeKindNames = [...]string{"", "Void", "Bool", "Int8", "Int16", "Int32", "Int64", "UInt8", "UInt16", "UInt32", "UInt64", "Text", "Data", "List", "Enum", "Struct"}

// String returns the capnp spelling of the kind; List, Enum and Struct are
// named for the kind, not a type.
func (k TypeKind) String() string {
	if int(k) < len(typeKindNames) {
		return typeKindNames[k]
	}
	return "TypeKind(?)"
}

// Pointer reports whether values of the kind live in a struct's pointer
// section rather than its data section. A default on a pointer field is
// substituted for a null pointer; one on a data field is XORed into the
// data, which is why a required data field cannot become defaulted within
// a struct generation.
func (k TypeKind) Pointer() bool {
	switch k {
	case Text, Data, List, Struct:
		return true
	}
	return false
}

// A Type is a capnp type: a scalar, a List of an element type, or a
// reference to an Enum or Struct node.
type Type struct {
	Kind TypeKind
	Elem *Type
	Node uint64
}
