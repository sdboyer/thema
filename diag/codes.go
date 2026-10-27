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
	// Internal, when set, marks a code whose condition cannot arise when
	// thema's checker is sound, and says which proof rules it out. Such a code
	// has no reproducer: there is no lineage that shows it. Its page says that
	// seeing it is a bug in thema and asks for a report.
	Internal string
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
	{Code: E0101, Severity: SeverityError, Title: "a file in the lineage's directory is not a version file", Sentinels: []error{terrors.ErrInvalidLineage}, Positionless: true},
	{Code: E0102, Severity: SeverityError, Title: "a version file is not CUE syntax", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0103, Severity: SeverityError, Title: "a version file has a package clause", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0104, Severity: SeverityError, Title: "a version file imports a package", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0105, Severity: SeverityError, Title: "a version file has a field that is not schema, ForwardLens or BackwardLens", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0106, Severity: SeverityError, Title: "a version file declares a field twice", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0107, Severity: SeverityError, Title: "a lens block is not a struct", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0108, Severity: SeverityError, Title: "a version file declares no schema", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0109, Severity: SeverityError, Title: "a version file uses CUE's struct shorthand", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0110, Severity: SeverityError, Title: "the lineage's directory has no version files", Sentinels: []error{terrors.ErrInvalidLineage}, Positionless: true},
	{Code: E0111, Severity: SeverityError, Title: "a lens block is not shaped as a lens", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0112, Severity: SeverityError, Title: "a version file's schema has an error", Sentinels: []error{terrors.ErrInvalidLineage}, Positionless: true},
	{Code: E0120, Severity: SeverityError, Title: "the lineage does not declare a field every lineage has", Sentinels: []error{terrors.ErrValueNotALineage}},
	{Code: E0121, Severity: SeverityError, Title: "a field every lineage has is not concrete", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0122, Severity: SeverityError, Title: "the lineage declares no schemas", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0123, Severity: SeverityError, Title: "a field that is not part of a lineage, a schema entry or a lens", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0124, Severity: SeverityError, Title: "a schemas key that is not a version", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0125, Severity: SeverityError, Title: "a lenses key that is not a lens edge", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0126, Severity: SeverityError, Title: "the lineage is not an instance of thema.Lineage", Sentinels: []error{terrors.ErrInvalidLineage}, Positionless: true},
	{Code: E0128, Severity: SeverityError, Title: "there is no value at the path given for the lineage", Sentinels: []error{terrors.ErrValueNotExist}, Positionless: true},
	{Code: E0129, Severity: SeverityError, Title: "a schema is not a struct", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0130, Severity: SeverityError, Title: "the joinSchema is not a struct", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0131, Severity: SeverityError, Title: "an example is not an instance of its schema", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0132, Severity: SeverityError, Title: "a schema uses a comprehension", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0133, Severity: SeverityError, Title: "a schema's references form a cycle", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0134, Severity: SeverityError, Title: "a schema refers to a value that contains it", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0135, Severity: SeverityError, Title: "a schema's chain of references is too deep to follow", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0137, Severity: SeverityError, Title: "the lineage's name is not an identifier", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0140, Severity: SeverityError, Title: "a schema refers to another schema of the lineage", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0141, Severity: SeverityError, Title: "a schema or lens refers to something the lineage declares outside it", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0142, Severity: SeverityError, Title: "a schema or lens refers to something declared outside the lineage", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0143, Severity: SeverityError, Title: "a schema or lens refers to a package that is not one of CUE's builtins", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0144, Severity: SeverityError, Title: "a lens refers to a schema of the lineage", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0145, Severity: SeverityError, Title: "a lens refers to another lens of the lineage", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0146, Severity: SeverityError, Title: "a reference cannot be resolved statically", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0201, Severity: SeverityError, Title: "the first schema is not version 0.0", Sentinels: []error{terrors.ErrNonContiguousVersions}},
	{Code: E0202, Severity: SeverityError, Title: "a schema's version does not follow its predecessor's", Sentinels: []error{terrors.ErrNonContiguousVersions}},
	{Code: E0203, Severity: SeverityError, Title: "a major version declares no schemas", Sentinels: []error{terrors.ErrNonContiguousVersions}},
	{Code: E0204, Severity: SeverityError, Title: "two version files declare the same version", Sentinels: []error{terrors.ErrNonContiguousVersions}},
	{Code: E0205, Severity: SeverityError, Title: "the version files skip a version", Sentinels: []error{terrors.ErrNonContiguousVersions}},
	{Code: E0206, Severity: SeverityError, Title: "a version file named as a major version is backward compatible with its predecessor", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0208, Severity: SeverityError, Title: "a major version is backward compatible with its predecessor", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0210, Severity: SeverityError, Title: "a minor version requires a field its predecessor does not declare", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0211, Severity: SeverityError, Title: "a minor version rejects a field its predecessor declares", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0212, Severity: SeverityError, Title: "a minor version requires a field its predecessor left optional", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0213, Severity: SeverityError, Title: "a minor version drops a default its predecessor filled", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0214, Severity: SeverityError, Title: "a minor version changes a default", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0215, Severity: SeverityError, Title: "a minor version narrows the values a field accepts", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0216, Severity: SeverityError, Title: "a minor version narrows a list's length", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0217, Severity: SeverityError, Title: "a minor version narrows the field names a struct accepts", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0218, Severity: SeverityError, Title: "a minor version adds or tightens a validator", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0230, Severity: SeverityError, Title: "a minor version constrains a field by another field's value, which the checker cannot compare", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0232, Severity: SeverityError, Title: "a minor version changes a validator the checker has no rule for", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0233, Severity: SeverityError, Title: "a minor version's disjunction cannot be shown to cover its predecessor's", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0234, Severity: SeverityError, Title: "a minor version's pattern constraints cannot be shown to cover its predecessor's", Sentinels: []error{terrors.ErrInvalidLineage}},
	{Code: E0240, Severity: SeverityError, Title: "the lineage has no schema with that version", Sentinels: []error{terrors.ErrVersionNotExist}, Positionless: true},
	{Code: E0242, Severity: SeverityError, Title: "a published schema was deleted", Sentinels: []error{terrors.ErrInvalidLineage}, Positionless: true},
	{Code: E0243, Severity: SeverityError, Title: "a published schema was changed", Sentinels: []error{terrors.ErrInvalidLineage}, Positionless: true},
	{Code: E0301, Severity: SeverityError, Title: "the lineage lacks a lens its versions require", Sentinels: []error{terrors.ErrMissingLenses, terrors.ErrInvalidLineage}, Positionless: true},
	{Code: E0302, Severity: SeverityError, Title: "the lineage declares a lens its versions do not require", Sentinels: []error{terrors.ErrErroneousLenses, terrors.ErrInvalidLineage}},
	{Code: E0303, Severity: SeverityError, Title: "a lens does not accept every instance of its source schema", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0304, Severity: SeverityError, Title: "what a lens produces is not an instance of its target schema", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}, Internal: "every lens is proven total at bind, and the proof names the mapping at fault (E0360 to E0372) before this check runs"},
	{Code: E0305, Severity: SeverityError, Title: "a lens writes a field its target schema does not declare", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0307, Severity: SeverityError, Title: "translating an instance, a lens produced something that is not concrete", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrLensIncomplete}, Internal: "every lens is proven total at bind, which rules out a result field left as a constraint for any instance (E0365)"},
	{Code: E0308, Severity: SeverityError, Title: "translating an instance, a lens failed", Sentinels: []error{terrors.ErrInvalidLens}, Internal: "every lens is proven total at bind, which rules out a result the target schema rejects for any instance (E0360, E0366, E0367)"},
	{Code: E0310, Severity: SeverityError, Title: "a lens does not declare the instance it takes", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0311, Severity: SeverityError, Title: "a lens does not declare the instance it returns", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0330, Severity: SeverityError, Title: "a lens body holds something that is not a mapping", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0331, Severity: SeverityError, Title: "a mapping's key is not a plain field name", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0332, Severity: SeverityError, Title: "a mapping has an attribute", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0333, Severity: SeverityError, Title: "a lens body or its lacunas declare a hidden field or a definition", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0334, Severity: SeverityError, Title: "a nested body embeds something other than a path into the source instance", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0335, Severity: SeverityError, Title: "an _orelse is not _orelse(source path, fallback)", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0336, Severity: SeverityError, Title: "a _match is not _match(source path, arms)", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0337, Severity: SeverityError, Title: "an _each is not _each(source list, body)", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0338, Severity: SeverityError, Title: "a _match's arms are not constant arms with at most one catch-all", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0340, Severity: SeverityError, Title: "a lens reads a field its source version does not declare", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0341, Severity: SeverityError, Title: "a lens maps a field its target version does not declare", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0342, Severity: SeverityError, Title: "a lens drops a field its target version requires", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0343, Severity: SeverityError, Title: "a lens moves an optional field to a required one with no fallback", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0344, Severity: SeverityError, Title: "a constant in a lens is not an instance of its target field", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0345, Severity: SeverityError, Title: "a lens maps a target field twice", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0346, Severity: SeverityError, Title: "a lens produces nothing for a field its target version requires", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0347, Severity: SeverityError, Title: "a field the source version has and the target lacks is neither mapped nor dropped", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0348, Severity: SeverityError, Title: "an _orelse reads a field the source version always has", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0349, Severity: SeverityError, Title: "an _orelse's fallback may itself be absent", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0350, Severity: SeverityError, Title: "a _match has two arms for one value", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0351, Severity: SeverityError, Title: "a _match is not exhaustive", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0352, Severity: SeverityError, Title: "a _match passes values through to a target the lens cannot test them against", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0353, Severity: SeverityError, Title: "an _each maps something other than an open list to an open list", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0354, Severity: SeverityError, Title: "the first version has a lens", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0355, Severity: SeverityError, Title: "a minor version has a ForwardLens", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0356, Severity: SeverityError, Title: "a normal-form lens is outside what the authoring form expresses", Sentinels: []error{terrors.ErrInvalidLens}, Positionless: true},
	{Code: E0357, Severity: SeverityError, Title: "a mapping calls a function a lens may not call", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0360, Severity: SeverityError, Title: "a lens copies a value its target rejects for some instance", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0361, Severity: SeverityError, Title: "a lens reads a field that may be absent", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0362, Severity: SeverityError, Title: "a lens reads a field its source schema does not declare", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0364, Severity: SeverityError, Title: "a lens's pattern tests a field that may be absent", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0365, Severity: SeverityError, Title: "a lens writes a constraint where an instance needs a value", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0366, Severity: SeverityError, Title: "a lens writes a literal its target rejects", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0367, Severity: SeverityError, Title: "a lens computes a value its target rejects for some instance", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0368, Severity: SeverityError, Title: "a lens leaves a required field unwritten for some instance", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0369, Severity: SeverityError, Title: "a lens writes a field twice with values that may disagree", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0370, Severity: SeverityError, Title: "a lens's arithmetic may overflow", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0371, Severity: SeverityError, Title: "a lens may divide by zero", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0372, Severity: SeverityError, Title: "a lens indexes a table with a value that has no entry", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0380, Severity: SeverityError, Title: "a lens copies a value the checker cannot show its target accepts", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0381, Severity: SeverityError, Title: "a lens's condition is not one the checker can read", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0382, Severity: SeverityError, Title: "a lens writes a required field only under conditions the checker cannot show exhaustive", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0383, Severity: SeverityError, Title: "a lens computes with an operation the checker cannot prove", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0384, Severity: SeverityError, Title: "a lens produces fields or elements dynamically", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0385, Severity: SeverityError, Title: "a lens refers to something the checker cannot follow", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0386, Severity: SeverityError, Title: "a lens's integer arithmetic has no bounds the checker can trust", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0387, Severity: SeverityError, Title: "a lens's divisor may be zero as far as the checker can tell", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0388, Severity: SeverityError, Title: "a lens writes to a target constraint the checker cannot compare", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0389, Severity: SeverityError, Title: "a lens indexes a table with a value that is not a finite set of constants", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0390, Severity: SeverityError, Title: "a lens takes the first element of a list that may be empty", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0401, Severity: SeverityError, Title: "a lens's lacunas are not listed unconditionally", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0402, Severity: SeverityError, Title: "a lacuna has no message", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0403, Severity: SeverityError, Title: "a lacuna has no type", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0404, Severity: SeverityError, Title: "a lacuna names a field its schema does not declare", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0405, Severity: SeverityError, Title: "a lacuna's field path is not a path", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0406, Severity: SeverityError, Title: "a lacuna's type is not a lacuna type", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0407, Severity: SeverityError, Title: "a field that is not part of a lacuna", Sentinels: []error{terrors.ErrInvalidLens, terrors.ErrInvalidLineage}},
	{Code: E0410, Severity: SeverityError, Title: "a lacunas declaration is not shaped as lacunas", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0411, Severity: SeverityError, Title: "a lacuna's type is not a lacuna type", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0412, Severity: SeverityError, Title: "a lacunas key names a mapping the lens does not have", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0413, Severity: SeverityError, Title: "a lacuna on a mapping inside _each", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0414, Severity: SeverityError, Title: "a _match's catch-all arm has no lacuna", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0415, Severity: SeverityError, Title: "a lacuna on _each has no when", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0416, Severity: SeverityError, Title: "a _drop has a lacuna that is not its one DroppedField refinement", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0418, Severity: SeverityError, Title: "a condition refers to a name a lens does not define", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0420, Severity: SeverityError, Title: "a lacuna on a _match with no catch-all arm has no when", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0421, Severity: SeverityError, Title: "a lacuna on a _drop inside _each has a when", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0422, Severity: SeverityError, Title: "a when names a field that is not a condition", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0423, Severity: SeverityError, Title: "a when tests the presence of a field it also reads", Sentinels: []error{terrors.ErrInvalidLens}},
	{Code: E0501, Severity: SeverityError, Title: "the data lacks a field the schema requires", Sentinels: []error{terrors.ErrInvalidData, terrors.ErrInvalidMissingField}},
	{Code: E0502, Severity: SeverityError, Title: "the data has a field the schema does not declare", Sentinels: []error{terrors.ErrInvalidData, terrors.ErrInvalidExcessField}},
	{Code: E0503, Severity: SeverityError, Title: "a value in the data is of a kind the schema does not accept", Sentinels: []error{terrors.ErrInvalidData, terrors.ErrInvalidKindConflict}},
	{Code: E0504, Severity: SeverityError, Title: "a value in the data is outside what the schema allows", Sentinels: []error{terrors.ErrInvalidData, terrors.ErrInvalidOutOfBounds}},
	{Code: E0505, Severity: SeverityError, Title: "a value in the data matches none of the alternatives the schema allows", Sentinels: []error{terrors.ErrInvalidData, terrors.ErrInvalidOutOfBounds}},
	{Code: E0506, Severity: SeverityError, Title: "the data is not concrete", Sentinels: []error{terrors.ErrInvalidData}, Positionless: true},
	{Code: E0507, Severity: SeverityError, Title: "the data does not parse", Sentinels: []error{terrors.ErrInvalidData}, Positionless: true},
	{Code: E0508, Severity: SeverityError, Title: "the data is an instance of no schema in the lineage", Sentinels: []error{terrors.ErrInvalidData}, Positionless: true},
	{Code: E0601, Severity: SeverityError, Title: "the lineage path does not exist or cannot be read", Positionless: true},
	{Code: E0602, Severity: SeverityError, Title: "no CUE package was found at the lineage path", Positionless: true},
	{Code: E0603, Severity: SeverityError, Title: "the lineage path holds lineages in several CUE packages", Positionless: true},
	{Code: E0604, Severity: SeverityError, Title: "the CUE path given for the lineage is not a path", Positionless: true},
	{Code: E0605, Severity: SeverityError, Title: "there is no value at the CUE path given for the lineage", Positionless: true},
	{Code: E0606, Severity: SeverityError, Title: "a cue.mod/module.cue above the lineage is not valid", Positionless: true},
	{Code: E0607, Severity: SeverityError, Title: "the CUE module requires a version of thema's CUE other than the one this program embeds", Positionless: true},
	{Code: E0608, Severity: SeverityError, Title: "the lineage imports a package that is not available here", Positionless: true},
	{Code: E0609, Severity: SeverityError, Title: "loading the lineage's CUE failed", Positionless: true},
	{Code: E0611, Severity: SeverityError, Title: "the data's format could not be recognized", Positionless: true},
	{Code: E0612, Severity: SeverityError, Title: "the data's format flag disagrees with its file extension", Positionless: true},
	{Code: E0613, Severity: SeverityError, Title: "no data was given, or the data path cannot be read", Positionless: true},
	{Code: E0614, Severity: SeverityError, Title: "the data format flag names a format thema does not read", Positionless: true},
	{Code: E0616, Severity: SeverityError, Title: "a file cannot be written", Positionless: true},
	{Code: E0617, Severity: SeverityError, Title: "this thema was built without the playground", Positionless: true},
	{Code: E0618, Severity: SeverityError, Title: "the server cannot listen on the address", Positionless: true},
	{Code: E0620, Severity: SeverityError, Title: "a flag or argument is not valid", Positionless: true},
	{Code: E0701, Severity: SeverityError, Title: "generating code from the schema failed", Positionless: true},
	{Code: E0801, Severity: SeverityError, Title: "the playground request is malformed", Positionless: true},
	{Code: E0802, Severity: SeverityError, Title: "the playground request gives the lineage in neither form, or in both", Positionless: true},
	{Code: E0803, Severity: SeverityError, Title: "a version in the playground request is not a version", Positionless: true},
	{Code: E0901, Severity: SeverityError, Title: "thema failed internally", Positionless: true},
	{Code: E0902, Severity: SeverityError, Title: "CUE reported an error thema does not recognize", Positionless: true},
	{Code: E0903, Severity: SeverityError, Title: "thema reported an error that has no code", Positionless: true},
}

