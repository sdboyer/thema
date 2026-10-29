package capnp

import (
	"errors"
	"fmt"
	"sort"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/encoding/capnp/compiled"
	"github.com/sdboyer/thema/internal/lensir"
	"github.com/sdboyer/thema/lacuna"
)

// flatten returns the errors err joins, nested joins flattened.
func flatten(err error) []error {
	j, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []error{err}
	}
	var out []error
	for _, e := range j.Unwrap() {
		out = append(out, flatten(e)...)
	}
	return out
}

// edges builds the compiled edges: one per lens program binding built, each
// with the CEL renderings of its expressions, and a synthesized identity copy
// for each forward minor step whose versions are read through different
// structs. A lineage with a lens the IR does not carry has no program for
// it and is rejected: its artifact would have no way across that edge.
func (cp *compiler) edges() []*compiled.Edge {
	progs, err := thema.LensPrograms(cp.lin)
	if err != nil {
		for _, e := range flatten(err) {
			var npe *lensir.NoProgramError
			if !errors.As(e, &npe) {
				panic(fmt.Sprintf("capnp: lens programs failed other than per lens: %v", e))
			}
			cp.addLens(npe.Edge, nil, RuleLensNoProgram,
				fmt.Sprintf("uses a construct the compiled lens programs do not carry yet: %v", npe.Err))
		}
		return nil
	}
	var edges []*compiled.Edge
	for _, p := range progs.Sorted() {
		e := &compiled.Edge{From: [2]uint(p.Edge.From), To: [2]uint(p.Edge.To), Program: p}
		e.CEL = cp.renderProgram(p)
		edges = append(edges, e)
	}
	var prev thema.Schema
	for _, sch := range cp.lin.All() {
		if prev != nil {
			pv, v := prev.Version(), sch.Version()
			if v[0] == pv[0] && cp.gens.rootOf(pv) != cp.gens.rootOf(v) {
				edges = append(edges, &compiled.Edge{
					From:        [2]uint(pv),
					To:          [2]uint(v),
					Synthesized: true,
					Program: &lensir.Program{
						Edge:   lensir.Edge{From: lacuna.SyntacticVersion(pv), To: lacuna.SyntacticVersion(v)},
						Result: []lensir.Node{&lensir.Copy{}},
					},
				})
			}
		}
		prev = sch
	}
	sort.SliceStable(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.From != b.From {
			return thema.SyntacticVersion(a.From).Less(thema.SyntacticVersion(b.From))
		}
		return thema.SyntacticVersion(a.To).Less(thema.SyntacticVersion(b.To))
	})
	return edges
}
