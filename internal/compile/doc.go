// Package compile compiles thema's authoring form to its normal form, the
// thema.Lineage struct, and decompiles the normal form back.
//
// # The authoring form
//
// A lineage is a directory, whose base name is the lineage's name, holding one
// file per version: <major>.<minor>.thema, at the top of the directory or
// under history/. Each file is CUE syntax with at most three fields:
//
//	schema: {...}    // the version's schema
//	forward: {...}   // mappings from the previous version to this one
//	backward: {...}  // mappings from this version to the previous one
//
// A lens body is a struct shaped like its target: each entry is a target
// field, and its value says where the field comes from. The vocabulary is
//
//	after: input.before           // a move: a reference into the source
//	kind:  "v2"                   // a constant
//	meta: {owner: input.name}     // a nested body; {input.m, x: 1} copies from m
//	legacy: drop                  // a drop, keyed by the source field
//
// A field both schemas declare, that no entry mentions, is copied. A reference
// to an optional source field is guarded. A field the forward direction's
// source has and its target lacks must be mapped or dropped. A required target
// field must be produced. The backward lens is derived from the forward lens
// (the inverse of each single-use move, and a drop of each field the version
// added, with a derived lacuna); what the author writes in backward overrides
// it. A minor version, which the compatibility check derives, has no forward
// lens and its file is usually just its schema.
//
// A lacuna attaches to the mapping it describes: x: e @lacuna(Type, "message").
// On a drop, @lacuna("message") refines the derived lacuna's message.
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
