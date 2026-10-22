package thema

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"cuelang.org/go/cue"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
	"github.com/cockroachdb/errors"

	terrors "github.com/sdboyer/thema/errors"
	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/embedreg"
)

// maybeLineage is an intermediate processing structure used to validate
// inputs as actual lineages
//
// it's important that these flags are populated in order to avoid false negatives.
// no system ensures this, it's all human reasoning
type maybeLineage struct {
	// user lineage definition, NOT unified with thema.Lineage
	raw cue.Value

	// user lineage definition, unified with thema.Lineage
	uni cue.Value

	// original input cue.Value representing the lineage. May or may not be unified
	// with thema.Lineage
	orig cue.Value

	rt *Runtime

	// pos of the original input for the lineage
	pos token.Pos

	// bind options passed by the caller
	cfg *bindConfig

	schlist []*schemaDef

	allv []SyntacticVersion

	implens []ImperativeLens

	lensmap map[lensID]ImperativeLens

	// The raw input value is the root of a package instance
	// rawIsPackage bool
}

// to, from
type lensID struct {
	From, To SyntacticVersion
}

func lid(from, to SyntacticVersion) lensID {
	return lensID{from, to}
}

func (id lensID) String() string {
	return fmt.Sprintf("%s -> %s", id.From, id.To)
}

func (ml *maybeLineage) checkGoValidity(cfg *bindConfig) error {
	schiter, err := ml.uni.LookupPath(cue.MakePath(cue.Str("schemas"))).List()
	if err != nil {
		panic(fmt.Sprintf("unreachable - should have already verified schemas field exists and is list: %+v", cerrors.Details(err, nil)))
	}
	vpath := cue.MakePath(cue.Str("version"))

	var previous *schemaDef
	for schiter.Next() {
		sch := &schemaDef{ref: schiter.Value()}
		v, err := schemaVersion(ml.rt.Context(), sch.ref.LookupPath(vpath))
		if err != nil {
			return errors.Mark(cerrors.Newf(userPos(sch.ref.LookupPath(vpath)), "schema %d: %s", len(ml.schlist), err), terrors.ErrInvalidLineage)
		}
		sch.v = v

		if err := ml.checkSchemasOrder(previous, sch); err != nil {
			return err
		}

		sch.def = sch.ref.LookupPath(pathSchDef)
		if previous != nil && !cfg.skipbuggychecks {
			compaterr := compat.ThemaCompatible(ml.rt.Context(), previous.def, sch.def)
			if sch.v[1] == 0 && compaterr == nil {
				// Major version change, should be backwards incompatible
				return errors.Mark(mkerror(sch.ref.LookupPath(pathSch), "schema %s must be backwards incompatible with schema %s: introduce a breaking change, or redeclare as version %s", sch.v, previous.v, synv(previous.v[0], previous.v[1]+1)), terrors.ErrInvalidLineage)
			}
			if sch.v[1] != 0 && compaterr != nil {
				// Minor version change, should be backwards compatible
				return errors.Mark(mkerror(sch.ref.LookupPath(pathSch), "schema %s is not backwards compatible with schema %s:\n%s", sch.v, previous.v, cerrors.Details(compaterr, nil)), terrors.ErrInvalidLineage)
			}
		}

		ml.schlist = append(ml.schlist, sch)
		ml.allv = append(ml.allv, sch.v)
		previous = sch
	}

	return nil
}

// schemaVersion returns the version declared by a schema entry. A version the
// author wrote must be a single concrete version; a disjunction, even one with a
// default, is rejected rather than resolved. An omitted version takes thema's
// default of 0.0.
//
// An omitted version is recognized by its value, not its source: it is thema's
// own SyntacticVersion | *[0, 0], which alone admits any version. A disjunction
// an author wrote admits only its own alternatives.
func schemaVersion(ctx *cue.Context, v cue.Value) (SyntacticVersion, error) {
	var sv SyntacticVersion
	if !v.Exists() {
		return sv, fmt.Errorf("version is missing")
	}
	if d, hasDefault := v.Default(); hasDefault {
		if v.Unify(ctx.Encode([]uint64{99, 99})).Validate(cue.Concrete(true)) != nil {
			return sv, fmt.Errorf("version must be a single concrete version, such as [0, 1]")
		}
		v = d
	} else if v.Validate(cue.Concrete(true)) != nil {
		return sv, fmt.Errorf("version must be a single concrete version, such as [0, 1]")
	}
	if err := v.Decode(&sv); err != nil {
		return sv, fmt.Errorf("version must be a pair of non-negative integers, such as [0, 1]")
	}
	return sv, nil
}

