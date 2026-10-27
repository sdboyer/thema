// Package diag is thema's diagnostic model: every error thema reports, from
// compiling authoring files through binding, validating data and translating
// it, is a [Diagnostic] with a stable [Code], a headline in the author's own
// vocabulary, the author's position as its primary position, and notes and
// help that say why and what to do.
//
// # Authoring principles
//
// These are binding, and tests enforce the ones marked so. They exist because
// of concrete past failures: munging that silently dropped CUE errors it did
// not recognize, messages written in the vocabulary of thema's normal form or
// of its own CUE rather than of what the author wrote, positions pointing into
// thema's CUE instead of the author's file, and CUE's own wording interpolated
// into thema's messages so that every CUE upgrade changed them.
//
//  1. Author's vocabulary only (lint). A headline, note or help uses the terms
//     of the form the author wrote: for authoring files, version file, schema,
//     ForwardLens, BackwardLens, mapping, lacunas, drop/orelse/match/each and
//     v0_0; for the normal form, schemas, lenses, input, result and lacunas.
//     Never the kernel's or the pipeline's: _accum, steps, Translate.out,
//     Lineage., or a path into thema's own CUE. A [Vocabulary] renders an
//     [Address] into the right terms, so one message template serves both.
//  2. Never embed CUE's text (lint). CUE's own message rides along as a note or
//     in Detail, verbatim, never interpolated into a headline. Positions come
//     from the cue/errors values, never from parsing cue/errors.Details.
//  3. Nothing dropped (test). Every entry of cue/errors.Errors(err) becomes
//     the headline or a note of some Diagnostic. An unrecognized shape becomes
//     a generic-code Diagnostic carrying CUE's text verbatim.
//  4. The primary position is the author's (golden). When CUE offers no
//     user-origin position, [From] walks the author's value along the error
//     path for one; only then is there no primary, and thema's positions are
//     notes. A kernel-origin primary fails the reproducer tests.
//  5. Headline = rule + violator. The headline names the invariant in the
//     author's terms and the construct that breaks it. A note says why. Help
//     says what to do.
//  6. Mechanical style (lint). No trailing period; a lowercase first word
//     unless it is an identifier; no Go formatting leakage; no file:line:col
//     inside text (positions are structured); versions as 0.1, never [0 1].
//  7. One code, one reproducer (lockstep test). No [Code] exists without a
//     txtar case under diag/testdata/codes that renders the full diagnostic,
//     and docs/errors/<code>.md is generated from it.
//  8. Compatibility findings are mapped from their rule, never re-derived.
package diag
