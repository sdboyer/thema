package thema

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/pkg/encoding/json"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/util"
	"github.com/sdboyer/thema/lacuna"
)

// BindInstanceType produces a TypedInstance, given an Instance and a
// TypedSchema derived from its Instance.Schema().
//
// The only possible error occurs if the TypedSchema is not derived from the
// Instance.Schema().
func BindInstanceType[T Assignee](inst *Instance, tsch TypedSchema[T]) (*TypedInstance[T], error) {
	// if !schemaIs(inst.Schema(), tsch) {
	// FIXME stop assuming underlying type UGH
	if !tsch.(*unaryTypedSchema[T]).is(inst.Schema()) {
		return nil, fmt.Errorf("typed schema is not derived from instance's schema")
	}

	return &TypedInstance[T]{
		Instance: inst,
		tsch:     tsch,
	}, nil
}

// Instance represents data that is a valid instance of a Thema [Schema].
//
// It is not possible to create a valid Instance directly. They can only be
// obtained by successful call to [Schema.Validate].
type Instance struct {
	// The CUE representation of the input data
	raw cue.Value
	// A name for the input data, primarily for use in error messages
	name string
	// The schema the data validated against/of which the input data is a valid instance
	sch Schema

	// simple flag the prevents external creation
	valid bool
}

func (i *Instance) check() {
	if !i.valid {
		panic("Instance is not valid; Instances must be created by a call to thema.Schema.Validate")
	}
}

// Hydrate returns a copy of the Instance with all default values specified by
// the schema included.
//
// NOTE hydration implementation is a WIP. If errors are encountered, the
// original input is returned unchanged.
func (i *Instance) Hydrate() *Instance {
	i.check()

	ni, err := doHydrate(i.sch.(*schemaDef).def, i.raw)
	// FIXME For now, just no-op it if we error
	if err != nil {
		return i
	}

	return &Instance{
		valid: true,
		raw:   ni,
		name:  i.name,
		sch:   i.sch,
	}
}

// Dehydrate returns a copy of the Instance with all default values specified by
// the schema removed.
//
// NOTE dehydration implementation is a WIP. If errors are encountered, the
// original input is returned unchanged.
func (i *Instance) Dehydrate() *Instance {
	i.check()

	ni, _, err := doDehydrate(i.sch.(*schemaDef).def, i.raw)
	// FIXME For now, just no-op it if we error
	if err != nil {
		return i
	}

	return &Instance{
		valid: true,
		raw:   ni,
		name:  i.name,
		sch:   i.sch,
	}
}

// AsSuccessor translates the instance into the form specified by the successor
// schema.
func (i *Instance) AsSuccessor() (*Instance, lacuna.TranslationLacunas, error) {
	i.check()
	// A minor version upgrade needs no lens: backwards compatibility means the
	// data is already an instance of the successor. Validate rather than assume
	// it, so that a compatibility break the compat check missed fails here
	// instead of yielding an invalid instance.
	nsch := i.Schema().Successor()
	if nsch.Version()[0] == i.Schema().Version()[0] {
		ni, err := nsch.Validate(i.raw)
		if err != nil {
			// The diagnostics say what the successor rejects; they are the
			// whole story, since no lens was involved.
			return nil, nil, err
		}
		return ni, lacuna.TranslationLacunas{{From: lacuna.SyntacticVersion(i.Schema().Version()), To: lacuna.SyntacticVersion(nsch.Version()), Lacunas: []lacuna.Lacuna{}}}, nil
	}
	return i.Translate(i.sch.Successor().Version())
}

// AsPredecessor translates the instance into the form specified by the predecessor
// schema.
func (i *Instance) AsPredecessor() (*Instance, lacuna.TranslationLacunas, error) {
	i.check()
	return i.Translate(i.sch.Predecessor().Version())
}

// Underlying returns the cue.Value representing the data contained in the Instance.
func (i *Instance) Underlying() cue.Value {
	i.check()
	return i.raw
}