// authorWrote reports whether any conjunct of v has a position outside the
// embedded thema module's root, i.e. was most likely written by the lineage's
// author. It is a heuristic, for choosing error positions only: when thema's CUE
// is loaded some other way (e.g. as the main module), its positions don't fall
// under that root.
func authorWrote(v cue.Value) bool {
	_, args := v.Expr()
	for _, a := range args {
		if p := a.Pos(); p.IsValid() && !strings.HasPrefix(p.Filename(), embedreg.Root()+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// checkSchemasOrder checks that each schema's version is the exact successor of
// its predecessor's: either the next minor version, or the first (.0) version of
// the next major version. The first schema must be 0.0.
func (ml *maybeLineage) checkSchemasOrder(prev, curr *schemaDef) error {
	if prev == nil {
		if curr.v != synv(0, 0) {
			return errors.Mark(cerrors.Newf(versionPos(curr), "the first schema must be version 0.0, not %s", curr.v), terrors.ErrInvalidSchemasOrder)
		}
		return nil
	}

	nextMinor, nextMajor := synv(prev.v[0], prev.v[1]+1), synv(prev.v[0]+1, 0)
	if curr.v != nextMinor && curr.v != nextMajor {
		return errors.Mark(cerrors.Newf(versionPos(curr), "schema version %s does not follow %s: the next schema must be version %s or %s", curr.v, prev.v, nextMinor, nextMajor), terrors.ErrInvalidSchemasOrder)
	}

	return nil
}

// versionPos returns the position of a schema entry's version as its author wrote
// it, or of the entry itself if the version was omitted.
func versionPos(sch *schemaDef) token.Pos {
	if v := sch.ref.LookupPath(cue.MakePath(cue.Str("version"))); authorWrote(v) {
		return userPos(v)
	}
	return userPos(sch.ref)
}

func (ml *maybeLineage) checkExists(cfg *bindConfig) error {
	p := ml.raw.Path().String()
	// The candidate lineage must exist.
	// TODO can we do any better with contextualizing these errors?
	if !ml.raw.Exists() {
		if p != "" {
			return errors.Mark(errors.Newf("not a lineage: no cue value at path %q", p), terrors.ErrValueNotExist)
		}

		return errors.WithStack(terrors.ErrValueNotExist)
	}
	return nil
}

// checkUnknownFields reports regular fields that thema does not define, at the
// lineage, schema entry and lens levels. thema's CUE rejects these too, but CUE
// positions such errors at the guard in thema's own CUE; reporting them from Go
// points at the author's line instead. Hidden and definition fields are left alone,
// as they are free for the author's own helpers.
//
// The set of known fields is read from thema's CUE, so it cannot drift from it.
func (ml *maybeLineage) checkUnknownFields() error {
	var errs cerrors.Error
	check := func(v cue.Value, def cue.Value, what string) {
		known := make(map[string]bool)
		var names []string
		kiter, err := def.Fields(cue.Optional(true))
		if err != nil {
			panic(fmt.Sprintf("unreachable - thema's own %s must be a struct: %v", what, err))
		}
		for kiter.Next() {
			known[kiter.Selector().Unquoted()] = true
			names = append(names, kiter.Selector().Unquoted())
		}
		sort.Strings(names)

		iter, err := v.Fields(cue.Optional(true))
		if err != nil {
			return
		}
		for iter.Next() {
			sel := iter.Selector()
			if !sel.IsString() || known[sel.Unquoted()] {
				continue
			}
			errs = cerrors.Append(errs, cerrors.Newf(userPos(iter.Value()), "%s is not a field of %s; valid fields are %s", sel.Unquoted(), what, strings.Join(names, ", ")))
		}
	}
	each := func(list cue.Value, known cue.Value, what string) {
		// List() refuses to iterate a list whose elements contain errors, which is
		// exactly the case being checked for, so walk the elements by index.
		for i := 0; ; i++ {
			elem := list.LookupPath(cue.MakePath(cue.Index(i)))
			if !elem.Exists() {
				return
			}
			check(elem, known, what)
		}
	}

	rtv := ml.rt.Underlying()
	check(ml.raw, ml.rt.linDef(), "a lineage")
	each(ml.raw.LookupPath(cue.MakePath(cue.Str("schemas"))), rtv.LookupPath(cue.MakePath(cue.Str("SchemaDef"))), "a schema entry")
	each(ml.raw.LookupPath(cue.MakePath(cue.Str("lenses"))), rtv.LookupPath(cue.MakePath(cue.Str("Lens"))), "a lens")

	if errs != nil {
		return errors.Mark(errs, terrors.ErrInvalidLineage)
	}
	return nil
}

// userPos returns the position of the first conjunct of v that is not in thema's
// own CUE, or v's position if there is none.
func userPos(v cue.Value) token.Pos {
	_, args := v.Expr()
	for _, a := range args {
		if p := a.Pos(); p.IsValid() && !strings.HasPrefix(p.Filename(), embedreg.Root()+string(filepath.Separator)) {
			return p
		}
	}
	return v.Pos()
}

func (ml *maybeLineage) checkLineageShape(cfg *bindConfig) error {
	// Check certain paths specifically, because these are common getting started errors of just arranging
	// CUE statements in the right way that deserve more targeted guidance
	for _, path := range []string{"name", "schemas"} {
		val := ml.raw.LookupPath(cue.MakePath(cue.Str(path)))
		if !val.Exists() {
			return errors.Mark(mkerror(ml.raw, "not a lineage, missing Lineage.%s", path), terrors.ErrValueNotALineage)
		}
		// IsConcrete reports any list as concrete, even an open one, so also require
		// a concrete length.
		_, lenerr := val.Len().Int64()
		if !val.IsConcrete() || (val.IncompleteKind() == cue.ListKind && lenerr != nil) {
			return errors.Mark(mkerror(val, "invalid lineage, Lineage.%s must be concrete", path), terrors.ErrInvalidLineage)
		}
	}

	// The candidate lineage must be an instance of Lineage. However, we can't validate the whole
	// structure, because lenses will fail validation. This is because we currently expect them to be written:
	//
	// {
	// 		input: _
	// 		result: {
	// 			foo: input.foo
	// 		}
	// }
	//
	// means that those structures won't pass Validate until we've injected an actual object there.
	//
	// Nor can it be validated as final: schemas are abstract, and a schema's required
	// (!) fields are legitimately absent until data is supplied. Final validation
	// reports each one as missing.
	if err := ml.uni.Validate(); err != nil {
		return errors.Mark(cerrors.Promote(err, "not an instance of thema.Lineage"), terrors.ErrInvalidLineage)
	}

	return nil
}

// Checks the validity properties of lineages that are expressible natively in CUE.
func (ml *maybeLineage) checkNativeValidity(cfg *bindConfig) error {
	// The candidate lineage must be error-free.
	// TODO replace this with Err, this check isn't actually what we want up here. Only schemas themselves must be cycle-free
	if err := ml.raw.Validate(cue.Concrete(false)); err != nil {
		return errors.Mark(cerrors.Promote(err, "lineage is invalid"), terrors.ErrInvalidLineage)
	}
	if err := ml.uni.Validate(cue.Concrete(false)); err != nil {
		return errors.Mark(cerrors.Promote(err, "lineage is invalid"), terrors.ErrInvalidLineage)
	}

	return nil
}

// checkLensResults statically checks each CUE lens against its schemas, with its
// input standing for any instance of its source schema (closed, so references to
// fields the source can't have are errors). The lens's input must accept every
// instance of the source schema, and its result must be consistent with the
// target schema and build only fields the target declares. This catches at bind
// time what would otherwise fail only when a matching instance is translated.
func (ml *maybeLineage) checkLensResults() error {
	if len(ml.implens) > 0 {
		return nil
	}
	ctx := ml.rt.Context()
	byVersion := make(map[SyntacticVersion]*schemaDef, len(ml.schlist))
	for _, sch := range ml.schlist {
		byVersion[sch.v] = sch
	}
	inputPath, resultPath := cue.MakePath(cue.Str("input")), cue.MakePath(cue.Str("result"))

	var errs cerrors.Error
	lenses := ml.uni.LookupPath(cue.MakePath(cue.Str("lenses")))
	for li := 0; ; li++ {
		lens := lenses.LookupPath(cue.MakePath(cue.Index(li)))
		if !lens.Exists() {
			break
		}
		var from, to SyntacticVersion
		if lens.LookupPath(cue.MakePath(cue.Str("from"))).Decode(&from) != nil || lens.LookupPath(cue.MakePath(cue.Str("to"))).Decode(&to) != nil {
			continue
		}
		fsch, tsch := byVersion[from], byVersion[to]
		if fsch == nil || tsch == nil {
			continue
		}
		fail := func(v cue.Value, format string, args ...any) {
			errs = cerrors.Append(errs, cerrors.Newf(userPos(v), "lens %s -> %s: %s", from, to, fmt.Sprintf(format, args...)))
		}

		source := cuetil.CloseRecursive(ctx, fsch.def)
		input := lens.LookupPath(inputPath)
		if err := input.Subsume(source); err != nil {
			fail(input, "input must accept every instance of schema %s, but is narrower: %s", from, cerrors.Details(err, nil))
			continue
		}

		result := lens.FillPath(inputPath, source).LookupPath(resultPath)
		if err := result.Unify(cuetil.CloseRecursive(ctx, tsch.def)).Validate(); err != nil {
			fail(lens.LookupPath(resultPath), "result is not an instance of schema %s:\n%s", to, cerrors.Details(err, nil))
			continue
		}
		paths := cuetil.UndeclaredBuiltFields(ctx, result, tsch.def)
		// Fields built inside comprehensions only exist once data makes them fire.
		for _, p := range cuetil.ComprehensionFieldPaths(lens.LookupPath(resultPath)) {
			if !cuetil.DeclaresPath(ctx, tsch.def, p) {
				paths = append(paths, p)
			}
		}
		if len(paths) > 0 {
			fail(lens.LookupPath(resultPath), "%s", tsch.undeclaredErr(paths))
		}
	}

	if errs != nil {
		return errors.Mark(errs, terrors.ErrInvalidLens)
	}
	return nil
}

// checkLacunas checks the lacunas each CUE lens can emit: they must be listed
// unconditionally (each lacuna's condition field decides whether it applies),
// have a message and a type, and refer only to fields declared by the lens's
// schemas: source fields by its 'from' schema, target fields by its 'to' schema.
// CUE checks the type and field shapes; the rest needs the lineage's schemas, or
// would only be reported by CUE once data arrives.
func (ml *maybeLineage) checkLacunas() error {
	byVersion := make(map[SyntacticVersion]*schemaDef, len(ml.schlist))
	for _, sch := range ml.schlist {
		byVersion[sch.v] = sch
	}

	var errs cerrors.Error
	lenses := ml.uni.LookupPath(cue.MakePath(cue.Str("lenses")))
	for li := 0; ; li++ {
		lens := lenses.LookupPath(cue.MakePath(cue.Index(li)))
		if !lens.Exists() {
			break
		}
		var from, to SyntacticVersion
		if lens.LookupPath(cue.MakePath(cue.Str("from"))).Decode(&from) != nil || lens.LookupPath(cue.MakePath(cue.Str("to"))).Decode(&to) != nil {
			// Malformed lens versions are other checks' concern.
			continue
		}
		fsch, tsch := byVersion[from], byVersion[to]
		if fsch == nil || tsch == nil {
			continue
		}

		// A lens that declares no lacunas has thema's open default, [...Lacuna], which
		// resolves to []. A list built conditionally has no concrete length at all.
		raw := lens.LookupPath(cue.MakePath(cue.Str("lacunas")))
		lacunas, _ := raw.Default()
		if _, err := lacunas.Len().Int64(); err != nil || cuetil.ListBuiltByComprehension(raw) {
			errs = cerrors.Append(errs, cerrors.Newf(userPos(lacunas),
				"lens %s -> %s: lacunas must be listed unconditionally, with each lacuna's condition field deciding whether it applies", from, to))
			continue
		}
		for i := 0; ; i++ {
			lac := lacunas.LookupPath(cue.MakePath(cue.Index(i)))
			if !lac.Exists() {
				break
			}
			fail := func(format string, args ...any) {
				errs = cerrors.Append(errs, cerrors.Newf(userPos(lac), "lens %s -> %s, lacuna %d: %s", from, to, i, fmt.Sprintf(format, args...)))
			}
			if !lac.LookupPath(cue.MakePath(cue.Str("message"))).Exists() {
				fail("message is required")
			}
			if !lac.LookupPath(cue.MakePath(cue.Str("type"))).Exists() {
				fail("type is required")
			}
			for _, side := range []struct {
				field string
				sch   *schemaDef
			}{{"sourceFields", fsch}, {"targetFields", tsch}} {
				refs := lac.LookupPath(cue.MakePath(cue.Str(side.field)))
				for j := 0; ; j++ {
					ref := refs.LookupPath(cue.MakePath(cue.Index(j)))
					if !ref.Exists() {
						break
					}
					path, err := ref.LookupPath(cue.MakePath(cue.Str("path"))).String()
					if err != nil {
						fail("%s[%d].path must be a concrete string", side.field, j)
						continue
					}
					if err := checkFieldRefPath(ml.rt.Context(), path, side.sch); err != nil {
						fail("%s[%d]: %v", side.field, j, err)
					}
				}
			}
		}
	}

	if errs != nil {
		return errors.Mark(errs, terrors.ErrInvalidLens)
	}
	return nil
}

func (ml *maybeLineage) checkLensesOrder() error {
	// Two distinct validation paths, depending on whether the lenses were defined in
	// Go or CUE.
	if len(ml.implens) > 0 {
		ids := make([]lensID, 0, len(ml.implens))
		for _, lens := range ml.implens {
			if lens.Mapper == nil {
				return fmt.Errorf("nil Go migration func for %s", lid(lens.From, lens.To))
			}
			ids = append(ids, lid(lens.From, lens.To))
		}
		if err := ml.checkLensCompleteness(ids, "Go migrations"); err != nil {
			return err
		}
		ml.lensmap = make(map[lensID]ImperativeLens, len(ml.implens))
		for _, lens := range ml.implens {
			ml.lensmap[lid(lens.From, lens.To)] = lens
		}
		return nil
	}

	lensIter, err := ml.uni.LookupPath(cue.MakePath(cue.Str("lenses"))).List()
	if err != nil {
		return nil // no lenses found
	}

	var ids []lensID
	var previous *lensVersionDef
	for lensIter.Next() {
		curr, err := newLensVersionDef(lensIter.Value())
		if err != nil {
			return err
		}

		if err := doCheck(previous, &curr); err != nil {
			return err
		}

		previous = &curr
		ids = append(ids, lid(curr.from, curr.to))
	}

	// Translate selects CUE lenses by their position in the sorted list, so a
	// missing or extra lens would shift every lens after it.
	return ml.checkLensCompleteness(ids, "lenses")
}

// checkLensCompleteness checks that a lineage has exactly the lenses its schema
// versions require: a lens from every schema back to its predecessor, and a lens
// forward across every major version boundary. Duplicates, self-lenses, lenses
// between nonexistent or non-adjacent versions, and forward lenses across minor
// versions (which are implicit) are all errors. kind names the lenses in errors,
// e.g. "CUE lenses".
func (ml *maybeLineage) checkLensCompleteness(ids []lensID, kind string) error {
	all := make(map[lensID]bool)
	for _, id := range ids {
		if all[id] {
			return errors.Mark(fmt.Errorf("duplicate %s for %s", kind, id), terrors.ErrErroneousLenses)
		}
		all[id] = true
	}

	var missing []lensID

	var prior SyntacticVersion
	for _, sch := range ml.schlist[1:] {
		// there must always at least be a reverse lens
		v := sch.Version()
		revid := lid(v, prior)

		if !all[revid] {
			missing = append(missing, revid)
		} else {
			delete(all, revid)
		}

		if v[0] != prior[0] {
			// if we crossed a major version, there must also be a forward lens
			fwdid := lid(prior, v)
			if !all[fwdid] {
				missing = append(missing, fwdid)
			} else {
				delete(all, fwdid)
			}
		}
		prior = v
	}

	// TODO is it worth making each sub-item into its own error type?
	if len(missing) > 0 {
		b := new(bytes.Buffer)

		fmt.Fprintf(b, "%s not provided for the following version pairs:\n", kind)
		for _, mlid := range missing {
			fmt.Fprint(b, "\t", mlid, "\n")
		}
		return errors.Mark(errors.New(b.String()), terrors.ErrMissingLenses)
	}

	if len(all) > 0 {
		b := new(bytes.Buffer)

		fmt.Fprintf(b, "%s erroneously provided for the following version pairs:\n", kind)
		// walk the slice so output is reliably ordered
		for _, elid := range ids {
			// if it's not in the list it's because it was expected & already processed
			if _, has := all[elid]; !has {
				continue
			}
			if !synvExists(ml.allv, elid.To) {
				fmt.Fprintf(b, "\t%s (schema version %s does not exist)\n", elid, elid.To)
			} else if !synvExists(ml.allv, elid.From) {
				fmt.Fprintf(b, "\t%s (schema version %s does not exist)\n", elid, elid.From)
			} else if elid.To == elid.From {
				fmt.Fprintf(b, "\t%s (lenses from a version to itself are not allowed)\n", elid)
			} else if elid.To.Less(elid.From) {
				// reverse lenses
				// only possibility is non-sequential versions connected
				fmt.Fprintf(b, "\t%s (%s is the predecessor of %s, not %s)\n", elid, ml.allv[searchSynv(ml.allv, elid.From)-1], elid.From, elid.To)
			} else if elid.To[0] == elid.From[0] {
				// forward lens within a major version
				fmt.Fprintf(b, "\t%s (minor version upgrades are handled automatically)\n", elid)
			} else {
				// forward lens across major versions, between non-adjacent versions
				fmt.Fprintf(b, "\t%s (%s is the successor of %s, not %s)\n", elid, ml.allv[searchSynv(ml.allv, elid.From)+1], elid.From, elid.To)
			}
		}
		return errors.Mark(errors.New(b.String()), terrors.ErrErroneousLenses)
	}

	return nil
}

type lensVersionDef struct {
	to   SyntacticVersion
	from SyntacticVersion
}

func newLensVersionDef(val cue.Value) (lensVersionDef, error) {
	v := lensVersionDef{}
	to, err := v.version(val, "to")
	if err != nil {
		return lensVersionDef{}, err
	}

	from, err := v.version(val, "from")
	if err != nil {
		return lensVersionDef{}, err
	}

	return lensVersionDef{to: to, from: from}, err
}

func doCheck(prev, curr *lensVersionDef) error {
	if prev == nil {
		return nil
	}

	if curr == nil {
		return nil
	}

	if curr.to.Less(prev.to) {
		return errors.Mark(
			errors.Errorf("lens version [to: %s, from: %s] is not greater than previous lens version [to: %s, from: %s]", curr.to, curr.from, prev.to, prev.from),
			terrors.ErrInvalidLensesOrder)
	}

	if prev.to == curr.to && curr.from.Less(prev.from) {
		return errors.Mark(
			errors.Errorf("lens version [to: %s, from: %s] is not greater than previous lens version [to: %s, from: %s]", curr.to, curr.from, prev.to, prev.from),
			terrors.ErrInvalidLensesOrder)
	}

	return nil
}

func (lensVersionDef) version(val cue.Value, p string) (SyntacticVersion, error) {
	vPath := cue.MakePath(cue.Str(p))
	vval := val.Value().LookupPath(vPath)

	v := SyntacticVersion{}
	if err := vval.Value().Decode(&v); err != nil {
		return v, errors.Mark(mkerror(val, "failed to decode lens version %s from: %s", vval, val), terrors.ErrInvalidLineage)
	}

	return v, nil
}
