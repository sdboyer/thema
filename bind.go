package thema

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/cueview"
	"github.com/sdboyer/thema/internal/lenscheck"
	"github.com/sdboyer/thema/internal/lensir"
	"github.com/sdboyer/thema/lacuna"
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

	// programs holds the program built for each lens as checkLensTotality
	// proves it (internal/lensir); nil where the check was skipped.
	// programErr says why a total lens has no program.
	programs   lensir.Programs
	programErr error

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
			return ml.at(diag.E0203, majiter.Value()).
				Headlinef("major version %d declares no schemas", maj).
				Helpf("declare its first schema as version %s", synv(maj, 0)).
				Err()
		}
	}
	slices.SortFunc(schlist, func(a, b *schemaDef) int {
		return cmp.Or(cmp.Compare(a.v[0], b.v[0]), cmp.Compare(a.v[1], b.v[1]))
	})

	// thema's default joinSchema is _, which joins nothing.
	if join := ml.uni.LookupPath(cue.MakePath(cue.Str("joinSchema"))); join.IncompleteKind() != cue.TopKind {
		if err := ml.checkSchemaConstructs(join, diag.Address{Kind: diag.AddrJoinSchema}); err != nil {
			return err
		}
	}

	var previous *schemaDef
	for _, sch := range schlist {
		if err := ml.checkSchemaVersions(previous, sch); err != nil {
			return err
		}

		sch.def = sch.ref.LookupPath(pathSchDef)
		if err := ml.checkSchemaConstructs(sch.ref.LookupPath(pathSch), schemaAddr(sch.v)); err != nil {
			return err
		}
		if previous != nil && !cfg.skipbuggychecks {
			res := compat.Check(ml.rt.Context(), previous.def, sch.def)
			if sch.v[1] == 0 && res.Compatible() {
				// Major version change, should be backwards incompatible
				return ml.at(diag.E0208, sch.ref.LookupPath(pathSch)).
					Headlinef("%s is backward compatible with %s, so it is not a major version", ml.term(schemaAddr(sch.v)), previous.v).
					Helpf("declare it as version %s, or make a breaking change", synv(previous.v[0], previous.v[1]+1)).
					Err()
			}
			if sch.v[1] != 0 && !res.Compatible() {
				// Minor version change, should be backwards compatible. An Unknown
				// verdict counts as incompatible: the author must change the
				// schema so that the checker can decide, or bump the major version.
				var errs []error
				for _, f := range res.Findings {
					if f.Verdict != compat.Compatible {
						errs = append(errs, ml.compatDiag(f, previous, sch))
					}
				}
				return diag.Join(errs...)
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
			return ml.at(diag.E0201, curr.ref).
				Headlinef("the first schema is version %s, not 0.0", curr.v).
				Help("every lineage starts at version 0.0").
				Err()
		}
		return nil
	}

	nextMinor, nextMajor := synv(prev.v[0], prev.v[1]+1), synv(prev.v[0]+1, 0)
	if curr.v != nextMinor && curr.v != nextMajor {
		return ml.at(diag.E0202, curr.ref).
			Headlinef("schema version %s does not follow %s", curr.v, prev.v).
			Helpf("the next schema after %s is version %s or %s", prev.v, nextMinor, nextMajor).
			Err()
	}

	return nil
}