// Schema returns the [Schema] corresponding to this instance.
func (i *Instance) Schema() Schema {
	i.check()
	return i.sch
}

func (i *Instance) rt() *Runtime {
	return getLinLib(i.Schema().Lineage())
}

// TypedInstance represents data that is a valid instance of a Thema
// [TypedSchema].
//
// A TypedInstance is to a [TypedSchema] as an [Instance] is to a [Schema].
//
// It is not possible to create a valid TypedInstance directly. They can only be
// obtained by successful call to [TypedSchema.Validate].
type TypedInstance[T Assignee] struct {
	*Instance
	tsch TypedSchema[T]
}

// TypedSchema returns the [TypedSchema] corresponding to this instance.
//
// This method is identical to [Instance.Schema], except that it returns the already-typed variant.
func (inst *TypedInstance[T]) TypedSchema() TypedSchema[T] {
	inst.check()
	return inst.tsch
}

// Value returns a Go struct of this TypedInstance's generic [Assignee] type,
// populated with the data contained in this instance, including default values, etc.
//
// This method is similar to [json.Unmarshal] - it decodes serialized data into a standard Go type
// for working with in all the usual ways.
func (inst *TypedInstance[T]) Value() (T, error) {
	inst.check()

	t := inst.tsch.NewT()
	// TODO figure out correct pointer handling here
	err := inst.Instance.raw.Decode(&t)
	return t, err
}

// ValueP is the same as Value, but panics if an error is encountered.
func (inst *TypedInstance[T]) ValueP() T {
	inst.check()

	t, err := inst.Value()
	if err != nil {
		panic(fmt.Errorf("error decoding value: %w", err))
	}
	return t
}

// Translate transforms the provided [Instance] to an Instance of a different
// [Schema] from the same [Lineage]. A new *Instance is returned representing the
// transformed value, along with any lacunas accumulated along the way.
//
// Forward translation within a major version (e.g. 0.0 to 0.7) is trivial, as
// all those schema changes are established as backwards compatible by Thema's
// lineage invariants. In such cases, the lens is referred to as implicit, as
// the lineage author does not write it, with translation relying on simple
// unification. Lacunas cannot be emitted from such translations.
//
// Forward translation across major versions (e.g. 0.0 to 1.0), and all reverse
// translation regardless of sequence boundaries (e.g. 1.1 to either 1.0
// or 0.0), is nontrivial and relies on explicitly defined lenses, which
// introduce room for lacunas and author judgment.
//
// Thema translation is non-invertible by design. That is, Thema does not seek
// to generally guarantee that translating an instance from 0.0->1.0->0.0 will
// result in the exact original data. Input state preservation can be fully
// achieved in the program depending on Thema, so we avoid introducing
// complexity into Thema that is not essential for all use cases.
//
// Errors only occur in cases where lenses were written in an unexpected way -
// for example, not all fields were mapped over, and the resulting object is not
// concrete. All errors returned from this func will children of [terrors.ErrInvalidLens].
func (i *Instance) Translate(to SyntacticVersion) (*Instance, lacuna.TranslationLacunas, error) {
	i.check()

	// TODO define this in terms of AsSuccessor and AsPredecessor, rather than those in terms of this.
	newsch, err := i.Schema().Lineage().Schema(to)
	if err != nil {
		panic(fmt.Sprintf("no schema in lineage with version %v, cannot translate", to))
	}

	out, err := cueArgs{
		"inst": i.raw,
		"to":   to,
		"from": i.Schema().Version(),
		"lin":  i.Schema().Lineage().Underlying(),
	}.call("Translate", i.rt())
	if err != nil {
		// This can't happen without a name change or an invariant violation
		panic(err)
	}

	lin := i.Schema().Lineage().(*baseLineage)
	if out.Err() != nil {
		return nil, nil, diag.Convert(out.Err(), lin.translateContext(out))
	}

	// Attempt to evaluate Translate result to remove intermediate structures created by Translate.
	// Otherwise, all the Translate results are non-concrete, which leads to undesired effects.
	raw, _ := out.LookupPath(cue.MakePath(cue.Str("result"), cue.Str("result"))).Default()

	// Check that the result is concrete by trying to marshal/export it as JSON
	if _, err = json.Marshal(raw); err != nil {
		ctx := lin.translateContext(out)
		ctx.Generic = diag.E0307
		return nil, nil, diag.Convert(err, ctx)
	}

	if err = i.checkLensResultFields(out); err != nil {
		return nil, nil, err
	}

	// Ensure the result is a valid instance of the target schema. The kernel
	// closes each step's result against its target, so this holds whenever
	// the steps evaluated; the diagnostics are about the result as data.
	inst, err := newsch.Validate(raw)
	if err != nil {
		return nil, nil, err
	}

	var lac lacuna.TranslationLacunas
	if err := out.LookupPath(cue.MakePath(cue.Str("steps"))).Decode(&lac); err != nil {
		return nil, nil, diag.Convert(err, lin.translateContext(out))
	}
	return inst, lac, nil
}

