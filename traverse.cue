package thema

// SearchAndValidate is a pseudofunction that takes a lineage (lin) and some candidate
// data (inst) as an argument, and searches the lineage for a schema against which
// that data is valid.
//
// A LinkedInstance is "returned" (out) corresponding to the first schema in the
// lineage for which the data is a valid instance of the schema. Data is not checked
// for concreteness. If there is no match, bottom is returned.
//
// Schemas are closed at the point of use, matching the default of the Go
// Schema.Validate: data with fields a schema does not define is not an instance.
//
// These pseudofunctions exist to allow limited thema operations in pure CUE. The
// Go library implements the same operations natively rather than calling these.
//
// TODO optionally check for concreteness of otherwise valid data
// TODO functionize
SearchAndValidate: {
	lin:  _
	inst: {...} // TODO consistently rename to 'object' or something

	// The yielded structs below declare their own lin/inst fields.
	let LIN = lin
	let INST = inst

	out: LinkedInstance
	out: [for _, V in lin._ordered
	let sch = lin.schemas["\(V[0])"]["\(V[1])"]
	let closed = ({#closed: sch._schema}).#closed
	if (INST & closed) != _|_ {
		v:    V
		lin:  LIN
		inst: INST & closed
	}, _|_][0]
}

ValidFor: {
	lin:  _
	inst: {...} // TODO consistently rename to 'object' or something

	out: SyntacticVersion
	out: [for _, V in lin._ordered if (({#closed: lin.schemas["\(V[0])"]["\(V[1])"]._schema}).#closed & inst) != _|_ {V}][0]
}

// LinkedInstance represents data that is an instance of some schema, the
// version of that schema, and the lineage of the schema.
LinkedInstance: {
	inst: {...}            // TODO consistently rename to 'object' or something
	lin:  Lineage
	v:    SyntacticVersion // TODO rename to 'version'

	// TODO need proper validation/subsumption check here, not simple unification
}
