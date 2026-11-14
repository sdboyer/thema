// Package contenthash computes the content identity of any CUE value, by the
// rules thema computes a schema version's identity with ([thema.VersionID]):
// a SHA-256 digest of the value's canonical form, tagged with the generation
// of that form.
//
// It is for tools that build on lineages and need identities for what they
// build, such as a structure that refers to lineage versions by their IDs.
// Such a structure is hashed as a value of its own: write each version's ID
// into it, as data, and take its digest with [Of]. A lineage version's own
// identity is [thema.Schema.ID], never Of applied to the lineage.
//
// The canonical form is the one a version's schema digest is taken over, so
// what holds for a schema digest holds for these digests:
//
//   - Comments, positions, layout, the order of fields and declarations, and
//     the names of lets don't change the canonical form.
//   - Two values with different meanings never have the same canonical form.
//     Two with the same meaning can still differ: wherever CUE presents them
//     differently and no rewrite undoes that, their forms differ.
//   - A reference to an imported package is written by its import path, and
//     a reference into thema's own CUE by its name, not its content.
//
// A Digest compares equal to another only when both are of the same
// generation and both are certified, or both not: see [Digest]. Generation
// is bumped whenever the canonical form's output changes, so a digest stored
// under one generation is recomputed, not compared, under another.
package contenthash

import (
	"cuelang.org/go/cue"

	"github.com/sdboyer/thema/internal/contenthash"
)

// A Digest is a SHA-256 digest of a canonical form, with the generation of
// that form and whether it was computed with the CUE the generation is
// certified against. Its String form is the tag, a dot and 64 lowercase hex
// characters: g2.<hex>, or g2u.<hex> when uncertified. It is the same type as
// [thema.Digest], the halves of a [thema.VersionID].
type Digest = contenthash.Digest

// Generation is the generation of the canonical form this build of thema
// computes. It is bumped whenever the form's output changes for any reason,
// and every digest of an earlier generation is then unequal to any computed
// now.
const Generation = contenthash.Generation

// Form returns the canonical form of v, the text its digest is taken over. It
// is for telling why two digests differ; compare digests, not forms.
//
// v is hashed as it is given. A value that holds a lineage holds what thema
// contributes to it too, as a bound lineage does; Form doesn't set that
// aside, as [thema.Schema.ID] does. It is an error for v to hold an error.
func Form(v cue.Value) (string, error) {
	return contenthash.Form(v)
}

// Of returns the digest of v's canonical form, [Form].
func Of(v cue.Value) (Digest, error) {
	form, err := contenthash.Form(v)
	if err != nil {
		return Digest{}, err
	}
	return contenthash.Sum(form), nil
}
