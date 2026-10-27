package thema

import "list"

// TODO docs
TranslatedInstance: {
	from:    SyntacticVersion
	to:      SyntacticVersion
	result:  _
	lacunas: [...Lacuna]
}

// Translate takes an instance, a lineage, and to and from versions.
//
// The provided instance must be a valid instance of the 'from' schema.
//
// The instance is iteratively transformed through the lineage's lenses,
// starting from the version the instance is valid against, and
// continuing until the target schema version is reached.
//
// Each step's result is unified with the target schema, closed at that point of
// use: a lens may never produce a field its target schema does not define,
// whether or not the lineage author wrote that schema as open.
//
// The result is the instance in final translated form, the schema versions
// at which the translation started and ended, and any lacunas emitted during
// translation.
//
// TODO functionize
Translate: {
	lin:  _
	inst: {...}
	from: SyntacticVersion
	to:   SyntacticVersion

	// Nested structs below declare their own from/to fields, so bind the
	// arguments to names that can't be shadowed.
	let L = lin

	// The instance as an instance of its schema, so that lenses see the schema's
	// defaults for fields the data omits.
	let I = inst & L.schemas["\(from[0])"]["\(from[1])"]._schema
	let FV = from
	let TV = to

	let cmp = (_cmpSV & {l: FV, r: TV}).out

	// TODO validate I is instance of FV schema

	out: {
		steps: [...TranslatedInstance]
		result: {
			from: FV
			to:   TV
			// Chosen by step count, not as a disjunction default: an erroneous
			// default is silently discarded, which would turn a broken lens into
			// an untranslated copy of the input.
			if len(steps) > 0 {
				result: steps[len(steps)-1].result
			}
			if len(steps) == 0 {
				result: I
			}
		}
	}
	out: {
		steps: [
			// to version older/smaller than from version
			if cmp == 1 {
				let hi = (L._flatidx & {v: FV}).out
				let lo = (L._flatidx & {v: TV}).out

				_accum: [{result: I, to: FV}, for i, pos in list.Range(hi - 1, lo - 1, -1) {
					// alias pointing to the previous item in the list we're building
					let prior = _accum[i]

					// the version stepped to, and its schema def
					let V = L._ordered[pos]
					let schdef = L.schemas["\(V[0])"]["\(V[1])"]

					// TODO does having this field in the result, even hidden, cause a problem? does using an alias cause the computation to run more than once?
					// The lens binds the instance it takes, and the one it returns, to
					// fields named after their versions.
					let FN = "v\(prior.to[0])_\(prior.to[1])"
					let TN = "v\(V[0])_\(V[1])"
					_lens: L.lenses["\(prior.to[0]).\(prior.to[1])->\(V[0]).\(V[1])"] & {
						// pass the prior result along as the instance this lens takes
						(FN): prior.result
					}

					from: prior.to
					to:   V
					// TODO initial input isn't necessarily unified with schema - does that make translated output meaningfully different?
					result:  _lens[TN] & ({#closed: schdef._schema}).#closed
					lacunas: [for lac in _lens.lacunas if lac.condition {lac}]
				}]

				// Final value excludes first element (initial input) from accum
				list.Drop(_accum, 1)
			},
			// to version newer/larger than from version
			if cmp == -1 {
				// vrange contains the versions being traversed in this translation,
				// exclusive of the starting version.
				let lo = (L._flatidx & {v: FV}).out
				let hi = (L._flatidx & {v: TV}).out
				let vrange = list.Slice(L._ordered, lo + 1, hi + 1)

				_accum: [{result: I, to: FV}, for i, V in vrange {
					// alias pointing to the previous item in the list we're building
					let prior = _accum[i]
					let schdef = L.schemas["\(V[0])"]["\(V[1])"]

					if prior.to[0] == V[0] {
						// Only a minor version. Backwards compatibility rules dictate that the mapping
						// algorithm for the forward lens is generic: simple unification.
						from:   prior.to
						to:     V
						result: prior.result & ({#closed: schdef._schema}).#closed
					}

					if prior.to[0] < V[0] {
						// Crossing a major version. The forward lens explicitly defined in the schema
						// provides the mapping algorithm.
						// TODO does having this field in the result, even hidden, cause a problem? does using an alias cause the computation to run more than once?
						let FN = "v\(prior.to[0])_\(prior.to[1])"
						let TN = "v\(V[0])_\(V[1])"
						_lens: L.lenses["\(prior.to[0]).\(prior.to[1])->\(V[0]).\(V[1])"] & {
							// pass the prior result along as the instance this next lens takes
							(FN): prior.result
						}

						from:    prior.to
						to:      V
						result:  _lens[TN] & ({#closed: schdef._schema}).#closed
						lacunas: [for lac in _lens.lacunas if lac.condition {lac}]
					}
				}]

				// Final value excludes first element (initial input) from accum
				list.Drop(_accum, 1)
			},
			// to version same as from version is a no-op
			if cmp == 0 {[]},
		][0]
	}
}
