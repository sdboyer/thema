// Package lensir defines the lens IR: a target-neutral program for each lens
// of a lineage, saying what the lens writes into its result and from what,
// which backends such as encoding/capnp render without consulting the lens's
// CUE.
//
// A program is built by the totality check (internal/lenscheck) as a
// by-product of proving its lens total: each node is emitted at the place in
// the walk that proved it, so no node exists without its proof, and a lens the
// check refuses has no program. The nodes are the forms the check proves:
//
//   - [Copy]: a result path takes an input path;
//   - [Constant]: a result path takes a literal;
//   - [Wrap]: a struct or list literal, whose fields or elements are nodes;
//   - [Each]: a list with an element for each element of a source list, whose
//     body is nodes over the open element ([for item in list {body}]);
//   - [Branch]: nodes written under a condition (an if comprehension);
//   - [Head]: one of two values, chosen by a condition ([if c {x}, y][0]);
//   - [Table]: a value looked up by an input field among proven entries;
//   - [Computed]: an expression over the proven operations, as a tree for a
//     backend to render;
//   - [Lacuna]: a lacuna the lens declares, with its condition, field
//     references, message and type.
//
// Conditions are the patterns the check proves ([Test]): a presence test, an
// equality with a literal or a type test on an input path, negated or not,
// in conjunction. Paths are relative to the lens's input ([Ref], Copy.From,
// Test.Path) or to its result (every To, [ResultRef]).
//
// This package does not depend on CUE, and must not come to: a test enforces
// it. Every string it carries is a value (a label, a literal, a function
// name), except [Test.Type], the constraint of a type test as the lens wrote
// it, which a backend maps to its own notion of type.
//
// A [Program] is constructed only by lenscheck.Check (the lensir-programs
// seam); [Print] renders one as text, which the goldens record.
package lensir

import (
	"sort"
	"strconv"

	"github.com/sdboyer/thema/lacuna"
)

// Edge names a lens by the versions it maps between, as the lens is keyed in
// a lineage's lenses: "from->to".
type Edge struct {
	From, To lacuna.SyntacticVersion
}

// String formats the edge as the lens's key in a lineage's lenses.
func (e Edge) String() string {
	return e.From.String() + "->" + e.To.String()
}

// less orders edges by their source version, then their target.
func (e Edge) less(o Edge) bool {
	for i := range e.From {
		if e.From[i] != o.From[i] {
			return e.From[i] < o.From[i]
		}
	}
	for i := range e.To {
		if e.To[i] != o.To[i] {
			return e.To[i] < o.To[i]
		}
	}
	return false
}

// Programs holds the lens programs of a lineage, one per lens, by edge.
type Programs map[Edge]*Program

// Sorted returns the programs ordered by edge.
func (ps Programs) Sorted() []*Program {
	out := make([]*Program, 0, len(ps))
	for _, p := range ps {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Edge.less(out[j].Edge) })
	return out
}

// A Program is the lens of one edge: what it writes into its result, and the
// lacunas it declares.
type Program struct {
	Edge Edge
	// Result holds the nodes writing the result, in the order the lens
	// declares them. A node's To is its full path within the result; the
	// nesting (Wrap, Branch) is the lens's own.
	Result []Node
	// Lacunas are the lacunas the lens declares, in declaration order.
	Lacunas []Lacuna
}

// A Path is a path within an instance: field labels, list indices and, inside
// an Each, the open element of a list. The empty path is the instance itself.
type Path []Step

// A Step is one step of a Path: a field, by label, a list element, by index,
// or the open element of a list (every element, inside an Each).
type Step struct {
	Label   string
	Index   int
	IsIndex bool
	IsAny   bool
}

// Field returns the step selecting the field label.
func Field(label string) Step { return Step{Label: label} }

// Index returns the step selecting the list element i.
func Index(i int) Step { return Step{Index: i, IsIndex: true} }

// Any returns the step selecting the open element of a list.
func Any() Step { return Step{IsAny: true} }

