package thema

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/contenthash"
)

// A VersionID is the identity of a schema version, computed from its content:
// the SHA-256 digest of the canonical form of its schema (Schema), and that
// of the lenses on the edge into it (Lens), the forward lens from its
// predecessor and the backward lens to it. Its String form is
// <schema>:<lens>, each digest as 64 lowercase hex characters.
//
// The schema digest covers the schema as instances are validated against it,
// joined with the lineage's joinSchema; the lens digest covers what each lens
// takes, returns and reports as lacunas. Neither covers the version's number,
// its examples, its comments (including the doc comments that become
// descriptions), or how its files are laid out. So the same schema and lenses
// have the same ID at any version number, and an edit that changes only
// formatting or comments leaves it unchanged. A version whose lens is fixed
// keeps its schema digest and gets a new lens digest.
//
// The ID is never written into the files it describes. What a version's ID
// was when it was published is read from the published lineage.
type VersionID = contenthash.ID

// A Digest is one half of a [VersionID]: a SHA-256 digest of a canonical form.
// Its String form is 64 lowercase hex characters.
type Digest = contenthash.Digest

// ID returns the identity of the schema's version. See [VersionID].
func (sch *schemaDef) ID() (VersionID, error) {
	id, err := contenthash.VersionID(sch.lin.raw, contenthash.Version(sch.v))
	if err != nil {
		return id, diag.InternalErr(fmt.Errorf("the identity of %s: %w", sch.v, err))
	}
	return id, nil
}

