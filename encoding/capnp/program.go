package capnp

import (
	"fmt"
	"sort"

	"cuelang.org/go/cue"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/encoding/capnp/compiled"
	"github.com/sdboyer/thema/internal/lensir"
	"github.com/sdboyer/thema/lacuna"
)

// edges builds the compiled edges: one per lens program binding built, each
// with the CEL renderings of its expressions, and a synthesized identity copy
// for each forward minor step whose versions are read through different
// structs. A lineage without programs (bound with SkipBuggyChecks, or with a
// lens the IR does not carry) is rejected: its artifact would have no way
// across its edges.
func (cp *compiler) edges() []*compiled.Edge {
	progs, err := thema.LensPrograms(cp.lin)
	if err != nil {
		pos := authorPos(cp.lin.Underlying().LookupPath(cue.MakePath(cue.Str("lenses"))))
		cp.add(cp.lin.First().Version(), cue.MakePath(), Rejected, RuleLensNoProgram,
			fmt.Sprintf("has no lens programs to compile: %v", err), pos)
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