// String formats the path as CUE would: a.b[0], the open element [_], with a
// label that is not an identifier quoted.
func (p Path) String() string {
	s := ""
	for _, st := range p {
		switch {
		case st.IsAny:
			s += "[_]"
		case st.IsIndex:
			s += "[" + strconv.Itoa(st.Index) + "]"
		case isIdent(st.Label):
			if s != "" {
				s += "."
			}
			s += st.Label
		default:
			if s != "" {
				s += "."
			}
			s += strconv.Quote(st.Label)
		}
	}
	return s
}

// isIdent reports whether s is a plain identifier: letters, digits and
// underscores, not starting with a digit. A label starting with _ is not: in
// CUE, _id names a hidden field, and the data field "_id" must be quoted.
func isIdent(s string) bool {
	if s == "" || s[0] == '_' {
		return false
	}
	for i, r := range s {
		isLetter := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		if ok := isLetter || (isDigit && i > 0); !ok {
			return false
		}
	}
	return true
}

// A Node is one thing a lens writes. Every node but Branch names the result
// path it writes.
type Node interface {
	node()
}

// Copy writes the input at From to the result at To. An empty From is the
// whole input.
//
// Alts is set where the target at To is a disjunction with a struct or list
// alternative, which a backend lowers to a tagged union: one entry per
// alternative of the source at From that the copy may carry, once the
// patterns it is under have refined them, pairing that source alternative
// with the target alternative the proof showed accepts it. Which alternative
// the input holds, the backend tests itself; the patterns above the copy may
// already fix it. A disjunction of scalars alone needs no Alts: its values
// carry their own identity.
type Copy struct {
	To, From Path
	Alts     []Alt
}

// An Alt pairs an alternative of a disjunction in the source with the one in
// the target that accepts it. Alternatives are numbered as the schemas
// declare them at the path, in order, nested disjunctions flattened, defaults
// not counted apart.
type Alt struct {
	From, To int
}

// Constant writes a literal to the result at To.
type Constant struct {
	To    Path
	Value Literal
}

// Wrap writes a struct or list literal to the result at To. Its fields or
// elements are Children, each writing a path beneath To; a list has Len
// elements.
//
// Alt is set where the target at To is a disjunction: the alternative the
// literal is an instance of, numbered as the target schema declares them at
// To (see [Alt]), whose fields alone the proof checked Children against. A
// Wrap whose children are only branches, each wrapping its own alternative,
// has no Alt: it is the lens's own nesting around the arms.
type Wrap struct {
	To       Path
	Kind     WrapKind
	Len      int
	Alt      *int
	Children []Node
}

// WrapKind is what a Wrap writes.
type WrapKind int

// The kinds of Wrap.
const (
	StructWrap WrapKind = iota + 1
	ListWrap
)

// Each writes to To a list with one element for each element of the input
// list at Source: a lens's [for item in list {body}]. Body writes the open
// element, To[_], from the source's open element, Source[_]; the proof has
// shown the source is a list whose length the target accepts.
//
// An Each in the Body of another maps a list within the other's element: its
// Source extends the enclosing Source[_], and its To the enclosing To[_]. Each
// open element in a path is then the element of the Each whose Source (for an
// input path) or To (for a result path) the path extends at that step: under
// an Each over s, s[_].f[_].n reads the current element of the inner list f,
// within the current element of s. A lens nesting an Each over any other list
// has no program.
type Each struct {
	To     Path
	Source Path
	Body   []Node
}

// Branch writes Then where When holds. The lens writes the same path in
// another branch, or unconditionally, where When does not hold; the proof
// has shown the branches together cover every instance and agree where they
// overlap.
type Branch struct {
	When []Test
	Then []Node
}

// Head writes Then to To where When holds and Else otherwise: a lens's
// [if c {x}, y][0]. Both arms write To.
type Head struct {
	To   Path
	When []Test
	Then []Node
	Else []Node
}

// Table writes to To the entry of a lookup table selected by the input at
// Key, a string field. Entries holds an entry for every value the field may
// take; each entry's nodes write To.
type Table struct {
	To      Path
	Key     Path
	Entries []Entry
}

