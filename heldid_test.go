package thema

import (
	"slices"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema/diag"
)

// heldIDSrc holds the lineages _lenses, with a lens on each edge, and _single,
// with one version and no lenses, as the normal form writes it, bare and in
// each of the ways a framework holds lineages in definitions.
const heldIDSrc = `
_lenses: {
	name: "q"
	schemas: {
		"0": "0": schema: {id: string}
		"1": "0": schema: {key: string}
	}
	lenses: {
		"0.0->1.0": {v0_0: _, v1_0: {key: v0_0.id}}
		"1.0->0.0": {v1_0: _, v0_0: {id: v1_0.key}}
	}
}
// _lenses with its backward lens changed.
_backward: {
	name: "q"
	schemas: {
		"0": "0": schema: {id: string}
		"1": "0": schema: {key: string}
	}
	lenses: {
		"0.0->1.0": {v0_0: _, v1_0: {key: v0_0.id}}
		"1.0->0.0": {v1_0: _, v0_0: {id: "unknown"}}
	}
}
_single: {
	name: "q"
	schemas: "0": "0": schema: {id: string}
	lenses: {}
}

bare: lenses: thema.Lineage & _lenses
bare: single: thema.Lineage & _single

// thema.Lineage alone in a definition.
#Alone: {lineages: [string]: thema.Lineage}
alone: lenses: #Alone & {lineages: q: _lenses}
alone: single: #Alone & {lineages: q: _single}

// A literal beside thema.Lineage, in a definition.
#Beside: {lineages: [string]: thema.Lineage & {name: "q"}}
beside: lenses: #Beside & {lineages: q: _lenses}
beside: single: #Beside & {lineages: q: _single}
beside: backward: #Beside & {lineages: q: _backward}

// A framework's shape: operations name lineages a definition holds, and
// reach them through a reference.
#Framework: {
	name!: string
	operations: [string]: #Operation
	#lineages: [string]~(N,_): thema.Lineage & {name: N}
	#Operation: {
		use?: body?: string
		if use.body != _|_ {body: #lineages[use.body]}
		body?: thema.Lineage
	}
}
framework: lenses: #Framework & {name: "x", #lineages: q: _lenses, operations: get: use: body: "q"}
framework: single: #Framework & {name: "x", #lineages: q: _single, operations: get: use: body: "q"}
`

// A lineage's version IDs are the lineage's, whatever holds it: held in a
// definition, they are the IDs it has bare, and a check that it only appends
// passes between the two.
func TestVersionIDIndependentOfHolder(t *testing.T) {
	rt := NewRuntime(cuecontext.New())
	root := loadLineageRoot(t, rt.Context(), heldIDSrc)
	bind := func(path string) Lineage {
		t.Helper()
		lin, err := BindLineage(root, cue.ParsePath(path), rt)
		if err != nil {
			t.Fatalf("%s does not bind: %v", path, diag.Diagnostics(err))
		}
		return lin
	}
	for _, name := range []string{"lenses", "single"} {
		t.Run(name, func(t *testing.T) {
			bare := bind("bare." + name)
			for _, path := range []string{
				"alone." + name + ".lineages.q",
				"beside." + name + ".lineages.q",
				"framework." + name + ".operations.get.body",
			} {
				t.Run(path, func(t *testing.T) {
					held := bind(path)
					for sch := bare.First(); sch != nil; sch = sch.Successor() {
						want, err := sch.ID()
						if err != nil {
							t.Fatal(err)
						}
						got, err := SchemaP(held, sch.Version()).ID()
						if err != nil {
							t.Errorf("%s: %v", sch.Version(), diag.Diagnostics(err))
							continue
						}
						if got != want {
							t.Errorf("%s: ID %s, want the bare lineage's, %s", sch.Version(), got, want)
						}
					}
					if err := CheckAppendOnly(bare, held); err != nil {
						t.Errorf("held, the bare lineage doesn't only append: %v", diag.Diagnostics(err))
					}
				})
			}
		})
	}
}

// Held, a lineage whose lens changed is reported for that lens alone.
func TestCheckAppendOnlyHeldLensChanged(t *testing.T) {
	rt := NewRuntime(cuecontext.New())
	root := loadLineageRoot(t, rt.Context(), heldIDSrc)
	bare, err := BindLineage(root, cue.ParsePath("bare.lenses"), rt)
	if err != nil {
		t.Fatal(err)
	}
	held, err := BindLineage(root, cue.ParsePath("beside.backward.lineages.q"), rt)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range diag.Diagnostics(CheckAppendOnly(bare, held)) {
		got = append(got, string(d.Code)+" "+d.Headline)
	}
	want := []string{"E0251 immutability violation: lens v1.0->v0.0 is not as published"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
