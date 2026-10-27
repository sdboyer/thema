package thema

import (
	"list"
	"regexp"
	"strconv"
	"struct"
)

// Lineage is the top-level container in thema, holding the complete
// evolutionary history of a particular kind of object: every schema that has
// ever existed for that object, and the lenses that allow translating between
// those schema versions.
//
// Lineage and the other structures in thema's CUE are deliberately not
// definitions. Definitions close recursively, which would impose closedness on
// every schema a lineage author writes. thema takes no position on the openness
// of schemas; operations that need closed semantics apply it where they are used.
Lineage: {
	// The name of the thing specified by the schemas in this lineage.
	//
	// A lineage's name must not change as it evolves. It must start with an
	// ASCII letter, followed by ASCII letters, digits and underscores only, so
	// that every language code is generated for can derive identifiers,
	// package names and file names from it mechanically. Cap'n Proto, which
	// forbids underscores in names, drops them (ship_v2 becomes shipv2), which
	// is predictable but can make two names collide; that is accepted.
	name!: =~"^[A-Za-z][A-Za-z0-9_]*$"

	// joinSchema governs the shape of schema that may be expressed in a
	// lineage. It is the least upper bound, or join, of the acceptable schema
	// value space; the schemas defined in this lineage must be instances of the
	// joinSchema.
	//
	// All Thema schemas must be struct-kinded. Consequently, if a lineage defines
	// a joinSchema, it must be a struct containing at least one field.
	//
	// A lineage's joinSchema must never change as the lineage evolves.

	//	joinSchema?: struct.MinFields(1)
	joinSchema: _

	// The keys of schemas (each a major or minor version) and of lenses (an edge
	// between two versions).
	let versionKey = "^(0|[1-9][0-9]*)$"
	let edgeKey = "^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)->(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)$"

	// schemas holds every schema in the lineage, keyed by major version, then by
	// minor version: schemas["1"]["0"] is the schema for version 1.0. Keys are
	// non-negative decimal integers, without leading zeros.
	//
	// Each schema is a SchemaDef, injected with the joinSchema for this lineage.
	// Its version is its key; a schema entry does not declare it.
	//
	// Versions are contiguous: majors run from 0 without gaps, and the minors of
	// each major run from 0 without gaps. A skipped version is a missing key.
	schemas!: struct.MinFields(1) & {
		[=~versionKey]~(M,_): struct.MinFields(1) & {
			[=~versionKey]~(m,_): SchemaDef & {
				_join:    joinSchema
				_version: [strconv.Atoi(M), strconv.Atoi(m)]
			}

			[!~versionKey]~(K,_): error("\(K) is not a minor version; schemas are keyed by major, then minor version, each a non-negative integer such as \"0\"")
		}

		[!~versionKey]~(K,_): error("\(K) is not a major version; schemas are keyed by major, then minor version, each a non-negative integer such as \"0\"")
	}

	// lenses contains all the mappings between all the schemas in the lineage,
	// keyed by the edge each one maps across: lenses["1.0->0.1"] maps instances
	// of schema 1.0 to schema 0.1. The source and target versions are the key's;
	// a lens does not declare them.
	//
	// For a lineage to be valid, it must contain lenses such that an instance of
	// each schema can be translated into both its predecessor and successor schemas.
	//
	// Because minor version changes are backwards compatible by definition, a lens
	// implicitly exists, and no lens definition may be defined by the lineage author.
	// However, all other version transitions require an explicit lens definition:
	//
	//  - A lens mapping forward across every breaking change/new major version
	//  - A lens mapping backward across every change
	//
	// Thus, for a lineage with schema versions 0.0, 0.1, 1.0, 2.0, 2.1, the
	// following lenses must exist (implicit lenses are wrapped in parentheses):
	//
	//  "0.1->0.0"
	// ("0.0->0.1")
	//  "1.0->0.1"
	//  "0.1->1.0"
	//  "2.0->1.0"
	//  "1.0->2.0"
	//  "2.1->2.0"
	// ("2.0->2.1")
	//
	// To be valid, a lineage must define the exact set of explicit lenses entailed by its
	// set of schema versions. It is not permitted to explicitly define a lens across
	// non-breaking changes.
	lenses: {
		// The lens's field names come from its edge, so the edge is parsed
		// here, outside the lens: a field of the lens that the lens's own
		// field names depended on would be a cycle.
		[=~edgeKey]~(E,_): (Lens & {
			from: [strconv.Atoi(regexp.FindSubmatch(edgeKey, E)[1]), strconv.Atoi(regexp.FindSubmatch(edgeKey, E)[2])]
			to:   [strconv.Atoi(regexp.FindSubmatch(edgeKey, E)[3]), strconv.Atoi(regexp.FindSubmatch(edgeKey, E)[4])]
		}).out

		[!~edgeKey]~(K,_): error("\(K) is not a lens edge; lenses are keyed by \"<from>-><to>\" versions, such as \"1.0->0.1\"")
	}

	// Catch misspelled or unknown fields. Hidden and definition fields are not
	// matched, and remain free for lineage authors' own helpers.
	[!~"^(name|joinSchema|schemas|lenses)$"]~(K,_): error("\(K) is not a field of a lineage; a lineage has only name, joinSchema, schemas and lenses")

	// _counts holds the number of minor versions in each major version: the index
	// is the major version, the value its number of minors.
	_counts: [for M in list.Range(0, len(schemas), 1) {len(schemas["\(M)"])}]

	// _ordered is the version of every schema in the lineage, ascending. Versions
	// are contiguous, so it is built by counting through the keys rather than by
	// sorting them; a skipped version is a missing key.
	//
	// It holds versions, not schemas: operations look each schema up by its key.
	// Translate closes each step's result against its target schema; closed
	// against the same schema taken from a list built by comprehension, a result
	// for a schema that is a disjunction with a default re-evaluates in Go to the
	// unclosed default (testdata/cases/shape-schema-default-disj).
	_ordered: [for M in list.Range(0, len(schemas), 1)
	for m in list.Range(0, len(schemas["\(M)"]), 1) {schemas["\(M)"]["\(m)"]._version}]

	// _basis keeps the index of the first schema in each major version
	// within _ordered.
	_basis: [0, for maj, _ in list.Drop(_counts, 1) {
		list.Sum(list.Take(_counts, maj + 1))
	}]

	// latestVersion always contains the SyntacticVersion of the lineage's
	// "latest" schema - the schema with the largest version number.
	//
	// Take care in using this. If any code that depends on schema contents relies
	// on it, that code will break as soon as a breaking schema change is made. This
	// may be desirable within a tight development loop - e.g., for a finite team,
	// working within a single VCS repository - in order to force updating code that
	// must be kept in sync.
	//
	// But relying on it in, for example, an API client based on Thema lineages
	// undermines the entire goal of Thema, as it would forces breaking changes
	// immediately on the client's users, rather than allowing them to update at
	// their own pace.
	//
	// TODO functionize
	#LatestVersion: _ordered[len(_ordered)-1]

	_flatidx: {
		v: SyntacticVersion
		// TODO check what happens when out of bounds
		out: _basis[v[0]] + v[1]
	}
}

