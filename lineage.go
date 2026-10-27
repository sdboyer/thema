package thema

import (
	"errors"
	"sort"

	"cuelang.org/go/cue"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/lensir"
)

var (
	_ Lineage                     = &baseLineage{}
	_ ConvergentLineage[Assignee] = &unaryConvLineage[Assignee]{}
)

// A baseLineage is a Go facade over a valid CUE lineage that does not compose
// other lineage.
type baseLineage struct {
	rt *Runtime

	// internal flag to ensure BindLineage is only mechanism to create
	validated bool

	// name of the lineage, Lineage.name
	name string

	// original raw input cue.Value containing lineage definition
	raw cue.Value

	// input cue.Value, unified with thema.Lineage
	uni cue.Value

	// all schema versions in the lineage
	allv []SyntacticVersion

	// all the schemas
	allsch []*schemaDef

	// vocab renders diagnostics about this lineage in the author's terms
	vocab diag.Vocabulary

	// programs holds the program of each lens, built when the lens was proven
	// total; nil where the proofs were skipped (SkipBuggyChecks). programErr
	// says why a proven lens has no program. LensPrograms reads both.
	programs   lensir.Programs
	programErr error
}

// BindLineage takes a raw [cue.Value], checks that it correctly follows Thema's
// invariants, such as translatability and backwards compatibility version
// numbering. If these checks succeed, a [Lineage] is returned.
//
// This function is the only way to create non-nil Lineage objects. As a result,
// all non-nil instances of Lineage in any Go program are guaranteed to follow
// Thema invariants.
func BindLineage(v cue.Value, rt *Runtime, opts ...BindOption) (Lineage, error) {
	orig := v
	// We could be more selective than this, but this isn't supposed to be forever, soooooo
	rt.l()
	defer rt.u()

	cfg := &bindConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	lindef := rt.linDef()

	var raw, uni cue.Value
	// don't unify thema.Lineage again if the input already did it. doing so may
	// result in noisy extra instances of thema internals, confusing things like
	// the openapi encoder and likely having performance implications
	if vlist := cuetil.AppendSplit(orig, cue.AndOp, nil); len(vlist) > 1 {
		others := make([]cue.Value, 0, len(vlist))
		for _, av := range vlist {
			_, path := av.ReferencePath()
			if path.String() != "Lineage" {
				others = append(others, av)
			}
		}

		if len(others) == len(vlist) {
			// input value wasn't unified with thema.Lineage, though there were other unifications
			raw = orig
			uni = lindef.Unify(orig)
		} else {
			// input value was unified with thema.Lineage...
			if len(others) == 1 {
				// ...and there was only one other value, probably a struct literal (but don't lean
				// on that assumption without adding more checks!)
				raw = orig
			} else {
				// ...and there were multiple other values, which we now must unify together
				raw = others[0].Unify(others[1])
				for _, v := range others[2:] {
					raw = raw.Unify(v)
				}
			}

			// The key property for the 'uni' value we store in the lineage is that it is
			// unified exactly once with thema.Lineage. So, reuse the original input if
			// there was only one Lineage unification eliminated. Else, make a new one.
			if len(others) == len(vlist)-1 {
				uni = orig
			} else {
				uni = lindef.Unify(raw)
			}
		}
	} else {
		raw = orig
		uni = lindef.Unify(orig)
	}

	ml := &maybeLineage{
		rt:   rt,
		orig: orig,
		raw:  raw,
		uni:  uni,
		cfg:  cfg,
	}

	if err := ml.checkExists(cfg); err != nil {
		return nil, err
	}
	if err := ml.checkUnknownFields(); err != nil {
		return nil, err
	}
	if err := ml.checkLineageShape(cfg); err != nil {
		return nil, err
	}
	if err := ml.checkNativeValidity(cfg); err != nil {
		return nil, err
	}
	if err := ml.checkGoValidity(cfg); err != nil {
		return nil, err
	}
	if err := ml.checkSchemaShapes(); err != nil {
		return nil, err
	}
	if err := ml.checkLensEdges(); err != nil {
		return nil, err
	}
	if err := ml.checkLensResults(); err != nil {
		return nil, err
	}
	if err := ml.checkLacunas(); err != nil {
		return nil, err
	}
	if err := ml.checkReferences(); err != nil {
		return nil, err
	}
	if err := ml.checkLensTotality(cfg); err != nil {
		return nil, err
	}

	// previously verified that this value is concrete
	nam, _ := orig.LookupPath(cue.MakePath(cue.Str("name"))).String()

	lin := &baseLineage{
		validated:  true,
		rt:         rt,
		name:       nam,
		raw:        ml.raw,
		uni:        ml.uni,
		allsch:     ml.schlist,
		allv:       ml.allv,
		vocab:      ml.vocab(),
		programs:   ml.programs,
		programErr: ml.programErr,
	}

	for _, sch := range lin.allsch {
		sch.lin = lin
	}
	return lin, nil
}

// LensPrograms returns the program of each of lin's lenses, keyed by edge: the
// target-neutral form of the lens (internal/lensir) that binding built as a
// by-product of proving the lens total, for backends such as encoding/capnp
// to render. A lineage bound with [SkipBuggyChecks] skipped the proofs and has
// no programs; a lens using a construct the IR does not yet carry binds, but
// has none either. In both cases the error says so.
func LensPrograms(lin Lineage) (lensir.Programs, error) {
	isValidLineage(lin)
	tlin := lin.(*baseLineage)
	switch {
	case tlin.programs == nil:
		return nil, errors.New("lineage was bound with SkipBuggyChecks, so its lenses were not proven total and have no programs")
	case tlin.programErr != nil:
		return nil, tlin.programErr
	}
	return tlin.programs, nil
}

