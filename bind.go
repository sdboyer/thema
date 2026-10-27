package thema

import (
	"bytes"
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
	"github.com/cockroachdb/errors"

	terrors "github.com/sdboyer/thema/errors"
	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/cueview"
	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/lenscheck"
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

// String returns id as the key of its lens in a lineage's lenses, e.g. "1.0->0.1".
func (id lensID) String() string {
	return fmt.Sprintf("%s->%s", id.From, id.To)
}

// parseVersionKey parses a major or minor version key in a lineage's schemas: a
// non-negative decimal integer, without leading zeros, so that each version has
// exactly one key.
func parseVersionKey(key string) (uint, bool) {
	n, err := strconv.ParseUint(key, 10, 32)
	if err != nil || strconv.FormatUint(n, 10) != key {
		return 0, false
	}
	return uint(n), true
}

// lensParam names the field a lens binds an instance of version v to.
func lensParam(v SyntacticVersion) string {
	return cuetil.LensParam(v[0], v[1])
}

// parseLensEdge parses a lens's key in a lineage's lenses: the versions it maps
// from and to, e.g. "1.0->0.1".
func parseLensEdge(key string) (lensID, bool) {
	from, to, ok := strings.Cut(key, "->")
	if !ok {
		return lensID{}, false
	}
	var id lensID
	for _, p := range []struct {
		s string
		v *SyntacticVersion
	}{{from, &id.From}, {to, &id.To}} {
		maj, minor, ok := strings.Cut(p.s, ".")
		if !ok {
			return lensID{}, false
		}
		a, aok := parseVersionKey(maj)
		b, bok := parseVersionKey(minor)
		if !aok || !bok {
			return lensID{}, false
		}
		*p.v = synv(a, b)
	}
	return id, true
}

func (ml *maybeLineage) checkGoValidity(cfg *bindConfig) error {
	majiter, err := ml.uni.LookupPath(cue.MakePath(cue.Str("schemas"))).Fields()
	if err != nil {
		panic(fmt.Sprintf("unreachable - should have already verified schemas field exists and is a struct: %+v", cerrors.Details(err, nil)))
	}
	// Keys were checked by checkUnknownFields.
	var schlist []*schemaDef
	for majiter.Next() {
		maj, _ := parseVersionKey(majiter.Selector().Unquoted())
		miniter, err := majiter.Value().Fields()
		if err != nil {
			panic(fmt.Sprintf("unreachable - should have already verified schemas[%q] is a struct: %+v", majiter.Selector().Unquoted(), cerrors.Details(err, nil)))
		}
		n := len(schlist)
		for miniter.Next() {
			minor, _ := parseVersionKey(miniter.Selector().Unquoted())
			schlist = append(schlist, &schemaDef{ref: miniter.Value(), v: synv(maj, minor)})
		}
		if len(schlist) == n {
			return errors.Mark(cerrors.Newf(userPos(majiter.Value()), "major version %d declares no schemas: its first schema must be version %s", maj, synv(maj, 0)), terrors.ErrNonContiguousVersions)
		}
	}
	slices.SortFunc(schlist, func(a, b *schemaDef) int {
		return cmp.Or(cmp.Compare(a.v[0], b.v[0]), cmp.Compare(a.v[1], b.v[1]))
	})

	// thema's default joinSchema is _, which joins nothing.
	if join := ml.uni.LookupPath(cue.MakePath(cue.Str("joinSchema"))); join.IncompleteKind() != cue.TopKind {
		if err := ml.checkSchemaConstructs(join, "joinSchema"); err != nil {
			return err
		}
	}

	var previous *schemaDef
	for _, sch := range schlist {
		if err := ml.checkSchemaVersions(previous, sch); err != nil {
			return err
		}

		sch.def = sch.ref.LookupPath(pathSchDef)
		if err := ml.checkSchemaConstructs(sch.ref.LookupPath(pathSch), fmt.Sprintf("schema %s", sch.v)); err != nil {
			return err
		}
		if previous != nil && !cfg.skipbuggychecks {
			res := compat.Check(ml.rt.Context(), previous.def, sch.def)
			if sch.v[1] == 0 && res.Compatible() {
				// Major version change, should be backwards incompatible
				return errors.Mark(mkerror(sch.ref.LookupPath(pathSch), "schema %s must be backwards incompatible with schema %s: introduce a breaking change, or redeclare as version %s", sch.v, previous.v, synv(previous.v[0], previous.v[1]+1)), terrors.ErrInvalidLineage)
			}
			if sch.v[1] != 0 && !res.Compatible() {
				// Minor version change, should be backwards compatible. An Unknown
				// verdict counts as incompatible: the author must change the
				// schema so that the checker can decide, or bump the major version.
				what := "is not"
				if res.Verdict() == compat.Unknown {
					what = "cannot be shown to be"
				}
				return errors.Mark(mkerror(sch.ref.LookupPath(pathSch), "schema %s %s backwards compatible with schema %s:\n%s", sch.v, what, previous.v, cerrors.Details(res.Error(), nil)), terrors.ErrInvalidLineage)
			}
		}

		ml.schlist = append(ml.schlist, sch)
		ml.allv = append(ml.allv, sch.v)
		previous = sch
	}

	return nil
}

// checkSchemaVersions checks, for each schema in ascending version order, that
// its version is the exact successor of its predecessor's: either the next minor
// version, or the first (.0) version of the next major version. The first schema
// must be 0.0. Versions are keys, so they can't repeat; this checks that none is
// skipped.
func (ml *maybeLineage) checkSchemaVersions(prev, curr *schemaDef) error {
	if prev == nil {
		if curr.v != synv(0, 0) {
			return errors.Mark(cerrors.Newf(userPos(curr.ref), "the first schema must be version 0.0, not %s", curr.v), terrors.ErrNonContiguousVersions)
		}
		return nil
	}

	nextMinor, nextMajor := synv(prev.v[0], prev.v[1]+1), synv(prev.v[0]+1, 0)
	if curr.v != nextMinor && curr.v != nextMajor {
		return errors.Mark(cerrors.Newf(userPos(curr.ref), "schema version %s does not follow %s: the next schema must be version %s or %s", curr.v, prev.v, nextMinor, nextMajor), terrors.ErrNonContiguousVersions)
	}

	return nil
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
// lineage, schema entry and lens levels, and keys that are not versions (in
// schemas) or edges (in lenses). thema's CUE rejects these too, but CUE
// positions such errors at the guard in thema's own CUE; reporting them from Go
// points at the author's line instead. Hidden and definition fields are left alone,
// as they are free for the author's own helpers.
//
// The set of known fields is read from thema's CUE, so it cannot drift from it.
func (ml *maybeLineage) checkUnknownFields() error {
	var errs cerrors.Error
	check := func(v cue.Value, def cue.Value, what string, extra ...string) {
		known := make(map[string]bool)
		var names []string
		for _, name := range extra {
			known[name] = true
			names = append(names, name)
		}
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
	// each calls fn with each regular field of v whose key is valid, and reports
	// those whose key isn't.
	each := func(v cue.Value, valid func(string) bool, badkey string, fn func(string, cue.Value)) {
		iter, err := v.Fields(cue.Optional(true))
		if err != nil {
			return
		}
		for iter.Next() {
			if !iter.Selector().IsString() {
				continue
			}
			if key := iter.Selector().Unquoted(); !valid(key) {
				errs = cerrors.Append(errs, cerrors.Newf(userPos(iter.Value()), "%q is not %s", key, badkey))
				continue
			}
			fn(iter.Selector().Unquoted(), iter.Value())
		}
	}
	isVersion := func(key string) bool {
		_, ok := parseVersionKey(key)
		return ok
	}
	isEdge := func(key string) bool {
		_, ok := parseLensEdge(key)
		return ok
	}
	const versionKeys = `schemas are keyed by major, then minor version, each a non-negative integer such as "0"`

	rtv := ml.rt.Underlying()
	schdef, lensdef := rtv.LookupPath(cue.MakePath(cue.Str("SchemaDef"))), rtv.LookupPath(cue.MakePath(cue.Str("Lens")))
	check(ml.raw, ml.rt.linDef(), "a lineage")
	each(ml.raw.LookupPath(cue.MakePath(cue.Str("schemas"))), isVersion, "a major version; "+versionKeys, func(_ string, major cue.Value) {
		each(major, isVersion, "a minor version; "+versionKeys, func(_ string, sch cue.Value) {
			check(sch, schdef, "a schema entry")
		})
	})
	each(ml.raw.LookupPath(cue.MakePath(cue.Str("lenses"))), isEdge, `a lens edge; lenses are keyed by "<from>-><to>" versions, such as "1.0->0.1"`, func(key string, lens cue.Value) {
		// A lens's fields are named after the versions its edge names: thema's
		// Lens is a pseudofunction of them, and names the two in takes and
		// returns, since out constrains them by pattern rather than declaring
		// them.
		id, _ := parseLensEdge(key)
		def := lensdef.FillPath(cue.MakePath(cue.Str("from")), id.From).FillPath(cue.MakePath(cue.Str("to")), id.To)
		takes, _ := def.LookupPath(cue.MakePath(cue.Str("takes"))).String()
		returns, _ := def.LookupPath(cue.MakePath(cue.Str("returns"))).String()
		check(lens, def.LookupPath(cue.MakePath(cue.Str("out"))), "a lens", takes, returns)
	})

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
		if path == "schemas" {
			if iter, err := val.Fields(); err == nil && !iter.Next() {
				return errors.Mark(cerrors.Newf(userPos(val), "invalid lineage, Lineage.schemas must declare at least one schema"), terrors.ErrInvalidLineage)
			}
		}
		if !val.IsConcrete() {
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

// checkSchemaShapes checks what each schema and the joinSchema may be: schemas
// must be struct-kinded, a joinSchema must be a struct (or thema's default, _), and each
// schema's examples must be valid instances of it. CUE can constrain kinds, but
// not report a schema like _ as an error until data arrives.
func (ml *maybeLineage) checkSchemaShapes() error {
	ctx := ml.rt.Context()
	var errs cerrors.Error

	// thema's default joinSchema is _, which joins nothing.
	if join := ml.uni.LookupPath(cue.MakePath(cue.Str("joinSchema"))); join.IncompleteKind() != cue.StructKind && join.IncompleteKind() != cue.TopKind {
		errs = cerrors.Append(errs, cerrors.Newf(userPos(join), "joinSchema must be a struct, not %s", join.IncompleteKind()))
	}

	for _, sch := range ml.schlist {
		if k := sch.def.IncompleteKind(); k != cue.StructKind {
			errs = cerrors.Append(errs, cerrors.Newf(userPos(sch.ref.LookupPath(pathSch)), "schema %s must be a struct, not %s", sch.v, k))
			continue
		}
		iter, err := sch.ref.LookupPath(cue.MakePath(cue.Str("examples"))).Fields()
		if err != nil {
			continue
		}
		closed := cuetil.CloseRecursive(ctx, sch.def)
		for iter.Next() {
			if err := closed.Unify(iter.Value()).Validate(cue.Concrete(true)); err != nil {
				errs = cerrors.Append(errs, cerrors.Newf(userPos(iter.Value()), "schema %s example %s is not a valid instance of the schema: %s", sch.v, iter.Selector(), cerrors.Details(err, nil)))
			}
		}
	}

	if errs != nil {
		return errors.Mark(errs, terrors.ErrInvalidLineage)
	}
	return nil
}

// checkSchemaConstructs rejects, in one schema (or the joinSchema), the
// constructs thema does not support: comprehensions and reference cycles. A
// comprehension makes a schema's fields depend on evaluation, and a cycle makes
// its paths unbounded; no later check (compatibility, lens results, lacunas)
// can describe either, so they are refused before any of them reads the
// schema. Admitting either later refuses nothing that binds today.
func (ml *maybeLineage) checkSchemaConstructs(v cue.Value, name string) error {
	var errs cerrors.Error
	t, err := cueview.NewTree(v)
	if err != nil {
		return errors.Mark(cerrors.Newf(userPos(v), "%s: %s", name, err), terrors.ErrInvalidLineage)
	}
	ast.Walk(t.File, func(n ast.Node) bool {
		c, ok := n.(*ast.Comprehension)
		if !ok {
			return true
		}
		errs = cerrors.Append(errs, cerrors.Newf(c.Pos(), "%s: comprehensions are not allowed in schemas", name))
		return false
	}, nil)

	w := &cycleWalker{name: name, root: v, reported: make(map[string]bool)}
	w.walk(v)
	errs = cerrors.Append(errs, w.errs)

	if errs != nil {
		return errors.Mark(errs, terrors.ErrInvalidLineage)
	}
	return nil
}

// maxReferenceDepth bounds the chain of references cycleWalker follows. A
// schema's references form a finite tree once cycles are excluded; a chain this
// long is reported rather than followed further.
const maxReferenceDepth = 64

// cycleWalker walks a schema value, following every reference within the
// schema with the chain of referenced paths on a stack. A reference to a path
// already on the stack closes a cycle, reported at that reference, as does one
// to a value outside the schema that contains it.
type cycleWalker struct {
	name     string
	root     cue.Value
	stack    []cue.Path
	reported map[string]bool
	errs     cerrors.Error
}

func (w *cycleWalker) walk(v cue.Value) {
	if _, p := v.ReferencePath(); len(p.Selectors()) > 0 {
		if !cuetil.PathHasPrefix(p, w.root.Path()) {
			// A reference that leaves the schema is not followed: what lies
			// outside (thema's own definitions, imports) is not the author's. One
			// to a value that contains the schema still closes a cycle.
			if cuetil.PathHasPrefix(w.root.Path(), p) {
				w.errs = cerrors.Append(w.errs, cerrors.Newf(v.Pos(), "%s: reference to %s, which contains the schema; schemas may not contain circular references", w.name, p))
			}
			return
		}
		key := p.String()
		for i, s := range w.stack {
			if s.String() == key {
				if !w.reported[key] {
					w.reported[key] = true
					w.errs = cerrors.Append(w.errs, cerrors.Newf(v.Pos(), "%s: reference cycle %s; schemas may not contain circular references", w.name, w.chain(i)))
				}
				return
			}
		}
		if len(w.stack) >= maxReferenceDepth {
			w.errs = cerrors.Append(w.errs, cerrors.Newf(v.Pos(), "%s: chain of references deeper than %d at %s", w.name, maxReferenceDepth, w.rel(p)))
			return
		}
		w.stack = append(w.stack, p)
		w.walk(cue.Dereference(v))
		w.stack = w.stack[:len(w.stack)-1]
		return
	}

	if op, args := v.Expr(); op != cue.NoOp {
		for _, a := range args {
			w.walk(a)
		}
		return
	}

	switch v.IncompleteKind() {
	case cue.StructKind:
		iter, err := v.Fields(cue.Optional(true), cue.Hidden(true), cue.Definitions(true), cue.Patterns(true))
		if err != nil {
			return
		}
		for iter.Next() {
			w.walk(iter.Value())
		}
	case cue.ListKind:
		iter, err := v.List()
		if err != nil {
			return
		}
		for iter.Next() {
			w.walk(iter.Value())
		}
		if elem := v.LookupPath(cue.MakePath(cue.AnyIndex)); elem.Exists() {
			w.walk(elem)
		}
	}
}

// chain renders the cycle that begins at stack[from], ending where it began.
func (w *cycleWalker) chain(from int) string {
	var parts []string
	for _, p := range w.stack[from:] {
		parts = append(parts, w.rel(p))
	}
	parts = append(parts, w.rel(w.stack[from]))
	return strings.Join(parts, " -> ")
}

// rel renders a referenced path relative to the schema where it lies within
// it, and in full where it leaves it.
func (w *cycleWalker) rel(p cue.Path) string {
	if cuetil.PathHasPrefix(p, w.root.Path()) {
		return cuetil.TrimPathPrefix(p, w.root.Path()).String()
	}
	return p.String()
}

// checkLensResults statically checks each CUE lens against its schemas, with its
// input standing for any instance of its source schema (closed, so references to
// fields the source can't have are errors). The lens's input must accept every
// instance of the source schema, and its result must be consistent with the
// target schema and build only fields the target declares. This catches at bind
// time what would otherwise fail only when a matching instance is translated.
func (ml *maybeLineage) checkLensResults() error {
	ctx := ml.rt.Context()
	byVersion := make(map[SyntacticVersion]*schemaDef, len(ml.schlist))
	for _, sch := range ml.schlist {
		byVersion[sch.v] = sch
	}
	var errs cerrors.Error
	for _, cl := range ml.cueLenses() {
		lens, from, to := cl.v, cl.id.From, cl.id.To
		fsch, tsch := byVersion[from], byVersion[to]
		if fsch == nil || tsch == nil {
			continue
		}
		inputPath, resultPath := cue.MakePath(cue.Str(lensParam(from))), cue.MakePath(cue.Str(lensParam(to)))
		fail := func(v cue.Value, format string, args ...any) {
			errs = cerrors.Append(errs, cerrors.Newf(userPos(v), "lens %s->%s: %s", from, to, fmt.Sprintf(format, args...)))
		}

		source := cuetil.CloseRecursive(ctx, fsch.def)
		input := lens.LookupPath(inputPath)
		if !input.Exists() {
			fail(lens, "the lens does not declare %s, the instance of %s it takes: write %s: _", lensParam(from), from, lensParam(from))
			continue
		}
		if !lens.LookupPath(resultPath).Exists() {
			fail(lens, "the lens does not declare %s, the instance of %s it returns", lensParam(to), to)
			continue
		}
		if err := input.Subsume(source); err != nil {
			fail(input, "%s must accept every instance of schema %s, but is narrower: %s", lensParam(from), from, cerrors.Details(err, nil))
			continue
		}

		result := lens.FillPath(inputPath, source).LookupPath(resultPath)
		if err := result.Unify(cuetil.CloseRecursive(ctx, tsch.def)).Validate(); err != nil {
			fail(lens.LookupPath(resultPath), "%s is not an instance of schema %s:\n%s", lensParam(to), to, cerrors.Details(err, nil))
			continue
		}
		syn, err := cuetil.ParseLens(lens, lensParam(from), lensParam(to))
		if err != nil {
			fail(lens, "%v", err)
			continue
		}
		fields := syn.Result()
		paths := pathStrings(cuetil.UndeclaredLensFields(ctx, result, tsch.def, fields))
		// Fields built inside comprehensions only exist once data makes them fire.
		for _, p := range cuetil.UndeclaredConditionalFields(ctx, fields, tsch.def) {
			if !slices.Contains(paths, p) {
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
	for _, cl := range ml.cueLenses() {
		lens, from, to := cl.v, cl.id.From, cl.id.To
		fsch, tsch := byVersion[from], byVersion[to]
		if fsch == nil || tsch == nil {
			continue
		}

		// A lens that declares no lacunas has thema's open default, [...Lacuna], which
		// resolves to []. A list built conditionally has no concrete length at all.
		raw := lens.LookupPath(cue.MakePath(cue.Str("lacunas")))
		lacunas, _ := raw.Default()
		syn, err := cuetil.ParseLens(lens, lensParam(from), lensParam(to))
		if err != nil {
			errs = cerrors.Append(errs, cerrors.Newf(userPos(lens), "lens %s->%s: %v", from, to, err))
			continue
		}
		if _, err := lacunas.Len().Int64(); err != nil || syn.LacunasConditional() {
			errs = cerrors.Append(errs, cerrors.Newf(userPos(lacunas),
				"lens %s->%s: lacunas must be listed unconditionally, with each lacuna's condition field deciding whether it applies", from, to))
			continue
		}
		for i := 0; ; i++ {
			lac := lacunas.LookupPath(cue.MakePath(cue.Index(i)))
			if !lac.Exists() {
				break
			}
			fail := func(format string, args ...any) {
				errs = cerrors.Append(errs, cerrors.Newf(userPos(lac), "lens %s->%s, lacuna %d: %s", from, to, i, fmt.Sprintf(format, args...)))
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

// checkLensTotality proves each CUE lens total: for every instance of its source
// schema, the result is an instance of its target schema, with every required
// field, and nothing in the lens fails to evaluate on the way. The walk
// (internal/lenscheck) reads the lens's syntax and queries the schemas: a copy is
// judged by the compatibility checker, a constant or computation by what the
// target accepts, a comprehension by whether its patterns cover every instance.
// What it cannot decide is an error too, saying what the author can do. CUE
// evaluates a lens only against data, so without this a lens that drops a field
// or overflows a bound surfaces at translate, for the instance that hits it.
//
// SkipBuggyChecks skips it: the corpus has lenses the check correctly refuses
// (exemplars/expand, 0.3->0.2, drops "baz"), which #87 migrates.
func (ml *maybeLineage) checkLensTotality(cfg *bindConfig) error {
	if cfg.skipbuggychecks {
		return nil
	}
	ctx := ml.rt.Context()
	byVersion := make(map[SyntacticVersion]*schemaDef, len(ml.schlist))
	for _, sch := range ml.schlist {
		byVersion[sch.v] = sch
	}

	var errs cerrors.Error
	for _, cl := range ml.cueLenses() {
		lens, from, to := cl.v, cl.id.From, cl.id.To
		fsch, tsch := byVersion[from], byVersion[to]
		if fsch == nil || tsch == nil {
			continue
		}
		res := lenscheck.Check(ctx, lens, fsch.def, tsch.def, lensParam(from), lensParam(to))
		if res.Total() {
			continue
		}
		verb := "is not total over"
		if res.Verdict() == lenscheck.Unknown {
			verb = "cannot be shown total over"
		}
		errs = cerrors.Append(errs, cerrors.Newf(userPos(lens), "lens %s->%s %s schema %s", from, to, verb, from))
		for _, f := range res.Findings {
			errs = cerrors.Append(errs, cerrors.Newf(f.Pos, "lens %s->%s: %s", from, to, f))
		}
	}

	if errs != nil {
		return errors.Mark(errs, terrors.ErrInvalidLens)
	}
	return nil
}

// checkLensEdges checks that a lineage has exactly the lenses its schema
// versions require: a lens from every schema back to its predecessor, and a lens
// forward across every major version boundary. Self-lenses, lenses between
// nonexistent or non-adjacent versions, and forward lenses across minor versions
// (which are implicit) are all errors. Lenses are keyed by edge, so an edge
// can't repeat.
func (ml *maybeLineage) checkLensEdges() error {
	lenses := ml.cueLenses()
	ids := make([]lensID, 0, len(lenses))
	all := make(map[lensID]bool, len(lenses))
	for _, cl := range lenses {
		ids = append(ids, cl.id)
		all[cl.id] = true
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

		b.WriteString("lenses not provided for the following version pairs:\n")
		for _, mlid := range missing {
			fmt.Fprint(b, "\t", mlid, "\n")
		}
		return errors.Mark(errors.New(b.String()), terrors.ErrMissingLenses)
	}

	if len(all) > 0 {
		b := new(bytes.Buffer)

		b.WriteString("lenses erroneously provided for the following version pairs:\n")
		// walk the slice so output is reliably ordered
		for _, elid := range ids {
			// if it's not in the list it's because it was expected & already processed
			if _, has := all[elid]; !has {
				continue
			}
			fmt.Fprintf(b, "\t%s (%s)\n", elid, ml.erroneousLensReason(elid))
		}
		return errors.Mark(errors.New(b.String()), terrors.ErrErroneousLenses)
	}

	return nil
}

// cueLens is a lens declared in a lineage's CUE, and the edge it is declared at.
type cueLens struct {
	id lensID
	v  cue.Value
}

// cueLenses returns the lenses declared in the lineage's CUE. Their keys have
// already been checked by checkUnknownFields.
func (ml *maybeLineage) cueLenses() []cueLens {
	iter, err := ml.uni.LookupPath(cue.MakePath(cue.Str("lenses"))).Fields()
	if err != nil {
		return nil
	}
	var lenses []cueLens
	for iter.Next() {
		id, _ := parseLensEdge(iter.Selector().Unquoted())
		lenses = append(lenses, cueLens{id: id, v: iter.Value()})
	}
	return lenses
}

// erroneousLensReason explains why a lens the lineage doesn't require is wrong.
func (ml *maybeLineage) erroneousLensReason(elid lensID) string {
	switch {
	case !synvExists(ml.allv, elid.To):
		return fmt.Sprintf("schema version %s does not exist", elid.To)
	case !synvExists(ml.allv, elid.From):
		return fmt.Sprintf("schema version %s does not exist", elid.From)
	case elid.To == elid.From:
		return "lenses from a version to itself are not allowed"
	case elid.To.Less(elid.From):
		// reverse lenses
		// only possibility is non-sequential versions connected
		return fmt.Sprintf("%s is the predecessor of %s, not %s", ml.allv[searchSynv(ml.allv, elid.From)-1], elid.From, elid.To)
	case elid.To[0] == elid.From[0]:
		// forward lens within a major version
		return "minor version upgrades are handled automatically"
	default:
		// forward lens across major versions, between non-adjacent versions
		return fmt.Sprintf("%s is the successor of %s, not %s", ml.allv[searchSynv(ml.allv, elid.From)+1], elid.From, elid.To)
	}
}
