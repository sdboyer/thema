package thema

import (
	"fmt"

	"cuelang.org/go/cue"

	"github.com/sdboyer/thema/internal/cuetil"
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
	Type LacunaType `json:"type"`

	// A human-readable message describing the gap in translation.
	Message string `json:"message"`
}

// LacunaType identifies a kind of gap a lacuna can describe. The types are
// defined, and documented, in thema's CUE (LacunaTypes in lacuna.cue).
type LacunaType string

// The kinds of gap a lacuna can describe. See LacunaTypes in lacuna.cue for what
// each means.
const (
	LacunaPlaceholder       LacunaType = "Placeholder"
	LacunaDroppedField      LacunaType = "DroppedField"
	LacunaLossyFieldMapping LacunaType = "LossyFieldMapping"
	LacunaChangedDefault    LacunaType = "ChangedDefault"
	LacunaPrecisionLoss     LacunaType = "PrecisionLoss"
	LacunaAmbiguousMapping  LacunaType = "AmbiguousMapping"
	LacunaDefaulted         LacunaType = "Defaulted"
)

// lacunaTypes holds every known LacunaType. It must match LacunaTypes in thema's
// CUE, which a test checks.
var lacunaTypes = map[LacunaType]bool{
	LacunaPlaceholder: true, LacunaDroppedField: true, LacunaLossyFieldMapping: true, LacunaChangedDefault: true,
	LacunaPrecisionLoss: true, LacunaAmbiguousMapping: true, LacunaDefaulted: true,
}

// FieldRef identifies a field in an instance being translated, and its value at
// the time of translation.
type FieldRef struct {
	// Path to the field, in CUE path syntax.
	Path string `json:"path"`

	// The value of the field at the time of translation.
	Value interface{} `json:"value"`
}

// checkLacuna checks that l, emitted by a lens from sch to tsch, has a known type
// and a message, and that its field references are declared by the schemas they
// refer to: source fields by sch, target fields by tsch.
func checkLacuna(ctx *cue.Context, l Lacuna, sch, tsch *schemaDef) error {
	if !lacunaTypes[l.Type] {
		return fmt.Errorf("lacuna has unknown type %q", l.Type)
	}
	if l.Message == "" {
		return fmt.Errorf("%s lacuna has no message", l.Type)
	}
	for _, fr := range l.SourceFields {
		if err := checkFieldRefPath(ctx, fr.Path, sch); err != nil {
			return fmt.Errorf("%s lacuna source field: %w", l.Type, err)
		}
	}
	for _, fr := range l.TargetFields {
		if err := checkFieldRefPath(ctx, fr.Path, tsch); err != nil {
			return fmt.Errorf("%s lacuna target field: %w", l.Type, err)
		}
	}
	return nil
}

// checkFieldRefPath checks that path is a valid CUE path to a field sch declares.
func checkFieldRefPath(ctx *cue.Context, path string, sch *schemaDef) error {
	p := cue.ParsePath(path)
	if err := p.Err(); err != nil {
		return fmt.Errorf("invalid path %q: %w", path, err)
	}
	if !cuetil.DeclaresPath(ctx, sch.def, p) {
		return fmt.Errorf("path %q is not declared by schema %s", path, sch.Version())
	}
	return nil
}
