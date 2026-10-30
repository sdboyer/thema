package thema

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"

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
	// instead of yielding an invalid instance. Fields the schema does not
	// declare are carried as in any step (see carry): the successor may set
	// some aside, which is never an error.
	nsch := i.Schema().Successor()
	if nsch.Version()[0] == i.Schema().Version()[0] {
		ctx := i.rt().Context()
		decl, extras, err := takeExtras(ctx, i.raw, i.sch.(*schemaDef).def)
		if err != nil {
			return nil, nil, diag.InternalErr(err)
		}
		if _, err = nsch.Validate(decl); err != nil {
			// The diagnostics say what the successor rejects; they are the
			// whole story, since no lens was involved.
			return nil, nil, err
		}
		raw, lacs, err := carry(ctx, decl, nil, i.sch.(*schemaDef).def, nsch.(*schemaDef), i.Schema().Version(), extras)
		if err != nil {
			return nil, nil, err
		}
		ni, err := nsch.Validate(raw)
		if err != nil {
			return nil, nil, err
		}
		return ni, lacuna.TranslationLacunas{{From: lacuna.SyntacticVersion(i.Schema().Version()), To: lacuna.SyntacticVersion(nsch.Version()), Lacunas: append([]lacuna.Lacuna{}, lacs...)}}, nil
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
// unification. Lacunas cannot be emitted from such translations, save for
// fields that are set aside (below).
//
// Forward translation across major versions (e.g. 0.0 to 1.0), and all reverse
// translation regardless of sequence boundaries (e.g. 1.1 to either 1.0
// or 0.0), is nontrivial and relies on explicitly defined lenses, which
// introduce room for lacunas and author judgment.
//
// Fields an instance holds that its schema does not declare, under an open
// struct (...) or matched by a pattern constraint, are not a lens's to
// translate, nor are the fields the source schema declares that a step's lens
// leaves alone. Around each step, translation carries them: each follows its
// struct, to where the lens moved or embedded it whole or else to its own
// path, and is placed there where the target struct is open or has a pattern
// constraint that accepts it, or promoted into a field the target declares,
// and the lens does not write, if the value is an instance of it. A field that
// is not placed is set aside, with a lacuna. Such fields never make
// translation fail; see docs/invariants.md, "Fields no lens maps".
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

	from := i.Schema().Version()
	if from == to {
		raw, out, kerr := i.kernelStep(i.raw, from, to)
		if kerr != nil {
			return nil, nil, kerr
		}
		inst, verr := newsch.Validate(raw)
		if verr != nil {
			return nil, nil, verr
		}
		var lac lacuna.TranslationLacunas
		if derr := out.LookupPath(cue.MakePath(cue.Str("steps"))).Decode(&lac); derr != nil {
			return nil, nil, diag.Convert(derr, i.Schema().Lineage().(*baseLineage).translateContext(out))
		}
		return inst, lac, nil
	}

	// The kernel's Translate runs one step at a time, so that around each
	// step the fields no lens touches can be carried (see carry).
	lac := lacuna.TranslationLacunas{}
	data, sch := i.raw, i.sch.(*schemaDef)
	for sch.Version() != to {
		next := sch.successor()
		if to.Less(sch.Version()) {
			next = sch.predecessor()
		}
		raw, step, serr := i.translateStep(data, sch, next)
		if serr != nil {
			return nil, nil, serr
		}
		lac = append(lac, step)
		data, sch = raw, next
	}

	// Ensure the result is a valid instance of the target schema. The kernel
	// closes each step's result against its target, and carrying places only
	// what keeps it an instance, so this holds whenever the steps evaluated;
	// the diagnostics are about the result as data.
	inst, err := newsch.Validate(data)
	if err != nil {
		return nil, nil, err
	}
	return inst, lac, nil
}