// An Entry is one proven entry of a Table.
type Entry struct {
	Key   string
	Value []Node
}

// Computed writes the value of Expr to To.
type Computed struct {
	To   Path
	Expr Expr
}

func (*Copy) node()     {}
func (*Constant) node() {}
func (*Wrap) node()     {}
func (*Each) node()     {}
func (*Branch) node()   {}
func (*Head) node()     {}
func (*Table) node()    {}
func (*Computed) node() {}

// A Test is one pattern a lens tests the input with, as the proof classifies
// it.
type Test struct {
	Kind TestKind
	// Path is the input path tested.
	Path Path
	// Neg negates the test: p == _|_, p != c, (p & T) == _|_.
	Neg bool
	// Value is the literal an Equality compares with.
	Value Literal
	// Type is the constraint a TypeTest unifies with, as the lens wrote it
	// (string, int32, "a" | "b").
	Type string
	// Safe marks an Equality written (p & c) != _|_, which is false rather
	// than an error where p is absent.
	Safe bool
}

// TestKind is the form of a Test.
type TestKind int

// The forms of Test.
const (
	// Presence is p != _|_.
	Presence TestKind = iota + 1
	// Equality is p == c for a literal c.
	Equality
	// TypeTest is (p & T) != _|_ for a type or constraint T.
	TypeTest
)

// A Literal is a scalar value.
type Literal struct {
	Kind Kind
	Bool bool
	// Int is the decimal digits of an Int.
	Int string
	// Float is a Float as CUE prints it.
	Float string
	// Str is the text of a String, or the bytes of a Bytes.
	Str string
}

// Kind is the kind of a Literal.
type Kind int

// The kinds of Literal.
const (
	Null Kind = iota + 1
	Bool
	Int
	Float
	String
	Bytes
)

// An Expr is an expression a Computed node evaluates, or a lacuna reports.
type Expr interface {
	expr()
}

// Ref reads the input at Path.
type Ref struct {
	Path Path
}

// ResultRef reads the result at Path, which another node writes.
type ResultRef struct {
	Path Path
}

// Unary applies Op (- or !) to X.
type Unary struct {
	Op string
	X  Expr
}

// Binary applies Op (+ - * == != < > <= >= && ||) to X and Y.
type Binary struct {
	Op   string
	X, Y Expr
}

// Interp builds a string from Parts: string Literals and expressions, in
// order.
type Interp struct {
	Parts []Expr
}

// Call calls the builtin Fn (len, quo, rem, div, mod, or a function of the
// strings package as strings.Name) on Args.
type Call struct {
	Fn   string
	Args []Expr
}

// Match is a conjunction of tests as a boolean value.
type Match struct {
	Tests []Test
}

// Cond is Then where When holds and Else otherwise: a lens's
// [if c {x}, y][0] in expression position (a lacuna's condition over
// optional fields: [if p != _|_ {p > 5}, false][0]).
type Cond struct {
	When []Test
	Then Expr
	Else Expr
}

func (*Ref) expr()       {}
func (*ResultRef) expr() {}
func (*Unary) expr()     {}
func (*Binary) expr()    {}
func (*Interp) expr()    {}
func (*Call) expr()      {}
func (*Match) expr()     {}
func (*Cond) expr()      {}
func (Literal) expr()    {}

// A Lacuna is one the lens declares, and the translation reports where When
// holds.
type Lacuna struct {
	// When is the condition: nil where the lacuna always applies, otherwise a
	// Match of the patterns tested, a guard expression, or a Bool literal.
	When         Expr
	SourceFields []FieldRef
	TargetFields []FieldRef
	// Message is the message reported: a string literal, or an expression
	// building one.
	Message Expr
	Type    lacuna.Type
}

// A FieldRef is a field a lacuna reports: its path, as the lens declares it,
// and the value reported for it. When is the patterns under which the entry
// is reported at all (an entry written [if p != _|_ {...}]); nil is always.
type FieldRef struct {
	Path  string
	When  []Test
	Value Expr
}
