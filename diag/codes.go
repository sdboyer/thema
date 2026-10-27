package diag

import (
	"slices"
	"strings"

	terrors "github.com/sdboyer/thema/errors"
)

// CodeInfo declares one code: what it means and how it behaves.
type CodeInfo struct {
	Code     Code
	Severity Severity
	// Title is a one-line statement of the rule, for the docs index and as the
	// default headline. It obeys the same style rules as a headline.
	Title string
	// Sentinels are the thema/errors sentinels an [Error] with this code
	// unwraps to, for errors.Is.
	Sentinels []error
	// Positionless marks a code whose diagnostics may lack a primary position,
	// because the problem is the absence of something rather than a construct:
	// no version files, a lens that is missing. Every other code must find the
	// author's position, and the reproducer tests enforce it.
	Positionless bool
}

// Codes declares every code, sorted. The hundreds group them:
//
//	E01xx  the shape of authoring files and of the lineage
//	E02xx  versions and compatibility
//	E03xx  lenses
//	E04xx  lacunas
//	E05xx  data validation
//	E06xx  loading, modules and the CLI's inputs
//	E09xx  thema itself: internal errors and CUE errors thema does not recognize
//
// A code's meaning never changes. A code that stops being reported is retired,
// not reused.
var Codes = []CodeInfo{
	{Code: E0501, Severity: SeverityError, Title: "the data lacks a field the schema requires", Sentinels: []error{terrors.ErrInvalidData, terrors.ErrInvalidMissingField}},
	{Code: E0502, Severity: SeverityError, Title: "the data has a field the schema does not declare", Sentinels: []error{terrors.ErrInvalidData, terrors.ErrInvalidExcessField}},
	{Code: E0503, Severity: SeverityError, Title: "a value in the data is of a kind the schema does not accept", Sentinels: []error{terrors.ErrInvalidData, terrors.ErrInvalidKindConflict}},
	{Code: E0504, Severity: SeverityError, Title: "a value in the data is outside what the schema allows", Sentinels: []error{terrors.ErrInvalidData, terrors.ErrInvalidOutOfBounds}},
	{Code: E0505, Severity: SeverityError, Title: "a value in the data matches none of the alternatives the schema allows", Sentinels: []error{terrors.ErrInvalidData, terrors.ErrInvalidOutOfBounds}},
	{Code: E0506, Severity: SeverityError, Title: "the data is not concrete", Sentinels: []error{terrors.ErrInvalidData}, Positionless: true},
	{Code: E0901, Severity: SeverityError, Title: "thema failed internally", Positionless: true},
	{Code: E0902, Severity: SeverityError, Title: "CUE reported an error thema does not recognize", Positionless: true},
	{Code: E0903, Severity: SeverityError, Title: "thema reported an error that has no code", Positionless: true},
}

// The codes.
const (
	// E0501: validating data, a field the schema requires is absent.
	E0501 Code = "E0501"
	// E0502: validating data, a field the schema does not declare is present.
	// Schemas are closed when validating.
	E0502 Code = "E0502"
	// E0503: validating data, a value is of the wrong kind: a string where the
	// schema declares an int.
	E0503 Code = "E0503"
	// E0504: validating data, a value of the right kind is outside what the
	// schema allows: out of a bound, failing a validator, or not the constant
	// the schema fixes.
	E0504 Code = "E0504"
	// E0505: validating data, a value matches none of the alternatives of a
	// disjunction in the schema. Each alternative's reason is a note.
	E0505 Code = "E0505"
	// E0506: validating data, the data itself is not concrete.
	E0506 Code = "E0506"
	// E0901: a panic inside thema, recovered at an operation boundary. The
	// detail holds the panic value and stack.
	E0901 Code = "E0901"
	// E0902: an error from the CUE evaluator that no code describes. The
	// detail holds CUE's message verbatim, so nothing is lost.
	E0902 Code = "E0902"
	// E0903: a plain Go error from thema that was never given a code. Every
	// one of these is a gap to close; the detail holds its text.
	E0903 Code = "E0903"
)

// Lookup returns the declaration of c.
func Lookup(c Code) (CodeInfo, bool) {
	i, ok := slices.BinarySearchFunc(Codes, c, func(ci CodeInfo, c Code) int {
		return strings.Compare(string(ci.Code), string(c))
	})
	if !ok {
		return CodeInfo{}, false
	}
	return Codes[i], true
}
