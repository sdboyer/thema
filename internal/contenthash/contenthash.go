// Package contenthash computes the identity of a schema version in a lineage
// from its content: two SHA-256 digests, of its schema and of the lenses on
// the edge into it, each taken over a canonical form of that content.
//
// A version's ID is written <schema>:<lens>, each digest as 64 lowercase hex
// characters. Hex never contains the separator, so the ID splits unambiguously,
// and the digest is the bare hash, with no algorithm prefix (sha256:...) whose
// colon would collide with it.
//
// The schema digest covers what the schema means for the instances validated
// against it: the schema unified with the lineage's joinSchema. Not the
// version number (a version's position, which cutting a tip may change), nor
// the version's examples (documentation). Promoting an instance validated
// against a tip compares only this digest.
//
// The lens digest covers the lenses on the edge into the version: forward,
// from its predecessor, and backward, to it. It covers each lens's input,
// result and lacunas, not its from and to, which are again position. Fixing a
// published lens changes the lens digest and leaves the schema digest alone.
// Only lenses that exist are hashed: a minor version's forward lens is
// implicit, so its edge holds only the backward lens. The first version has no
// predecessor and no lenses; its lens digest is that of the empty edge, whose
// canonical form is {}: the constant sha256("{}"), computed like any other.
//
// Lenses are hashed from their syntax, not their evaluated value: lenses
// aren't concrete, and what they mean is the expressions they map input with.
// A lineage whose lenses are written in Go has no lens syntax to hash; this
// package sees only its CUE.
//
// The lineage is the lineage as its author wrote it, not unified with
// thema.Lineage, whose kernel constraints would otherwise be hashed with every
// lens. Only normalform.go knows where in it a version's schema and lenses are.
//
// What the canonical form is, and what it does and doesn't make equal, is
// described in canonical.go and pinned by the tests and the golden digests in
// testdata. Changing it changes stored identities: see the contenthash seam.
package contenthash

import (
	"crypto/sha256"
	"encoding/hex"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"

	"github.com/sdboyer/thema/internal/cuetil"
)

// Digest is a SHA-256 digest of a canonical form.
type Digest [sha256.Size]byte

// String returns d as 64 lowercase hex characters.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// ID is the identity of a schema version: the digests of its schema and of the
// lenses on the edge into it.
type ID struct {
	Schema, Lens Digest
}

// String returns id as <schema>:<lens>.
func (id ID) String() string {
	return id.Schema.String() + ":" + id.Lens.String()
}

// VersionID returns the identity of version v of the lineage lin.
func VersionID(lin cue.Value, v Version) (ID, error) {
	var id ID
	sch, err := SchemaForm(lin, v)
	if err != nil {
		return id, err
	}
	lens, err := LensForm(lin, v)
	if err != nil {
		return id, err
	}
	return ID{Schema: sha256.Sum256([]byte(sch)), Lens: sha256.Sum256([]byte(lens))}, nil
}

// SchemaForm returns the canonical form of version v's schema in the lineage
// lin, whose digest is the schema part of v's ID.
func SchemaForm(lin cue.Value, v Version) (string, error) {
	sch, err := versionSchema(lin, v)
	if err != nil {
		return "", err
	}
	x, err := export(sch)
	if err != nil {
		return "", err
	}
	return canonicalize(x)
}

// LensForm returns the canonical form of the lenses on the edge into version v
// of the lineage lin, whose digest is the lens part of v's ID: a struct with a
// forward and a backward field for the lenses there are, each holding the
// lens's source, result and lacunas fields.
func LensForm(lin cue.Value, v Version) (string, error) {
	fwd, back, err := versionLenses(lin, v)
	if err != nil {
		return "", err
	}
	edge := &ast.StructLit{}
	for _, l := range []struct {
		name string
		edgeLens
	}{{"forward", fwd}, {"backward", back}} {
		if !l.lens.Exists() {
			continue
		}
		x, err := lensSyntax(l.lens, l.from, l.to)
		if err != nil {
			return "", err
		}
		edge.Elts = append(edge.Elts, &ast.Field{Label: ast.NewIdent(l.name), Value: x})
	}
	return canonicalize(edge)
}

// lensSyntax returns the exported content fields of the lens from version from
// to version to, as a struct: its source field, its result field (both named
// by their version, cuetil.LensParam) and its lacunas. The edge is the lens's
// position, which, like a schema's version, is not hashed. Each field is
// exported on its own, so a reference to the source is one that leaves the
// field, bound in a let CUE generates (let V0_0 = _).
func lensSyntax(lens cue.Value, from, to Version) (ast.Expr, error) {
	s := &ast.StructLit{}
	// The fields are named after the edge's versions, which is the lens's
	// position; the canonical form labels them by role instead.
	for _, f := range []struct{ name, label string }{
		{cuetil.LensParam(from[0], from[1]), "takes"},
		{cuetil.LensParam(to[0], to[1]), "returns"},
		{"lacunas", "lacunas"},
	} {
		fv := lens.LookupPath(cue.MakePath(cue.Str(f.name)))
		if !fv.Exists() {
			continue
		}
		x, err := export(fv)
		if err != nil {
			return nil, err
		}
		s.Elts = append(s.Elts, &ast.Field{Label: ast.NewIdent(f.label), Value: x})
	}
	return s, nil
}