func (ml *maybeLineage) checkExists(cfg *bindConfig) error {
	// The candidate lineage must exist.
	if !ml.raw.Exists() {
		b := ml.diag(diag.E0128)
		if p := ml.raw.Path().String(); p != "" {
			b.Headlinef("there is no value at %s to bind as a lineage", p)
		} else {
			b.Headlinef("there is no value to bind as a lineage")
		}
		if err := ml.raw.Err(); err != nil {
			b.Detail(err.Error())
		}
		return b.Err()
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
	var errs []error
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
			b := ml.at(diag.E0123, iter.Value()).
				Headlinef("%s is not a field of %s", sel.Unquoted(), what)
			if want, ok := diag.Suggest(sel.Unquoted(), names); ok {
				b.Helpf("did you mean %s?", want)
			} else {
				b.Helpf("%s has only %s", what, strings.Join(names, ", "))
			}
			errs = append(errs, b.Err())
		}
	}
	// each calls fn with each regular field of v whose key is valid, and reports
	// those whose key isn't.
	each := func(v cue.Value, valid func(string) bool, code diag.Code, badkey, help string, fn func(string, cue.Value)) {
		iter, err := v.Fields(cue.Optional(true))
		if err != nil {
			return
		}
		for iter.Next() {
			if !iter.Selector().IsString() {
				continue
			}
			if key := iter.Selector().Unquoted(); !valid(key) {
				errs = append(errs, ml.diag(code).AtValue(iter.Value()).
					Headlinef("%q is not %s", key, badkey).
					Help(help).
					Err())
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
	const edgeKeys = `lenses are keyed by "<from>-><to>" versions, such as "1.0->0.1"`

	rtv := ml.rt.Underlying()
	schdef, lensdef := rtv.LookupPath(cue.MakePath(cue.Str("SchemaDef"))), rtv.LookupPath(cue.MakePath(cue.Str("Lens")))
	check(ml.raw, ml.rt.linDef(), "a lineage")
	each(ml.raw.LookupPath(cue.MakePath(cue.Str("schemas"))), isVersion, diag.E0124, "a major version", versionKeys, func(_ string, major cue.Value) {
		each(major, isVersion, diag.E0124, "a minor version", versionKeys, func(_ string, sch cue.Value) {
			check(sch, schdef, "a schema entry")
		})
	})
	each(ml.raw.LookupPath(cue.MakePath(cue.Str("lenses"))), isEdge, diag.E0125, "a lens edge", edgeKeys, func(key string, lens cue.Value) {
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

	return diag.Join(errs...)
}

func (ml *maybeLineage) checkLineageShape(cfg *bindConfig) error {
	// Check certain paths specifically, because these are common getting started errors of just arranging
	// CUE statements in the right way that deserve more targeted guidance
	for _, path := range []string{"name", "schemas"} {
		val := ml.raw.LookupPath(cue.MakePath(cue.Str(path)))
		if !val.Exists() {
			return ml.at(diag.E0120, ml.raw).
				Headlinef("the lineage does not declare %s", path).
				Helpf("every lineage declares name and schemas").
				Err()
		}
		if path == "schemas" {
			if iter, err := val.Fields(); err == nil && !iter.Next() {
				return ml.at(diag.E0122, val).
					Headline("the lineage declares no schemas").
					Help("declare at least version 0.0 under schemas").
					Err()
			}
		}
		if !val.IsConcrete() {
			return ml.at(diag.E0121, val).
				Headlinef("%s is not concrete", path).
				Err()
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
		return ml.convert(err, diag.E0126)
	}

	return nil
}

// Checks the validity properties of lineages that are expressible natively in CUE.
func (ml *maybeLineage) checkNativeValidity(cfg *bindConfig) error {
	// The candidate lineage must be error-free.
	// TODO replace this with Err, this check isn't actually what we want up here. Only schemas themselves must be cycle-free
	if err := ml.raw.Validate(cue.Concrete(false)); err != nil {
		return ml.convert(err, diag.E0126)
	}
	if err := ml.uni.Validate(cue.Concrete(false)); err != nil {
		return ml.convert(err, diag.E0126)
	}

	return nil
}

// checkSchemaShapes checks what each schema and the joinSchema may be: schemas
// must be struct-kinded, a joinSchema must be a struct (or thema's default, _), and each
// schema's examples must be valid instances of it. CUE can constrain kinds, but
// not report a schema like _ as an error until data arrives.
func (ml *maybeLineage) checkSchemaShapes() error {
	ctx := ml.rt.Context()
	var errs []error

	// thema's default joinSchema is _, which joins nothing.
	if join := ml.uni.LookupPath(cue.MakePath(cue.Str("joinSchema"))); join.IncompleteKind() != cue.StructKind && join.IncompleteKind() != cue.TopKind {
		errs = append(errs, ml.at(diag.E0130, join).
			Headlinef("joinSchema is %s, not a struct", join.IncompleteKind()).
			Help("a joinSchema is a struct every schema in the lineage is an instance of, or _ to join nothing").
			Err())
	}

	for _, sch := range ml.schlist {
		if k := sch.def.IncompleteKind(); k != cue.StructKind {
			errs = append(errs, ml.at(diag.E0129, sch.ref.LookupPath(pathSch)).
				Headlinef("%s is %s, not a struct", ml.term(schemaAddr(sch.v)), k).
				Help("every schema is a struct").
				Err())
			continue
		}
		iter, err := sch.ref.LookupPath(cue.MakePath(cue.Str("examples"))).Fields()
		if err != nil {
			continue
		}
		closed := cuetil.CloseRecursive(ctx, sch.def)
		for iter.Next() {
			if err := closed.Unify(iter.Value()).Validate(cue.Concrete(true)); err != nil {
				ex := diag.Address{Kind: diag.AddrExample, Version: diag.Version(sch.v), Name: iter.Selector().Unquoted()}
				errs = append(errs, ml.diag(diag.E0131).AtValue(iter.Value()).Addr(ex).
					Headlinef("%s is not an instance of it", ml.term(ex)).
					Cause(err).
					Err())
			}
		}
	}

	return diag.Join(errs...)
}

// checkSchemaConstructs rejects, in one schema (or the joinSchema), the
// constructs thema does not support: comprehensions and reference cycles. A
// comprehension makes a schema's fields depend on evaluation, and a cycle makes
// its paths unbounded; no later check (compatibility, lens results, lacunas)
// can describe either, so they are refused before any of them reads the
// schema. Admitting either later refuses nothing that binds today.
func (ml *maybeLineage) checkSchemaConstructs(v cue.Value, addr diag.Address) error {
	var errs []error
	name := ml.term(addr)
	t, err := cueview.NewTree(v)
	if err != nil {
		// Every value that reaches here is one thema exported itself.
		return diag.InternalErr(err)
	}
	ast.Walk(t.File, func(n ast.Node) bool {
		c, ok := n.(*ast.Comprehension)
		if !ok {
			return true
		}
		errs = append(errs, ml.diag(diag.E0132).At(c.Pos()).Addr(addr).
			Headlinef("%s uses a comprehension", name).
			Note(token.NoPos, "a schema's fields must be known without evaluating data: no check can describe a schema whose fields depend on evaluation").
			Help("write the fields the comprehension would produce").
			Err())
		return false
	}, nil)

	w := &cycleWalker{ml: ml, addr: addr, root: v, reported: make(map[string]bool)}
	w.walk(v)
	errs = append(errs, w.errs...)

	return diag.Join(errs...)
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
	ml       *maybeLineage
	addr     diag.Address
	root     cue.Value
	stack    []cue.Path
	reported map[string]bool
	errs     []error
}

func (w *cycleWalker) walk(v cue.Value) {
	if _, p := v.ReferencePath(); len(p.Selectors()) > 0 {
		if !cuetil.PathHasPrefix(p, w.root.Path()) {
			// A reference that leaves the schema is not followed: what lies
			// outside (thema's own definitions, imports) is not the author's. One
			// to a value that contains the schema still closes a cycle.
			if cuetil.PathHasPrefix(w.root.Path(), p) {
				w.errs = append(w.errs, w.ml.diag(diag.E0134).At(v.Pos()).Addr(w.addr).
					Headlinef("%s refers to %s, which contains it", w.ml.term(w.addr), w.ml.relPath(p)).
					Note(token.NoPos, "a schema's references may not form a cycle: no check can describe a schema whose paths are unbounded").
					Err())
			}
			return
		}
		key := p.String()
		for i, s := range w.stack {
			if s.String() == key {
				if !w.reported[key] {
					w.reported[key] = true
					w.errs = append(w.errs, w.ml.diag(diag.E0133).At(v.Pos()).Addr(w.addr).
						Headlinef("%s has a reference cycle: %s", w.ml.term(w.addr), w.chain(i)).
						Note(token.NoPos, "a schema's references may not form a cycle: no check can describe a schema whose paths are unbounded").
						Err())
				}
				return
			}
		}
		if len(w.stack) >= maxReferenceDepth {
			w.errs = append(w.errs, w.ml.diag(diag.E0135).At(v.Pos()).Addr(w.addr).
				Headlinef("%s has a chain of references deeper than %d at %s", w.ml.term(w.addr), maxReferenceDepth, w.rel(p)).
				Err())
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
	var errs []error
	for _, cl := range ml.cueLenses() {
		lens, from, to := cl.v, cl.id.From, cl.id.To
		fsch, tsch := byVersion[from], byVersion[to]
		if fsch == nil || tsch == nil {
			continue
		}
		lensA := lensAddr(from, to)
		inputA, resultA := lensA, lensA
		inputA.Kind, resultA.Kind = diag.AddrLensInput, diag.AddrLensResult
		inputPath, resultPath := cue.MakePath(cue.Str(lensParam(from))), cue.MakePath(cue.Str(lensParam(to)))

		source := cuetil.CloseRecursive(ctx, fsch.def)
		input := lens.LookupPath(inputPath)
		if !input.Exists() {
			errs = append(errs, ml.diag(diag.E0310).AtValue(lens).Addr(lensA).
				Headlinef("%s does not declare %s, the instance of %s it takes", ml.term(lensA), lensParam(from), from).
				Helpf("write %s: _", lensParam(from)).
				Err())
			continue
		}
		if !lens.LookupPath(resultPath).Exists() {
			errs = append(errs, ml.diag(diag.E0311).AtValue(lens).Addr(lensA).
				Headlinef("%s does not declare %s, the instance of %s it returns", ml.term(lensA), lensParam(to), to).
				Helpf("write %s: {...}, mapping each field of %s", lensParam(to), to).
				Err())
			continue
		}
		if err := input.Subsume(source); err != nil {
			errs = append(errs, ml.diag(diag.E0303).AtValue(input).Addr(inputA).
				Headlinef("%s does not accept every instance of %s", ml.term(inputA), ml.term(schemaAddr(from))).
				Cause(err).
				Helpf("%s stands for any instance of its source schema; leave it as _", lensParam(from)).
				Err())
			continue
		}

		result := lens.FillPath(inputPath, source).LookupPath(resultPath)
		if err := result.Unify(cuetil.CloseRecursive(ctx, tsch.def)).Validate(); err != nil {
			// The totality checker usually knows the rule behind the conflict,
			// and names the mapping; prefer its account when it has one.
			if _, findings := ml.totalityFindings(lens, fsch, tsch); len(findings) > 0 {
				errs = append(errs, findings...)
				continue
			}
			errs = append(errs, ml.diag(diag.E0304).AtValue(lens.LookupPath(resultPath)).Addr(resultA).
				Headlinef("%s is not an instance of %s", ml.term(resultA), ml.term(schemaAddr(to))).
				Cause(err).
				Err())
			continue
		}
		syn, err := cuetil.ParseLens(lens, lensParam(from), lensParam(to))
		if err != nil {
			errs = append(errs, diag.InternalErr(err))
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
			b := ml.diag(diag.E0305).AtValue(lens.LookupPath(resultPath)).Addr(resultA).
				Headlinef("%s writes %s, which %s does not declare", ml.term(resultA), strings.Join(paths, ", "), ml.term(schemaAddr(to))).
				Help("a lens writes only fields its target schema declares, however open the schema is written")
			errs = append(errs, b.Err())
		}
	}

	return diag.Join(errs...)
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

	var errs []error
	for _, cl := range ml.cueLenses() {
		lens, from, to := cl.v, cl.id.From, cl.id.To
		fsch, tsch := byVersion[from], byVersion[to]
		if fsch == nil || tsch == nil {
			continue
		}
		lensA := lensAddr(from, to)

		// A lens that declares no lacunas has thema's open default, [...Lacuna], which
		// resolves to []. A list built conditionally has no concrete length at all.
		raw := lens.LookupPath(cue.MakePath(cue.Str("lacunas")))
		lacunas, _ := raw.Default()
		syn, err := cuetil.ParseLens(lens, lensParam(from), lensParam(to))
		if err != nil {
			errs = append(errs, diag.InternalErr(err))
			continue
		}
		if _, err := lacunas.Len().Int64(); err != nil || syn.LacunasConditional() {
			lacA := lensA
			lacA.Kind = diag.AddrLacuna
			errs = append(errs, ml.diag(diag.E0401).AtValue(lacunas).Addr(lacA).
				Headlinef("the lacunas of %s are not listed unconditionally", ml.term(lensA)).
				Help("list every lacuna the lens can emit; each lacuna's condition field decides whether it applies").
				Err())
			continue
		}
		for i := 0; ; i++ {
			lac := lacunas.LookupPath(cue.MakePath(cue.Index(i)))
			if !lac.Exists() {
				break
			}
			lacA := lensA
			lacA.Kind, lacA.Index = diag.AddrLacuna, i
			fail := func(code diag.Code, v cue.Value, format string, args ...any) {
				errs = append(errs, ml.diag(code).AtValue(v).Addr(lacA).
					Headlinef("%s %s", ml.term(lacA), fmt.Sprintf(format, args...)).
					Err())
			}
			if !lac.LookupPath(cue.MakePath(cue.Str("message"))).Exists() {
				fail(diag.E0402, lac, "has no message")
			}
			if !lac.LookupPath(cue.MakePath(cue.Str("type"))).Exists() {
				fail(diag.E0403, lac, "has no type")
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
					pathv := ref.LookupPath(cue.MakePath(cue.Str("path")))
					path, err := pathv.String()
					if err != nil {
						fail(diag.E0405, ref, "names a field with %s[%d].path, which is not a concrete string", side.field, j)
						continue
					}
					if p := cue.ParsePath(path); p.Err() != nil {
						fail(diag.E0405, pathv, "names a field with %s[%d].path, but %q is not a path", side.field, j, path)
						continue
					}
					if err := checkFieldRefPath(ml.rt.Context(), path, side.sch); err != nil {
						fail(diag.E0404, pathv, "names %s in %s[%d], which %s does not declare", path, side.field, j, ml.term(schemaAddr(side.sch.v)))
					}
				}
			}
		}
	}

	return diag.Join(errs...)
}

// checkReferences checks what each schema, the joinSchema and each CUE lens
// refer to: a schema only its own body and CUE's builtin packages, a lens only
// the instance it takes, its own body and the builtin packages
// (docs/invariants.md, References). CUE resolves any reference in lexical
// scope, so a schema that reads a sibling schema, the lineage's name or a
// file-level helper evaluates without complaint; the rule is thema's.
// References are read from each value's syntax (the Raw view), the only place
// every one is visible as written: the evaluator resolves lets and index
// expressions and leaves the bodies of pending comprehensions unevaluated
// (references.go says more). Schemas are checked first, and a lineage whose
// schemas fail is reported without its lenses: a lens is judged against
// schemas that mean what they say.
func (ml *maybeLineage) checkReferences() error {
	rc := newRefChecker(ml.rt.Context(), ml.raw)
	var errs []error
	check := func(o refOwner) {
		for _, f := range rc.check(o) {
			errs = append(errs, ml.refDiag(o, f))
		}
	}

	for _, sch := range ml.schlist {
		rel := cue.MakePath(cue.Str(labelSchemas), cue.Str(strconv.Itoa(int(sch.v[0]))), cue.Str(strconv.Itoa(int(sch.v[1]))), cue.Str("schema"))
		check(refOwner{name: fmt.Sprintf("schema %s", sch.v), addr: schemaAddr(sch.v), v: ml.raw.LookupPath(rel), rel: rel})
	}
	// thema's default joinSchema is _, which joins nothing and is not the author's.
	if rel := cue.MakePath(cue.Str(labelJoinSchema)); ml.raw.LookupPath(rel).IncompleteKind() != cue.TopKind {
		check(refOwner{name: labelJoinSchema, addr: diag.Address{Kind: diag.AddrJoinSchema}, v: ml.raw.LookupPath(rel), rel: rel})
	}
	if len(errs) > 0 {
		return diag.Join(errs...)
	}

	for _, cl := range ml.cueLenses() {
		rel := cue.MakePath(cue.Str(labelLenses), cue.Str(cl.id.String()))
		check(refOwner{name: fmt.Sprintf("lens %s", cl.id), addr: lensAddr(cl.id.From, cl.id.To), v: ml.raw.LookupPath(rel), rel: rel, lens: true, takes: lensParam(cl.id.From)})
	}
	return diag.Join(errs...)
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
// Each lens proven total leaves its program (internal/lensir) behind, built
// by the walk as a by-product of the proof; [LensPrograms] returns them. A
// lens the IR cannot yet carry binds all the same, and the accessor says why
// it has no program.
//
// SkipBuggyChecks does not skip it: translation relies on every lens being
// total, so an unproven lens does not bind, whatever the caller asks.
func (ml *maybeLineage) checkLensTotality() error {
	byVersion := make(map[SyntacticVersion]*schemaDef, len(ml.schlist))
	for _, sch := range ml.schlist {
		byVersion[sch.v] = sch
	}
	ml.programs = lensir.Programs{}

	var errs []error
	for _, cl := range ml.cueLenses() {
		lens, from, to := cl.v, cl.id.From, cl.id.To
		fsch, tsch := byVersion[from], byVersion[to]
		if fsch == nil || tsch == nil {
			continue
		}
		res, findings := ml.totalityFindings(lens, fsch, tsch)
		if len(findings) > 0 {
			errs = append(errs, findings...)
			continue
		}
		edge := lensir.Edge{From: lacuna.SyntacticVersion(from), To: lacuna.SyntacticVersion(to)}
		if res.Program == nil {
			ml.programErr = errors.Join(ml.programErr, fmt.Errorf("lens %s has no program: %w", edge, res.ProgramErr))
			continue
		}
		res.Program.Edge = edge
		ml.programs[edge] = res.Program
	}

	return diag.Join(errs...)
}

// totalityFindings runs the totality checker over one lens and returns its
// result, with a diagnostic per finding that is not Total. Under
// SkipBuggyChecks there are none.
func (ml *maybeLineage) totalityFindings(lens cue.Value, fsch, tsch *schemaDef) (lenscheck.Result, []error) {
	if ml.cfg.skipbuggychecks {
		return lenscheck.Result{}, nil
	}
	var errs []error
	res := lenscheck.Check(ml.rt.Context(), lens, fsch.def, tsch.def, lensParam(fsch.v), lensParam(tsch.v))
	for _, f := range res.Findings {
		if f.Verdict != lenscheck.Total {
			errs = append(errs, ml.totalityDiag(f, fsch.v, tsch.v))
		}
	}
	return res, errs
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

	var errs []error
	if len(missing) > 0 {
		// Point at the lenses field when the author wrote one, else the lineage.
		at := ml.raw.LookupPath(cue.MakePath(cue.Str("lenses")))
		if !at.Exists() {
			at = ml.raw
		}
		for _, mlid := range missing {
			a := lensAddr(mlid.From, mlid.To)
			b := ml.diag(diag.E0301).AtValue(at).Addr(a).
				Headlinef("the lineage has no %s", ml.term(a))
			if mlid.To.Less(mlid.From) {
				b.Notef(token.NoPos, "every schema needs a lens back to its predecessor, so that instances of %s can be translated to %s", mlid.From, mlid.To)
			} else {
				b.Notef(token.NoPos, "every major version needs a lens forward from its predecessor, so that instances of %s can be translated to %s", mlid.From, mlid.To)
			}
			errs = append(errs, b.Err())
		}
		return diag.Join(errs...)
	}

	if len(all) > 0 {
		byID := make(map[lensID]cue.Value, len(lenses))
		for _, cl := range lenses {
			byID[cl.id] = cl.v
		}
		// walk the slice so output is reliably ordered
		for _, elid := range ids {
			// if it's not in the list it's because it was expected & already processed
			if _, has := all[elid]; !has {
				continue
			}
			a := lensAddr(elid.From, elid.To)
			errs = append(errs, ml.diag(diag.E0302).AtValue(byID[elid]).Addr(a).
				Headlinef("the lineage's versions do not call for %s", ml.term(a)).
				Note(token.NoPos, ml.erroneousLensReason(elid)).
				Help("a lineage declares a lens from each schema back to its predecessor, and one forward across each major version").
				Err())
		}
		return diag.Join(errs...)
	}

	return nil
}

// schemaAddr is the Address of the schema at version v.
func schemaAddr(v SyntacticVersion) diag.Address {
	return diag.Address{Kind: diag.AddrSchema, Version: diag.Version(v)}
}

// lensAddr is the Address of the lens from -> to.
func lensAddr(from, to SyntacticVersion) diag.Address {
	return diag.Address{Kind: diag.AddrLens, From: diag.Version(from), To: diag.Version(to)}
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
		return fmt.Sprintf("there is no schema %s", elid.To)
	case !synvExists(ml.allv, elid.From):
		return fmt.Sprintf("there is no schema %s", elid.From)
	case elid.To == elid.From:
		return "a lens maps between two versions, never from a version to itself"
	case elid.To.Less(elid.From):
		// reverse lenses
		// only possibility is non-sequential versions connected
		return fmt.Sprintf("%s is the predecessor of %s, not %s", ml.allv[searchSynv(ml.allv, elid.From)-1], elid.From, elid.To)
	case elid.To[0] == elid.From[0]:
		// forward lens within a major version
		return fmt.Sprintf("%s is backward compatible with %s, so translating forward is implicit", elid.To, elid.From)
	default:
		// forward lens across major versions, between non-adjacent versions
		return fmt.Sprintf("%s is the successor of %s, not %s", ml.allv[searchSynv(ml.allv, elid.From)+1], elid.From, elid.To)
	}
}
