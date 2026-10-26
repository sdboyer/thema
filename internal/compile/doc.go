// Package compile compiles thema's authoring form to its normal form, the
// thema.Lineage struct, and decompiles the normal form back.
//
// # The authoring form
//
// A lineage is a directory, whose base name is the lineage's name, holding one
// file per version: <major>.<minor>.thema, at the top of the directory or
// under history/. Each file is CUE syntax with at most three fields:
//
//	schema: {...}
//	ForwardLens:  {v0_0: _, v1_0: {...}}   // from the previous version to this one
//	BackwardLens: {v1_0: _, v0_0: {...}}   // from this version to the previous one
//
// A lens reads like a function: the field whose value is _ declares the
// instance it takes, named by its version, and the other field is the body
// producing the instance it returns, shaped like that version's schema. Each
// entry of the body is a target field, and its value says where the field
// comes from. The vocabulary is
//
//	after: v0_0.before                      // a move: a reference into the instance
//	kind:  "v2"                             // a constant
//	meta: {owner: v0_0.name}                // a nested body; {v0_0.m, x: 1} copies from m
//	legacy: drop                            // a drop, keyed by the source field
//	nick:  orelse(v0_0.nick, v0_0.name)     // an optional source: the fallback when absent
//	kind:  remap(v0_0.kind, {"a": 1, [_]: 0 @lacuna(...)})  // a value table, with an optional catch-all
//	items: each(v0_0.items, {name: item.title})            // a list, mapped element by element
//
// A field both schemas declare, that no entry mentions, is copied. A reference
// to an optional source field is guarded; where the target requires the
// field, orelse says what to use instead. A remap's arms map keys to constants;
// a value no arm keys passes through where the target accepts it, else the
// [_] arm applies, which must carry a lacuna; without one, every source value
// must be keyed or accepted. An each body is a lens body over item. A field
// the forward direction's source has and its target lacks must be mapped or
// dropped. A required target field must be produced. The backward lens is
// derived from the forward lens (the inverse of each single-use move, and a
// drop of each field the version added, with a derived lacuna); what the
// author writes in BackwardLens overrides it. orelse, remap and each have no
// derived inverse. A minor version, which the compatibility check derives,
// has no forward lens and its file is usually just its schema.
//
// A lacuna attaches to the mapping it describes: x: e @lacuna(Type, "message").
// On an orelse it applies when the fallback is used; on a remap it sits on
// an arm and applies when the arm does. On a drop, @lacuna("message") refines
// the derived lacuna's message. Computed expressions are not accepted yet;
// orelse over a computed primary and the curated builtin table are next.
//
// # Layout
//
// Format lays authoring text out in thema's style: every struct keeps its
// braces and every field has its own line. CUE has no normal form and this
// is not one; it is the one layout Decompile produces, so that decompiling a
// formatted file gives back its text.
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
