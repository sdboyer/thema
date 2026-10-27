// Package lacuna defines the Go types for lacunas, the semantic gaps a lens
// reports when it translates an instance between schemas.
//
// This package does not depend on CUE, and must not come to. Lacunas are
// emitted not only by thema's CUE-evaluating Translate, but also by engines
// that run lenses compiled from a lineage and never link CUE. A test enforces
// the package's CUE-freedom.
//
// The kinds of lacuna are defined, and documented, in thema's CUE (LacunaTypes
// in lacuna.cue); the Go constants here mirror them, and a test keeps the two
// in lockstep. Checks that need CUE, such as that a lacuna's field references
// name fields its schemas declare, live with the callers that have CUE.
package lacuna

import (
	"slices"
	"strconv"
)

// TranslationLacunas holds the lacunas emitted by a translation, step by step.
//
// A translation between non-adjacent schemas passes through each intermediate
// schema. Each step's lacunas are expressed in that step's terms: field paths
// refer to the step's source and target schemas, and are not re-mapped through
// later steps.
type TranslationLacunas []TranslationStep

// TranslationStep is a single step of a translation, between adjacent schemas,
// and the lacunas emitted by that step.
type TranslationStep struct {
	From    SyntacticVersion `json:"from"`
	To      SyntacticVersion `json:"to"`
	Lacunas []Lacuna         `json:"lacunas"`
}

// SyntacticVersion is the position of a schema within a lineage: the index of
// the schema's sequence within the lineage, then the index of the schema within
// that sequence.
//
// It duplicates thema.SyntacticVersion, which this package cannot import
// without depending on CUE. The two have the same underlying type, so each
// converts to the other.
type SyntacticVersion [2]uint

// String formats the version canonically, as "<sequence>.<schema>".
func (sv SyntacticVersion) String() string {
	return strconv.FormatUint(uint64(sv[0]), 10) + "." + strconv.FormatUint(uint64(sv[1]), 10)
}

// AsList returns the lacunas from all steps of the translation, in order. It
// loses the information about which step emitted each.
func (tl TranslationLacunas) AsList() []Lacuna {
	l := make([]Lacuna, 0)
	for _, step := range tl {
		l = append(l, step.Lacunas...)
	}
	return l
}

// A Lacuna represents a semantic gap in a Lens's mapping between schemas.
//
// For any given mapping between schema, there may exist some valid values and
// intended semantics on either side that are impossible to precisely translate.
// When such gaps occur, and an actual schema instance falls into such a gap,
// the Lens is expected to emit Lacuna that describe the general nature of the
// translation gap.
//
// The production of a lacuna as the output of the translation of a particular
// instance indicates the lacuna applies to that specific translation.
type Lacuna struct {
	// The fields in the pre-translation instance that are relevant to the lacuna.
	SourceFields []FieldRef `json:"sourceFields,omitempty"`

	// The fields in the post-translation instance that are relevant to the lacuna.
	TargetFields []FieldRef `json:"targetFields,omitempty"`

	// The kind of gap.
	Type Type `json:"type"`

	// A human-readable message describing the gap in translation.
	Message string `json:"message"`
}

// Type identifies a kind of gap a lacuna can describe. The types are
// defined, and documented, in thema's CUE (LacunaTypes in lacuna.cue).
type Type string

// The kinds of gap a lacuna can describe. See LacunaTypes in lacuna.cue for what
// each means.
const (
	Placeholder       Type = "Placeholder"
	DroppedField      Type = "DroppedField"
	LossyFieldMapping Type = "LossyFieldMapping"
	ChangedDefault    Type = "ChangedDefault"
	PrecisionLoss     Type = "PrecisionLoss"
	AmbiguousMapping  Type = "AmbiguousMapping"
	Defaulted         Type = "Defaulted"
)

// types lists every known Type, in the order thema's CUE declares them. It must
// match LacunaTypes in lacuna.cue, which a test checks.
var types = []Type{Placeholder, DroppedField, LossyFieldMapping, ChangedDefault, PrecisionLoss, AmbiguousMapping, Defaulted}

// Types returns every known Type, in the order thema's CUE declares them.
func Types() []Type {
	return slices.Clone(types)
}

// Known reports whether t is one of the lacuna types thema defines.
func (t Type) Known() bool {
	return slices.Contains(types, t)
}

// FieldRef identifies a field in an instance being translated, and its value at
// the time of translation.
type FieldRef struct {
	// Path to the field, in CUE path syntax.
	Path string `json:"path"`

	// The value of the field at the time of translation.
	Value interface{} `json:"value"`
}