// checkLensResultFields checks that each lens applied by a CUE Translate (whose
// output is out) wrote only fields its target schema declares. Lenses are
// strictly bound to the schema as defined, even where its author wrote it as
// open. Data a lens copies from its input is not checked; nor are minor-version
// steps, which have no lens.
func (i *Instance) checkLensResultFields(out cue.Value) error {
	steps, err := out.LookupPath(cue.MakePath(cue.Str("steps"))).List()
	if err != nil {
		return nil
	}
	lensPath := cue.MakePath(cue.Hid("_lens", util.ThemaPackageID))
	for steps.Next() {
		lens := steps.Value().LookupPath(lensPath)
		if !lens.Exists() {
			continue
		}
		var from, to SyntacticVersion
		if err := steps.Value().LookupPath(cue.MakePath(cue.Str("from"))).Decode(&from); err != nil {
			return diag.InternalErr(err)
		}
		if err := steps.Value().LookupPath(cue.MakePath(cue.Str("to"))).Decode(&to); err != nil {
			return diag.InternalErr(err)
		}
		result := lens.LookupPath(cue.MakePath(cue.Str(lensParam(to))))
		sch, err := i.Schema().Lineage().Schema(to)
		if err != nil {
			return err
		}
		def := sch.(*schemaDef)
		// The lens's syntax is read from the lineage's lens, not from the step's
		// copy of it: the syntax depends only on what the lens is, and the
		// step's copy, filled with the instance, is not exportable (CUE's
		// exporter cannot name a path through the lens's edge key).
		declared := i.Schema().Lineage().Underlying().LookupPath(cue.MakePath(cue.Str("lenses"), cue.Str(lensID{From: from, To: to}.String())))
		syn, err := cuetil.ParseLens(declared, lensParam(from), lensParam(to))
		if err != nil {
			return diag.InternalErr(err)
		}
		if paths := pathStrings(cuetil.UndeclaredLensFields(i.rt().Context(), result, def.def, syn.Result())); len(paths) > 0 {
			lin := i.Schema().Lineage().(*baseLineage)
			resultA := diag.Address{Kind: diag.AddrLensResult, From: diag.Version(from), To: diag.Version(to)}
			own := lin.uni.LookupPath(cue.MakePath(cue.Str("lenses"), cue.Str(lid(from, to).String()), cue.Str(lensParam(to))))
			return diag.New(diag.E0305).AtValue(own).Addr(resultA).
				Headlinef("%s writes %s for this instance, which %s does not declare", lin.vocab.Term(resultA), strings.Join(paths, ", "), lin.vocab.Term(schemaAddr(to))).
				Help("a lens writes only fields its target schema declares, however open the schema is written").
				Err()
		}
	}
	return nil
}
