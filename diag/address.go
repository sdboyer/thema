package diag

import (
	"fmt"
	"strings"
)

// AddrKind says what construct an Address names.
type AddrKind string

const (
	AddrLineage         AddrKind = "lineage"
	AddrName            AddrKind = "name"
	AddrJoinSchema      AddrKind = "joinSchema"
	AddrSchema          AddrKind = "schema"          // Version
	AddrSchemaField     AddrKind = "schemaField"     // Version, Field
	AddrExample         AddrKind = "example"         // Version, Name
	AddrLens            AddrKind = "lens"            // From, To
	AddrLensInput       AddrKind = "lensInput"       // From, To
	AddrLensResult      AddrKind = "lensResult"      // From, To
	AddrLensResultField AddrKind = "lensResultField" // From, To, Field
	AddrLacuna          AddrKind = "lacuna"          // From, To, Index (or Field for keyed lacunas)
	AddrLacunaField     AddrKind = "lacunaField"     // From, To, Index, Name
	AddrStep            AddrKind = "step"            // From, To, Index: one step of a translation
	AddrData            AddrKind = "data"            // Field
)

// Version is a syntactic version as an Address carries it. It is a pair rather
// than thema.SyntacticVersion because diag must not import the thema root.
type Version [2]uint

func (v Version) String() string { return fmt.Sprintf("%d.%d", v[0], v[1]) }

// Address is the structured subject of a Diagnostic, in terms independent of
// how the lineage was written. A Vocabulary renders it into the author's.
type Address struct {
	Kind    AddrKind `json:"kind"`
	Version Version  `json:"version,omitempty"`
	From    Version  `json:"from,omitempty"`
	To      Version  `json:"to,omitempty"`
	// Index is a lacuna's or a translation step's index.
	Index int `json:"index,omitempty"`
	// Field is a path inside a schema, a lens result or the data.
	Field []string `json:"field,omitempty"`
	// Name is an example's name, a lacuna field's name, or an unknown field.
	Name string `json:"name,omitempty"`
}

// Vocabulary renders an Address in the terms of one authoring form.
type Vocabulary interface {
	// Path locates the Address in the author's input, as a path a tool can
	// show or follow.
	Path(a Address) []string
	// Term names the Address in a sentence: "lens 0.0->1.0", "schema 1.0".
	Term(a Address) string
}

// NormalForm is the Vocabulary of a lineage written as thema.Lineage CUE:
// schemas keyed by version, lenses keyed by edge, with input and result.
var NormalForm Vocabulary = normalForm{}

type normalForm struct{}

func edge(a Address) string { return a.From.String() + "->" + a.To.String() }

func (normalForm) Path(a Address) []string {
	sch := func(v Version) []string { return []string{"schemas", fmt.Sprint(v[0]), fmt.Sprint(v[1])} }
	lens := func() []string { return []string{"lenses", edge(a)} }
	switch a.Kind {
	case AddrLineage:
		return nil
	case AddrName:
		return []string{"name"}
	case AddrJoinSchema:
		return []string{"joinSchema"}
	case AddrSchema:
		return append(sch(a.Version), "schema")
	case AddrSchemaField:
		return append(append(sch(a.Version), "schema"), a.Field...)
	case AddrExample:
		return append(sch(a.Version), "examples", a.Name)
	case AddrLens, AddrStep:
		return lens()
	case AddrLensInput:
		return append(lens(), "input")
	case AddrLensResult:
		return append(lens(), "result")
	case AddrLensResultField:
		return append(append(lens(), "result"), a.Field...)
	case AddrLacuna:
		return append(lens(), "lacunas", fmt.Sprint(a.Index))
	case AddrLacunaField:
		return append(lens(), "lacunas", fmt.Sprint(a.Index), a.Name)
	case AddrData:
		return a.Field
	}
	return nil
}

func (normalForm) Term(a Address) string {
	field := strings.Join(a.Field, ".")
	switch a.Kind {
	case AddrLineage:
		return "the lineage"
	case AddrName:
		return "name"
	case AddrJoinSchema:
		return "joinSchema"
	case AddrSchema:
		return "schema " + a.Version.String()
	case AddrSchemaField:
		return field + " in schema " + a.Version.String()
	case AddrExample:
		return "example " + a.Name + " of schema " + a.Version.String()
	case AddrLens:
		return "lens " + edge(a)
	case AddrLensInput:
		return "the input of lens " + edge(a)
	case AddrLensResult:
		return "the result of lens " + edge(a)
	case AddrLensResultField:
		return field + " in the result of lens " + edge(a)
	case AddrLacuna:
		return fmt.Sprintf("lacuna %d of lens %s", a.Index, edge(a))
	case AddrLacunaField:
		return fmt.Sprintf("%s of lacuna %d of lens %s", a.Name, a.Index, edge(a))
	case AddrStep:
		return "the step from " + a.From.String() + " to " + a.To.String()
	case AddrData:
		if field == "" {
			return "the data"
		}
		return field + " in the data"
	}
	return string(a.Kind)
}