// SchemaDef represents a single schema declaration in Thema: the schema itself,
// and optional examples. Its version is the key it is declared at in a
// lineage's schemas; it does not declare one itself.
SchemaDef: {
	// _version is the Syntactic Version number of the schema, filled from the
	// keys the lineage declares the schema at.
	_version: SyntacticVersion

	schema!: _

	_join: _

	// Thema's internal handle for the user-provided schema, unified with the
	// joinSchema of the containing lineage. This handle is used by all
	// helpers/operations in the thema package. It is exactly as open or closed as
	// the lineage author wrote it.
	_schema: _join & schema

	// examples is an optional set of named examples of the schema, intended
	// for use in documentation or other non-functional contexts.
	examples?: [string]: _schema

	[!~"^(schema|examples)$"]~(K,_): error("\(K) is not a field of a schema; a schema entry has only schema and examples")
}

// Lens is the shape of the lens at an edge of a lineage's lenses, as a
// pseudofunction of the edge's versions: (Lens & {from: [0, 0], to: [1, 0]}).out.
// A lens maps instances of the from version to instances of the to version,
// and the lacunas that may exist for specific objects when translating between
// them. The versions are the edge the lens is declared at, e.g. "1.0->0.1"; the
// lens does not declare them itself.
//
// A lens binds the instance it takes to a field named after its version,
// v<major>_<minor>, and builds the instance it returns in a field named the
// same way after the target version. For the edge "0.0->1.0" with schemas
//
//   0.0: {a: string}
//   1.0: {b: string}
//
// a lens that moves a to b is written
//
//   "0.0->1.0": {
//       v0_0: _
//       v1_0: {b: v0_0.a}
//   }
//
// The value built must be an instance of the to schema, constructed through
// references to the from instance. It may not contain any field the to schema
// does not define, whatever the openness of that schema.
Lens: {
	from: SyntacticVersion
	to:   SyntacticVersion

	// takes and returns are the names of the lens's fields for the instance it
	// takes and the instance it returns.
	takes:   "v\(from[0])_\(from[1])"
	returns: "v\(to[0])_\(to[1])"

	// out constrains the two fields by pattern rather than declaring them:
	// a field declared here under a computed label is one CUE's exporter
	// cannot name from a reference, and lenses are read back as syntax.
	// That the author declares both is checked at bind.
	out: {
		// The instance of the from version the lens takes.
		[=~"^\(takes)$"]: _

		// The instance of the to version the lens returns.
		[=~"^\(returns)$"]: struct.MinFields(0)

		// lacunas describe semantic gaps in the transform's mapping. See lacuna docs
		// for more information (TODO).
		lacunas: [...Lacuna]

		[!~"^(\(takes)|\(returns)|lacunas)$"]~(K,_): error("\(K) is not a field of a lens; the lens from \(from[0]).\(from[1]) to \(to[0]).\(to[1]) has only \(takes), \(returns) and lacunas")
	}
}

// SyntacticVersion is an ordered pair of non-negative integers. It represents
// the version of a schema within a lineage, or the version of an instance that
// is valid with respect to the schema of that version.
SyntacticVersion: [uint64, uint64]

// TODO functionize
_cmpSV: {
	l:   SyntacticVersion
	r:   SyntacticVersion
	out: -1 | 0 | 1
	out: {
		if l[0] < r[0] {-1}
		if l[0] > r[0] {1}
		if l[0] == r[0] && l[1] < r[1] {-1}
		if l[0] == r[0] && l[1] > r[1] {1}
		if l == r {0}
	}
}
