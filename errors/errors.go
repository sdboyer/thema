package errors

import (
	"errors"
)

// Validation errors. Each failure is a diag.Diagnostic whose code unwraps to
// ErrInvalidData and to the sentinel for its kind, so errors.Is tells them
// apart; the codes themselves (E05xx) are the stable identity.
var (
	// ErrInvalidData is the general error that indicates some data failed validation
	// against a Thema schema. Use it with errors.Is() to differentiate validation errors
	// from other classes of failure.
	ErrInvalidData = errors.New("data not a valid instance of schema")

	// ErrInvalidExcessField indicates a validation failure in which the schema is
	// treated as closed, and the data contains a field not specified in the schema.
	ErrInvalidExcessField = errors.New("data contains field not present in schema")

	// ErrInvalidMissingField indicates a validation failure in which the data lacks
	// a field that is required in the schema.
	ErrInvalidMissingField = errors.New("required field is absent in data")

	// ErrInvalidKindConflict indicates a validation failure in which the schema and
	// data values are of differing, conflicting kinds - the schema value does not
	// subsume the data value. Example: data: "foo"; schema: int
	ErrInvalidKindConflict = errors.New("schema and data are conflicting kinds")

	// ErrInvalidOutOfBounds indicates a validation failure in which the data and
	// schema have the same (or subsuming) kinds, but the data is out of
	// schema-defined bounds. Example: data: 4; schema: int & <3
	ErrInvalidOutOfBounds = errors.New("data is out of schema bounds")
)

// Translation errors. These all occur as a result of an invalid lens. Currently
// these may be returned from [thema.Instance.Translate]. Eventually, it is
// hoped that they will be caught statically in [thema.BindLineage] and cannot
// occur at runtime.
var (
	// ErrInvalidLens indicates that a lens is not correctly written. It is the parent
	// to all other lens and translation errors, and is a child of ErrInvalidLineage.
	ErrInvalidLens = errors.New("lens is invalid")

	// ErrLensIncomplete indicates that translating some valid data through
	// a lens produced a non-concrete result. This always indicates a problem with the
	// lens as it is written, and as such is a child of ErrInvalidLens.
	ErrLensIncomplete = errors.New("result of lens translation is not concrete")

	// ErrLensResultIsInvalidData indicates that translating some valid data through a
	// lens produced a result that was not an instance of the target schema. This
	// always indicates a problem with the lens as it is written, and as such is a
	// child of ErrInvalidLens.
	ErrLensResultIsInvalidData = errors.New("result of lens translation is not valid for target schema")
)

// Lower level general errors
var (
	// ErrValueNotExist indicates that a necessary CUE value did not exist.
	ErrValueNotExist = errors.New("cue value does not exist")

	// ErrValueNotALineage indicates that a provided CUE value is not a lineage.
	// This is almost always an end-user error - they oops'd and provided the
	// wrong path, file, etc.
	ErrValueNotALineage = errors.New("not a lineage")

	// ErrInvalidLineage indicates that a provided lineage does not fulfill one
	// or more of the Thema invariants.
	ErrInvalidLineage = errors.New("invalid lineage")

	// ErrNonContiguousVersions indicates that a lineage's schema versions skip a
	// version: they do not start at 0.0, or a schema's version is neither the next
	// minor version nor the next major version after its predecessor's.
	ErrNonContiguousVersions = errors.New("schema versions in lineage are not contiguous")

	// ErrDuplicateLenses indicates that a lens was defined declaratively in CUE, but the same lens
	// was also provided as a Go function to BindLineage.
	ErrDuplicateLenses = errors.New("lens is declared in both CUE and Go")

	// ErrMissingLenses indicates that a lineage lacks at least one of the lenses
	// its set of schemas requires.
	ErrMissingLenses = errors.New("not all expected lenses were provided")

	// ErrErroneousLenses indicates that a lineage declares a lens that is not one
	// of those its set of schemas requires.
	ErrErroneousLenses = errors.New("unexpected lenses were erroneously provided")

	// ErrVersionNotExist indicates that no schema exists in a lineage with a
	// given version.
	ErrVersionNotExist = errors.New("lineage does not contain schema with version") // ErrNoSchemaWithVersion

	// ErrMalformedSyntacticVersion indicates a string input of a syntactic
	// version was malformed.
	ErrMalformedSyntacticVersion = errors.New("not a valid syntactic version")
)

// Development states. A lineage's tip is a provisional version, which only
// the development bind path (load.DevelopmentLineage) binds. The states that
// path tolerates, and the errors it reports about them, unwrap to
// ErrProvisional and to nothing else: none is an error in the lineage, and
// none can arise from BindLineage.
var (
	// ErrProvisional indicates a state of a provisional version that the
	// development bind path tolerates: a lens missing on the edge into the
	// tip, a translation that needs such a lens, a tip that differs from the
	// newest frozen version.
	ErrProvisional = errors.New("provisional version")
)
