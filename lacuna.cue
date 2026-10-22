package thema

// A Lacuna represents a semantic gap in a Lens's mapping between schemas.
//
// For any given mapping between schema, there may exist some valid values and
// intended semantics on either side that are impossible to precisely translate.
// When such gaps occur, and an actual schema instance falls into such a gap,
// the Lens is expected to emit Lacuna that describe the general nature of the
// translation gap.
//
// A lens lists every lacuna it can emit unconditionally, in its lacunas field.
// Each lacuna's condition decides whether it applies to the particular instance
// being translated: some gaps exist for every instance (condition: true, the
// default), others only when certain values appear in the instance. Listing them
// this way means the lacunas a lens can emit are known before any data arrives.
Lacuna: {
	// Indicates whether the lacuna applies to the particular object being translated.
	// If this field evaluates to false, this lacuna is not emitted for a particular
	// translation. If unspecified, defaults to true.
	condition: bool | *true

	// The fields in the pre-translation instance that are relevant to the lacuna.
	// Each path must be declared by the lens's 'from' schema.
	sourceFields: [...LacunaFieldRef]

	// The fields in the post-translation instance that are relevant to the lacuna.
	// Each path must be declared by the lens's 'to' schema.
	targetFields: [...LacunaFieldRef]

	// At least one of sourceFields or targetFields must be non-empty.
	// TODO(must) https://github.com/cue-lang/cue/issues/943

	// A human-readable message describing the gap in translation.
	message!: string

	// The kind of gap; see LacunaTypes.
	type!: or([for _, t in LacunaTypes {t}])

	[!~"^(condition|sourceFields|targetFields|message|type)$"]~(K,_): error("\(K) is not a field of a lacuna; a lacuna has only condition, sourceFields, targetFields, message and type")
}

// A reference to a field in an instance being translated, and its value at the
// time of translation.
LacunaFieldRef: {
	// Path to the field, in CUE path syntax (e.g. "a.b", "list[0]").
	path!: string

	// The value of the field at the time of translation.
	value: _

	[!~"^(path|value)$"]~(K,_): error("\(K) is not a field of a lacuna field reference; it has only path and value")
}

// LacunaTypes enumerates the kinds of gap a lacuna can describe. A lacuna's type
// is the type's name; LacunaTypes.X is a convenient way to refer to it.
LacunaTypes: {
	// Placeholder lacunas indicate that a field in the target instance has
	// been filled with a placeholder value.
	//
	// Use Placeholder when introducing a new required field that lacks a default,
	// and it is necessary to fill the field with some value to meet lens
	// validity requirements.
	//
	// A placeholder is NOT a schema-defined default. It is expressly the
	// opposite: a lens-defined value that exists solely to be replaced by the
	// calling program.
	Placeholder: "Placeholder"

	// DroppedField lacunas indicate that field(s) in the source instance were
	// dropped in a manner that potentially lost some of their contained semantics.
	//
	// When a lens drops multiple fields, prefer to create one DroppedField
	// lacuna per distinct cause. For example, if multiple instance fields are
	// dropped from a single open struct because they were absent from the
	// schema, all of those fields should be included in a single DroppedField.
	DroppedField: "DroppedField"

	// LossyFieldMapping lacunas indicate that no clear mapping existed from the
	// source field value to the intended semantics of any valid target field
	// value, such as a coercion between representations that only some source
	// values survive (e.g. a string field becoming an integer, where "12" parses
	// and "twelve" does not).
	//
	// Only use this lacuna type when there exists at least one valid source
	// value with a clear, lossless mapping to the target value.
	LossyFieldMapping: "LossyFieldMapping"

	// ChangedDefault lacunas indicate that the source field value was equal to the
	// source schema's default for the field, the target schema's default for the
	// field differs, and the value in the instance was changed accordingly.
	//
	// Whether the source value was written explicitly or filled in from the default
	// is not considered: the intent behind including or omitting a field is not
	// knowable.
	ChangedDefault: "ChangedDefault"

	// PrecisionLoss lacunas indicate that every source value maps to a target
	// value, but with reduced fidelity, such as seconds becoming whole
	// milliseconds, or timestamps being truncated.
	PrecisionLoss: "PrecisionLoss"

	// AmbiguousMapping lacunas indicate that the lens chose one of several equally
	// valid target values for the source value, such as splitting a full name
	// into first and last names. Nothing is lost, but the choice may be wrong.
	AmbiguousMapping: "AmbiguousMapping"

	// Defaulted lacunas indicate that a field in the target instance holds the
	// target schema's default for the field because the source instance had no
	// corresponding data. The value is a legitimate one chosen by the schema, not
	// a placeholder, but it was inferred rather than stated.
	Defaulted: "Defaulted"
}