func isValidLineage(lin Lineage) {
	switch tlin := lin.(type) {
	case nil:
		panic("nil lineage")
	case *baseLineage:
		if !tlin.validated {
			panic("lineage not validated")
		}
	default:
		panic("unreachable")
	}
}

func getLinLib(lin Lineage) *Runtime {
	switch tlin := lin.(type) {
	case *baseLineage:
		return tlin.rt
	default:
		panic("unreachable")
	}
}

// Runtime returns the thema.Runtime instance with which this lineage was built.
func (lin *baseLineage) Runtime() *Runtime {
	return lin.rt
}

// Latest returns the newest Schema in the lineage - largest minor version
// within the largest major version.
func (lin *baseLineage) Latest() Schema {
	return lin.allsch[len(lin.allsch)-1]
}

// First returns the first Schema in the lineage (v0.0). Thema requires that all
// valid lineages contain at least one schema, so this is guaranteed to exist.
func (lin *baseLineage) First() Schema {
	return lin.allsch[0]
}

// All returns all Schemas in the lineage sorted by version (0.0 being the first
// element). Thema requires that all valid lineages contain at least one schema,
// so this is guaranteed to contain at least one element.
func (lin *baseLineage) All() []Schema {
	schemas := make([]Schema, len(lin.allsch))
	for i, s := range lin.allsch {
		schemas[i] = s
	}
	return schemas
}

// Underlying returns the cue.Value of the entire lineage.
func (lin *baseLineage) Underlying() cue.Value {
	isValidLineage(lin)

	return lin.uni
}

// Name returns the name of the object schematized by the lineage, as declared in
// the lineage's name field.
func (lin *baseLineage) Name() string {
	isValidLineage(lin)

	if !lin.validated {
		panic("lineage not validated")
	}
	return lin.name
}

// ValidateAny checks that the provided data is valid with respect to at
// least one of the schemas in the lineage. The oldest (smallest) schema against
// which the data validates is chosen. A nil return indicates no validating
// schema was found.
//
// While this method takes a cue.Value, this is only to avoid having to trigger
// the translation internally; input values must be concrete. To use
// incomplete CUE values with Thema schemas, prefer working directly in CUE,
// or if you must, rely on Underlying().
//
// TODO should this instead be interface{} (ugh ugh wish Go had tagged unions) like FillPath?
func (lin *baseLineage) ValidateAny(data cue.Value) *Instance {
	isValidLineage(lin)

	for sch := lin.schema(synv()); sch != nil; sch = sch.successor() {
		if inst, err := sch.Validate(data); err == nil {
			return inst
		}
	}
	return nil
}

// Schema returns the schema identified by the provided version, if one exists.
//
// Only the [0, 0] schema is guaranteed to exist in all valid lineages.
func (lin *baseLineage) Schema(v SyntacticVersion) (Schema, error) {
	isValidLineage(lin)

	if !synvExists(lin.allv, v) {
		return nil, diag.New(diag.E0240).
			Headlinef("lineage %s has no schema %s", lin.name, v).
			Helpf("its versions are %s", versionList(lin.allv)).
			Err()
	}

	return lin.schema(v), nil
}

func (lin *baseLineage) allVersions() versionList {
	return lin.allv
}

func (lin *baseLineage) schema(v SyntacticVersion) *schemaDef {
	return lin.allsch[searchSynv(lin.allv, v)]
}

func (lin *baseLineage) _lineage() {}

func searchSynv(a []SyntacticVersion, x SyntacticVersion) int {
	return sort.Search(len(a), func(i int) bool { return !a[i].Less(x) })
}

func synvExists(a []SyntacticVersion, x SyntacticVersion) bool {
	i := searchSynv(a, x)
	return i < len(a) && a[i] == x
}

type unaryConvLineage[T Assignee] struct {
	Lineage
	tsch TypedSchema[T]
}

func (lin *unaryConvLineage[T]) TypedSchema() TypedSchema[T] {
	return lin.tsch
}

// IsAppendOnly returns nil if the new lineage only contains new schemas compared to the old one.
// It returns an error if old schemas are updated or deleted.
func IsAppendOnly(oldLineage Lineage, newLineage Lineage) error {
	oldSchemas := oldLineage.All()
	newSchemas := newLineage.All()

	if len(newSchemas) < len(oldSchemas) {
		return diag.New(diag.E0242).
			Headlinef("the new lineage has %d schemas where the published one has %d", len(newSchemas), len(oldSchemas)).
			Help("a published schema is never deleted; add versions, do not remove them").
			Err()
	}

	// Compare each schema as thema uses it: joined with the joinSchema, and closed,
	// since the openness of a schema as written says nothing about whether two
	// schemas accept the same instances.
	ctx := oldLineage.Runtime().Context()
	for i, schema := range oldSchemas {
		x := cuetil.CloseRecursive(ctx, schema.Underlying().LookupPath(pathSchDef))
		y := cuetil.CloseRecursive(ctx, newSchemas[i].Underlying().LookupPath(pathSchDef))

		if err := cuetil.Equal(x, y); err != nil {
			return diag.New(diag.E0243).
				Headlinef("schema %s differs from the published one", schema.Version()).
				AtValue(newSchemas[i].Underlying().LookupPath(pathSch)).
				Cause(err).
				Help("a published schema is never changed; add a new version instead").
				Err()
		}
	}

	return nil
}
