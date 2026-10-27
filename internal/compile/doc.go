// Package compile compiles thema's authoring form to its normal form, the
// thema.Lineage struct, and decompiles the normal form back.
//
// # The authoring form
//
// A lineage is a directory, whose base name is the lineage's name, holding one
// file per version: <major>.<minor>.thema, at the top of the directory or
// under history/. Each file is CUE syntax with at most three fields:
//
//	Schema: {...}
//	ForwardLens:  {v0_0: _, v1_0: {...}, lacunas: {...}}   // from the previous version to this one
//	BackwardLens: {v1_0: _, v0_0: {...}, lacunas: {...}}   // from this version to the previous one
//
// A lens reads like a function: the field whose value is _ declares the
// instance it takes, named by its version, and the other version field is the
// body producing the instance it returns, shaped like that version's schema.
// Each entry of the body is a target field, and its value says where the
// field comes from. The vocabulary is
//
//	after: v0_0.before                      // a move: a reference into the instance
//	kind:  "v2"                             // a constant
//	area:  v0_0.w * v0_0.h                  // a computed value
//	meta: {owner: v0_0.name}                // a nested body; {v0_0.m, x: 1} copies from m
//	legacy: drop                            // a drop, keyed by the source field
//	nick:  orelse(v0_0.nick, v0_0.name)     // an optional source: the fallback when absent
//	kind:  match(v0_0.kind, {"a": 1, [_]: 0})      // a match on the value, with an optional [_] arm
//	items: each(v0_0.items, {name: item.title})    // a list, mapped element by element
//
// A field both schemas declare, that no entry mentions, is copied. A reference
// to an optional source field is copied only when present; where the target
// requires the field, orelse says what to use instead. A match's arms map
// values to constants; a value no arm matches passes through where the target
// accepts it, else the [_] arm applies, which must have a lacuna; without one,
// the arms must be exhaustive: every source value matched or accepted. An each body is a lens body over item. A field
// the forward direction's source has and its target lacks must be mapped or
// dropped. A required target field must be produced. The backward lens is
// derived from the forward lens (the inverse of each single-use move, and a
// drop of each field the version added, with a derived lacuna); what the
// author writes in BackwardLens overrides it. orelse, match and each have no
// derived inverse. A minor version, which the compatibility check derives,
// has no forward lens and its file is usually just its schema.
//
// The optional lacunas field declares what the body's mappings lose. It is
// keyed like the body, and each key lists the lacunas of that mapping:
//
//	lacunas: {
//		count: [{type: Placeholder, message: "count was absent; 0 is a placeholder"}]
//		meta: {team: [{type: LossyFieldMapping, message: "...", when: len(v0_0.name) > 64}]}
//	}
//
// Without a when, a lacuna applies under the condition its mapping implies:
// always, for a move or a constant; when the fallback is used, for an orelse;
// when the [_] arm applies, for a match; when the field is present, for a
// drop. A DroppedField lacuna on a drop refines the derived one's message. A
// when replaces the implied condition. It compares and combines source
// fields with CUE's operators, len and the integer divisions (quo, rem, div,
// mod): what the totality proof proves a boolean. A when that reads an
// optional field concerns only the instances that have it; an optional field
// named bare, when: v0_0.x, tests its presence, and a required bool named
// bare is its value. Inside an each body, only the DroppedField refinement is
// supported yet, keyed through the list (items: {legacy: [...]}).
//
// A computed value is built from source fields with CUE's operators, len,
// the integer divisions and the strings functions the totality proof proves
// (lenscheck.Builtins and lenscheck.StringFuncs, the one list of each); the
// file needs no import for strings. A computed value that reads optional
// fields is written only where they are present, so a target field that
// requires it takes orelse(<expression>, fallback), which uses the fallback
// where any of them is absent.
//
// # Layout
//
// Format lays authoring text out in thema's style: every struct keeps its
// braces and every field has its own line. CUE has no normal form and this
// is not one; it is the one layout Decompile produces, so that decompiling a
// formatted file gives back its text. A version file's three fields are
// written with their braces: CUE's struct shorthand, Schema: a: int, is
// refused on them, and expanded by Format beneath them.
//
// # Positions
//
// The compiler builds CUE AST, never text, and positions every node in the
// authoring file it came from, so that the evaluator and thema's checks
// report the author's line. Nodes with no text of their own (implicit copies,
// derived lacunas, the normal form's scaffolding) are positioned at the nearest
// authored construct. The *_test.go files that pin this behavior predate the
// compiler and are kept: the design depends on it, and they fail loudly if a
// CUE upgrade moves it.
//
// # Decompile
//
// Decompile is Compile's inverse on the canonical subset of the normal form,
// the lineages Compile produces: implicit copies are dropped, derived lacunas
// become drops, and derived backward entries are omitted. A lens outside that
// subset is an error, never an approximation. Every round-trip fixture checks
// both directions.
package compile
