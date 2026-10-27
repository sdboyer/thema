// Package rulecodes maps the rules of the compatibility checker and the
// totality checker to their diagnostic codes. Both checkers' rules are stable
// identifiers of their own (docs/invariants.md); each rule has one code, and
// the lockstep tests in the thema root hold the two in step. Binding and
// compiling both report checker findings, so the maps live where both reach.
package rulecodes

import (
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/lenscheck"
)

// Compat is the code of each compatibility rule.
var Compat = map[compat.Rule]diag.Code{
	compat.RuleFieldAddedRequired: diag.E0210,
	compat.RuleFieldRemoved:       diag.E0211,
	compat.RuleFieldNowRequired:   diag.E0212,
	compat.RuleDefaultRemoved:     diag.E0213,
	compat.RuleDefaultChanged:     diag.E0214,
	compat.RuleValueNarrowed:      diag.E0215,
	compat.RuleLengthNarrowed:     diag.E0216,
	compat.RuleLabelsNarrowed:     diag.E0217,
	compat.RuleValidatorAdded:     diag.E0218,
	compat.RuleUnknownCrossField:  diag.E0230,
	// A comprehension in a schema is refused before compatibility is checked
	// (bind.go checkSchemaConstructs), under the same code.
	compat.RuleUnknownComprehension: diag.E0132,
	compat.RuleUnknownValidator:     diag.E0232,
	compat.RuleUnknownDisjunction:   diag.E0233,
	compat.RuleUnknownPatterns:      diag.E0234,
}

// CompatCode is the code of rule r; a rule the map lacks reports as
// value-narrowed, the most general incompatibility.
func CompatCode(r compat.Rule) diag.Code {
	if c, ok := Compat[r]; ok {
		return c
	}
	return diag.E0215
}

// Lenscheck is the code of each totality rule.
var Lenscheck = map[lenscheck.Rule]diag.Code{
	lenscheck.RuleCopyNarrowed:       diag.E0360,
	lenscheck.RuleSourceAbsent:       diag.E0361,
	lenscheck.RuleSourceUndeclared:   diag.E0362,
	lenscheck.RuleTargetUndeclared:   diag.E0305, // one rule, two enforcement sites: the lens-result-fields seam
	lenscheck.RuleSourceConstrained:  diag.E0303, // one rule, two enforcement sites: bind's subsumption check and the totality walk
	lenscheck.RulePatternAbsent:      diag.E0364,
	lenscheck.RuleNotAValue:          diag.E0365,
	lenscheck.RuleLiteralNotInstance: diag.E0366,
	lenscheck.RuleComputedNarrowed:   diag.E0367,
	lenscheck.RuleRequiredNotWritten: diag.E0368,
	lenscheck.RuleWriteConflict:      diag.E0369,
	lenscheck.RuleArithOverflow:      diag.E0370,
	lenscheck.RuleDivisionByZero:     diag.E0371,
	lenscheck.RuleTableKeyMissing:    diag.E0372,

	lenscheck.RuleUnknownCopy:             diag.E0380,
	lenscheck.RuleUnknownPattern:          diag.E0381,
	lenscheck.RuleUnknownExhaustive:       diag.E0382,
	lenscheck.RuleUnknownOp:               diag.E0383,
	lenscheck.RuleUnknownDynamic:          diag.E0384,
	lenscheck.RuleUnknownReference:        diag.E0385,
	lenscheck.RuleUnknownBounds:           diag.E0386,
	lenscheck.RuleUnknownDivisor:          diag.E0387,
	lenscheck.RuleUnknownTargetConstraint: diag.E0388,
	lenscheck.RuleUnknownTableKey:         diag.E0389,
	lenscheck.RuleUnknownHead:             diag.E0390,
}

// LenscheckCode is the code of rule r; a rule the map lacks reports as
// copy-narrowed.
func LenscheckCode(r lenscheck.Rule) diag.Code {
	if c, ok := Lenscheck[r]; ok {
		return c
	}
	return diag.E0360
}