// translateStep translates data, an instance of from, to the adjacent version
// to: through the kernel's Translate, without the fields from does not
// declare, which are then carried, as are the fields from declares, to does
// not, and the step's lens does not map (see carry).
func (i *Instance) translateStep(data cue.Value, from, to *schemaDef) (cue.Value, lacuna.TranslationStep, error) {
	ctx := i.rt().Context()
	decl, fields, err := takeExtras(ctx, data, from.def)
	if err != nil {
		return cue.Value{}, lacuna.TranslationStep{}, diag.InternalErr(err)
	}
	raw, out, err := i.kernelStep(decl, from.Version(), to.Version())
	if err != nil {
		return cue.Value{}, lacuna.TranslationStep{}, err
	}
	lin := i.Schema().Lineage().(*baseLineage)
	var steps lacuna.TranslationLacunas
	if err = out.LookupPath(cue.MakePath(cue.Str("steps"))).Decode(&steps); err != nil {
		return cue.Value{}, lacuna.TranslationStep{}, diag.Convert(err, lin.translateContext(out))
	}
	if len(steps) != 1 {
		return cue.Value{}, lacuna.TranslationStep{}, diag.InternalErr(fmt.Errorf("translating %s to %s took %d steps", from.Version(), to.Version(), len(steps)))
	}
	step := steps[0]

	var lens *stepLens
	if from.Version()[0] != to.Version()[0] || to.Version().Less(from.Version()) {
		if lens, err = i.stepLens(out, from.Version(), to.Version()); err != nil {
			return cue.Value{}, lacuna.TranslationStep{}, diag.InternalErr(err)
		}
	}
	fields = takeFields(ctx, data, fields, lens, raw, from.def, to.def)
	raw, lacs, err := carry(ctx, raw, lens, from.def, to, from.Version(), fields)
	if err != nil {
		return cue.Value{}, lacuna.TranslationStep{}, err
	}
	step.Lacunas = append(step.Lacunas, lacs...)
	return raw, step, nil
}

// stepLens reads the lens of the edge from->to, as the step whose kernel
// output is out evaluated it. What the lens reads and copies is read from the
// lineage's lens, so it depends only on what the lens is; what it wrote, from
// the step.
func (i *Instance) stepLens(out cue.Value, from, to SyntacticVersion) (*stepLens, error) {
	lens := out.LookupPath(cue.MakePath(cue.Str("steps"), cue.Index(0), cue.Hid("_lens", util.ThemaPackageID)))
	declared := i.Schema().Lineage().Underlying().LookupPath(cue.MakePath(cue.Str("lenses"), cue.Str(lid(from, to).String())))
	syn, err := cuetil.ParseLens(declared, lensParam(from), lensParam(to))
	if err != nil {
		return nil, err
	}
	sl := &stepLens{
		result: lens.LookupPath(cue.MakePath(cue.Str(lensParam(to)))),
		reads:  syn.InputReads(),
		copies: syn.Copies(),
	}
	lacs, err := lens.LookupPath(cue.MakePath(cue.Str("lacunas"))).List()
	if err != nil {
		return nil, err
	}
	for lacs.Next() {
		refs, err := lacs.Value().LookupPath(cue.MakePath(cue.Str("sourceFields"))).List()
		if err != nil {
			continue
		}
		for refs.Next() {
			s, err := refs.Value().LookupPath(cue.MakePath(cue.Str("path"))).String()
			if err != nil {
				continue
			}
			if p := cue.ParsePath(s); p.Err() == nil {
				sl.named = append(sl.named, p)
			}
		}
	}
	return sl, nil
}

// kernelStep runs the kernel's Translate on data, an instance of from, to the
// version to, and returns its result, checked to be concrete and to hold only
// what each lens may write, and the kernel's output.
func (i *Instance) kernelStep(data cue.Value, from, to SyntacticVersion) (cue.Value, cue.Value, error) {
	out, err := cueArgs{
		"inst": data,
		"to":   to,
		"from": from,
		"lin":  i.Schema().Lineage().Underlying(),
	}.call("Translate", i.rt())
	if err != nil {
		// This can't happen without a name change or an invariant violation
		panic(err)
	}

	lin := i.Schema().Lineage().(*baseLineage)
	if out.Err() != nil {
		return cue.Value{}, cue.Value{}, diag.Convert(out.Err(), lin.translateContext(out))
	}

	// Attempt to evaluate Translate result to remove intermediate structures created by Translate.
	// Otherwise, all the Translate results are non-concrete, which leads to undesired effects.
	raw, _ := out.LookupPath(cue.MakePath(cue.Str("result"), cue.Str("result"))).Default()

	if err = raw.Validate(cue.Concrete(true)); err != nil {
		ctx := lin.translateContext(out)
		ctx.Generic = diag.E0307
		return cue.Value{}, cue.Value{}, diag.Convert(err, ctx)
	}

	if err = i.checkLensResultFields(out); err != nil {
		return cue.Value{}, cue.Value{}, err
	}
	return raw, out, nil
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
			b := diag.New(diag.E0305).AtValue(own).Addr(resultA).
				Headlinef("%s writes %s for this instance, which %s does not declare", lin.vocab.Term(resultA), strings.Join(paths, ", "), lin.vocab.Term(schemaAddr(to)))
			if !patternKeys(b, i.rt().Context(), def.def, lin.vocab.Term(schemaAddr(to)), "writes", paths) {
				b.Help("a lens writes only fields its target schema declares, however open the schema is written")
			}
			return b.Err()
		}
	}
	return nil
}