// CheckAppendOnly checks that candidate, a lineage's next revision, only
// appends to published, the revision of it that was published: that each
// version published stays as it was published, and that only new versions
// are added. It returns nil when candidate only appends, and otherwise a
// diagnostic for each violation, all of them, positioned in candidate's files
// where they have a position:
//
//   - E0250: a published version's schema was changed;
//   - E0251: a published version's lens was changed (one diagnostic for each
//     lens on the edge into it whose content differs);
//   - E0252: a published version was removed;
//   - E0253: a new version comes before the newest published version, rather
//     than after it;
//   - E0254: the lineage's name was changed.
//
// A version is the same in both when it has the same number and the same
// [VersionID]: what changes it is what changes its ID, and nothing else.
// published is the record. It is a lineage, read from where published
// lineages are kept, such as a registry's store or history/ at the git
// revision that was released, never a hash stored beside it.
//
// Only versions that were cut are compared. A provisional version, the tip of
// a lineage bound on the authoring form's development path, is ignored on
// either side; bind both lineages from history/ alone, as
// load.AuthoringLineage does, to compare what is published.
func CheckAppendOnly(published, candidate Lineage) error {
	isValidLineage(published)
	isValidLineage(candidate)
	pub, cand := published.(*baseLineage), candidate.(*baseLineage)

	var errs []error
	if pub.name != cand.name {
		errs = append(errs, diag.New(diag.E0254).AtValue(cand.raw.LookupPath(cue.MakePath(cue.Str("name")))).
			Addr(diag.Address{Kind: diag.AddrName}).
			Headlinef("the lineage is named %s, but was published as %s", cand.name, pub.name).
			Note(token.NoPos, "a lineage's name is part of what was published: what refers to it names it").
			Helpf("name it %s, or publish it as a new lineage", pub.name).
			Err())
	}

	pubv, candv := pub.cutSchemas(), cand.cutSchemas()
	onRecord := make(map[SyntacticVersion]bool, len(pubv))
	for _, sch := range pubv {
		onRecord[sch.v] = true
	}
	byVersion := make(map[SyntacticVersion]*schemaDef, len(candv))
	for _, sch := range candv {
		byVersion[sch.v] = sch
	}
	for _, ps := range pubv {
		cs, ok := byVersion[ps.v]
		if !ok {
			errs = append(errs, diag.New(diag.E0252).
				Headlinef("the lineage removes published version %s", ps.v).
				Notef(token.NoPos, "%s was published, so instances of it may exist", pub.vocab.Term(schemaAddr(ps.v))).
				Helpf("restore %s as it was published; a published version is never removed", pub.vocab.Term(schemaAddr(ps.v))).
				Err())
			continue
		}
		violations, err := cand.changes(ps, cs)
		if err != nil {
			return err
		}
		errs = append(errs, violations...)
	}

	if len(pubv) > 0 {
		newest := pubv[len(pubv)-1].v
		for _, cs := range candv {
			if !cs.v.Less(newest) || onRecord[cs.v] {
				continue
			}
			a := schemaAddr(cs.v)
			errs = append(errs, diag.New(diag.E0253).AtValue(cs.ref.LookupPath(pathSch)).Addr(a).
				Headlinef("new version %s comes before published version %s", cs.v, newest).
				Notef(token.NoPos, "versions are only appended: a new version comes after the newest published one, %s, so that every published version keeps its predecessor", newest).
				Helpf("remove %s, and make the change in a version after %s", cand.vocab.Term(a), newest).
				Err())
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return diag.Convert(diag.Join(errs...), diag.Context{Vocabulary: cand.vocab})
}

// changes compares cs, a version of the candidate lin, with ps, the same
// version as published, by their identities: a diagnostic for a changed
// schema, and one for each changed lens on the edge into the version.
func (lin *baseLineage) changes(ps, cs *schemaDef) ([]error, error) {
	pid, err := ps.ID()
	if err != nil {
		return nil, err
	}
	cid, err := cs.ID()
	if err != nil {
		return nil, err
	}
	var errs []error
	if pid.Schema != cid.Schema {
		a := schemaAddr(cs.v)
		errs = append(errs, diag.New(diag.E0250).AtValue(cs.ref.LookupPath(pathSch)).Addr(a).
			Headlinef("%s is not as published", lin.vocab.Term(a)).
			Note(token.NoPos, "a published version never changes: instances were validated against it as it was published").
			Helpf("restore %s as it was published, and make the change in a new version", lin.vocab.Term(a)).
			Err())
	}
	if pid.Lens == cid.Lens {
		return errs, nil
	}
	pf, pb, err := contenthash.LensForms(ps.lin.raw, contenthash.Version(ps.v))
	if err != nil {
		return nil, diag.InternalErr(fmt.Errorf("the lenses into %s: %w", ps.v, err))
	}
	cf, cb, err := contenthash.LensForms(lin.raw, contenthash.Version(cs.v))
	if err != nil {
		return nil, diag.InternalErr(fmt.Errorf("the lenses into %s: %w", cs.v, err))
	}
	pred := lin.allv[searchSynv(lin.allv, cs.v)-1]
	var changed []lensID
	if pf != cf {
		changed = append(changed, lid(pred, cs.v))
	}
	if pb != cb {
		changed = append(changed, lid(cs.v, pred))
	}
	if len(changed) == 0 {
		return nil, diag.InternalErr(fmt.Errorf("the lens digest of %s differs from the published one, but none of its lenses does", cs.v))
	}
	for _, id := range changed {
		a := lensAddr(id.From, id.To)
		errs = append(errs, diag.New(diag.E0251).AtValue(lin.uni.LookupPath(cue.MakePath(cue.Str("lenses"), cue.Str(id.String())))).Addr(a).
			Headlinef("%s is not as published", lin.vocab.Term(a)).
			Note(token.NoPos, "a version's lenses are part of it: instances already translated through the published lens would translate differently").
			Helpf("restore %s as it was published", lin.vocab.Term(a)).
			Err())
	}
	return errs, nil
}

// cutSchemas returns the lineage's schemas without its provisional version,
// if it has one: the versions that were cut.
func (lin *baseLineage) cutSchemas() []*schemaDef {
	if lin.provisional != nil {
		return lin.allsch[:len(lin.allsch)-1]
	}
	return lin.allsch
}