// The codes.
const (
	// E01xx below E0120 are the shape of authoring files, from the compiler.
	E0101 Code = "E0101" // a file that is not <major>.<minor>.thema
	E0102 Code = "E0102" // a version file CUE cannot parse
	E0103 Code = "E0103" // a package clause
	E0104 Code = "E0104" // an import
	E0105 Code = "E0105" // a field other than schema, ForwardLens, BackwardLens
	E0106 Code = "E0106" // one of those declared twice
	E0107 Code = "E0107" // a lens block that is not a struct
	E0108 Code = "E0108" // no schema field
	E0109 Code = "E0109" // CUE's a: b: c shorthand
	E0110 Code = "E0110" // no version files at all
	E0111 Code = "E0111" // a lens block not shaped {v<from>: _, v<to>: {...}, lacunas?: {...}}
	E0112 Code = "E0112" // a schema that fails to evaluate on its own

	E0120 Code = "E0120" // the lineage lacks name or schemas
	E0121 Code = "E0121" // name or schemas is not concrete
	E0122 Code = "E0122" // schemas declares nothing
	E0123 Code = "E0123" // a field thema does not define, at the lineage, a schema entry or a lens
	E0124 Code = "E0124" // a schemas key that is not a version
	E0125 Code = "E0125" // a lenses key that is not an edge
	E0126 Code = "E0126" // not an instance of thema.Lineage: CUE's reasons are notes
	E0128 Code = "E0128" // no value at the path the caller gave
	E0129 Code = "E0129" // a schema is not struct-kinded
	E0130 Code = "E0130" // joinSchema is neither a struct nor _
	E0131 Code = "E0131" // an example fails its schema
	E0132 Code = "E0132" // a comprehension in a schema
	E0133 Code = "E0133" // a reference cycle within a schema
	E0134 Code = "E0134" // a reference from a schema to a value containing it
	E0135 Code = "E0135" // a reference chain deeper than thema follows
	E0137 Code = "E0137" // name fails the identifier pattern
	E0140 Code = "E0140" // ref-sibling-schema
	E0141 Code = "E0141" // ref-lineage-meta
	E0142 Code = "E0142" // ref-outside-lineage
	E0143 Code = "E0143" // ref-import
	E0144 Code = "E0144" // ref-lens-to-schema
	E0145 Code = "E0145" // ref-lens-to-lens
	E0146 Code = "E0146" // ref-unresolved

	E0201 Code = "E0201" // first version is not 0.0
	E0202 Code = "E0202" // a version is neither the next minor nor the next major
	E0203 Code = "E0203" // a major version key with no minor versions
	E0204 Code = "E0204" // two version files, one version
	E0205 Code = "E0205" // version files that skip a version
	E0206 Code = "E0206" // N.0.thema whose schema is compatible with its predecessor
	E0208 Code = "E0208" // a .0 version that is compatible with its predecessor
	// E0210 to E0218: one per rule of internal/compat that finds an
	// incompatibility; E0230 to E0234: one per rule that leaves the verdict
	// Unknown, which thema treats as incompatible.
	E0210 Code = "E0210" // field-added-required
	E0211 Code = "E0211" // field-removed
	E0212 Code = "E0212" // field-now-required
	E0213 Code = "E0213" // default-removed
	E0214 Code = "E0214" // default-changed
	E0215 Code = "E0215" // value-narrowed
	E0216 Code = "E0216" // length-narrowed
	E0217 Code = "E0217" // labels-narrowed
	E0218 Code = "E0218" // validator-added
	E0230 Code = "E0230" // unknown-crossfield
	E0232 Code = "E0232" // unknown-validator
	E0233 Code = "E0233" // unknown-disjunction
	E0234 Code = "E0234" // unknown-patterns
	E0240 Code = "E0240" // no schema with the requested version
	E0242 Code = "E0242" // IsAppendOnly: a schema was deleted
	E0243 Code = "E0243" // IsAppendOnly: a schema was changed

	E0301 Code = "E0301" // a required lens is missing
	E0302 Code = "E0302" // a lens no edge requires
	E0303 Code = "E0303" // input narrower than the source schema
	// E0304 is the data-independent check of a lens's result, and E0307 and
	// E0308 arise when translating an instance. Every lens is proven total at
	// bind, which rules all three out; they remain as the fallbacks that
	// report a checker bug (Internal in Codes).
	E0304 Code = "E0304" // result not an instance of the target schema, data-independently
	E0305 Code = "E0305" // result writes fields the target does not declare
	E0307 Code = "E0307" // the lens's result for this instance is not concrete
	E0308 Code = "E0308" // the lens failed to evaluate for this instance
	E0310 Code = "E0310" // lens lacks its parameter field
	E0311 Code = "E0311" // lens lacks its output field
	// E0330 to E0356: the lens language, from the compiler.
	E0330 Code = "E0330" // body holds an embedding, comprehension, let or other non-mapping
	E0331 Code = "E0331" // mapping key is not a plain name
	E0332 Code = "E0332" // mapping has an attribute
	E0333 Code = "E0333" // hidden field or definition as a key in a lens body or its lacunas
	E0334 Code = "E0334" // nested body embeds something other than input.<path>
	E0335 Code = "E0335" // orelse arguments
	E0336 Code = "E0336" // match arguments
	E0337 Code = "E0337" // each arguments
	E0338 Code = "E0338" // match arms shape
	E0340 Code = "E0340" // source field undeclared
	E0341 Code = "E0341" // target field undeclared
	E0342 Code = "E0342" // drop of a required target field
	E0343 Code = "E0343" // optional source to required target, no fallback
	E0344 Code = "E0344" // constant not an instance of the target field
	E0345 Code = "E0345" // target field mapped twice
	E0346 Code = "E0346" // required target field unproduced
	E0347 Code = "E0347" // source field neither mapped nor dropped
	E0348 Code = "E0348" // orelse on an always-present field
	E0349 Code = "E0349" // orelse fallback optional
	E0350 Code = "E0350" // match has two arms for a value
	E0351 Code = "E0351" // match not exhaustive
	E0352 Code = "E0352" // match passthrough target not self-contained
	E0353 Code = "E0353" // each over a non-list, a fixed-length list, or nested
	E0354 Code = "E0354" // a lens in the first version
	E0355 Code = "E0355" // a ForwardLens in a minor version
	E0356 Code = "E0356" // normal form Decompile cannot express
	E0357 Code = "E0357" // call to a function outside the lens language
	// E0360 to E0372: one per rule of internal/lenscheck that refutes totality;
	// E0380 to E0390: one per rule that leaves the verdict Unknown.
	E0360 Code = "E0360" // copy-narrowed
	E0361 Code = "E0361" // source-absent
	E0362 Code = "E0362" // source-undeclared
	E0364 Code = "E0364" // pattern-absent
	E0365 Code = "E0365" // not-a-value
	E0366 Code = "E0366" // literal-not-instance
	E0367 Code = "E0367" // computed-narrowed
	E0368 Code = "E0368" // required-not-written
	E0369 Code = "E0369" // write-conflict
	E0370 Code = "E0370" // arith-overflow
	E0371 Code = "E0371" // division-by-zero
	E0372 Code = "E0372" // table-key-missing
	E0380 Code = "E0380" // unknown-copy
	E0381 Code = "E0381" // unknown-pattern
	E0382 Code = "E0382" // unknown-exhaustive
	E0383 Code = "E0383" // unknown-op
	E0384 Code = "E0384" // unknown-dynamic
	E0385 Code = "E0385" // unknown-reference
	E0386 Code = "E0386" // unknown-bounds
	E0387 Code = "E0387" // unknown-divisor
	E0388 Code = "E0388" // unknown-target-constraint
	E0389 Code = "E0389" // unknown-table-key
	E0390 Code = "E0390" // unknown-head

	E0401 Code = "E0401" // lacunas listed conditionally
	E0402 Code = "E0402" // lacuna without a message
	E0403 Code = "E0403" // lacuna without a type
	E0404 Code = "E0404" // lacuna field path not declared by the schema
	E0405 Code = "E0405" // lacuna field path not a concrete string
	E0406 Code = "E0406" // lacuna type not in LacunaTypes
	E0407 Code = "E0407" // a field thema does not define on a lacuna
	// E0410 to E0421: lacunas in the authoring form, from the compiler.
	E0410 Code = "E0410" // lacunas shape
	E0411 Code = "E0411" // lacuna type unknown
	E0412 Code = "E0412" // lacunas key without a mapping
	E0413 Code = "E0413" // lacuna on a mapping inside each
	E0414 Code = "E0414" // match catch-all arm without a lacuna
	E0415 Code = "E0415" // lacuna on each without when
	E0416 Code = "E0416" // drop with a lacuna that is not its DroppedField refinement
	E0418 Code = "E0418" // when refers to an undefined name
	E0420 Code = "E0420" // lacuna on a match without a catch-all, and without when
	E0421 Code = "E0421" // lacuna on a drop inside each with a when
	E0422 Code = "E0422" // when names a non-boolean field bare
	E0423 Code = "E0423" // when tests presence redundantly

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
	// E0507: the data document is not valid JSON or YAML.
	E0507 Code = "E0507"
	// E0508: no schema in the lineage accepts the data (validate-any).
	E0508 Code = "E0508"
	// E06xx: loading, modules and the CLI's inputs.
	E0601 Code = "E0601" // lineage path missing or unreadable
	E0602 Code = "E0602" // no CUE package at the path
	E0603 Code = "E0603" // lineages in several packages
	E0604 Code = "E0604" // -p is not a CUE path
	E0605 Code = "E0605" // nothing at -p
	E0606 Code = "E0606" // cue.mod/module.cue invalid
	E0607 Code = "E0607" // thema module version skew
	E0608 Code = "E0608" // import of a package not available (the playground)
	E0609 Code = "E0609" // CUE's loader failed
	E0611 Code = "E0611" // data format unrecognized
	E0612 Code = "E0612" // data format flag vs extension
	E0613 Code = "E0613" // no data on stdin or path
	E0614 Code = "E0614" // unknown format flag
	E0616 Code = "E0616" // writing a file failed
	E0617 Code = "E0617" // serve playground in a build without it
	E0618 Code = "E0618" // serve cannot listen
	E0620 Code = "E0620" // a CLI flag or argument that is not valid

	// E07xx: code generation.
	E0701 Code = "E0701" // a generator failed on the schema

	// E08xx: the playground's requests.
	E0801 Code = "E0801" // request JSON malformed, or no such operation
	E0802 Code = "E0802" // lineage in neither form, or both
	E0803 Code = "E0803" // a request's version does not parse

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
