package thema

import (
	"slices"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/build"

	"github.com/sdboyer/thema/internal/cuetil"
)

// Where a lineage is declared, so that a reference can be placed inside it or
// outside it whichever way the lineage reached BindLineage.
//
// A reference resolves, in the value, to a root and an absolute path in that
// root (cue.Value.ReferencePath). Which root, and so which path, is up to the
// evaluator, not the lineage. Bound where it is declared, the lineage's
// references resolve in the root that holds it, at its path there. Re-exported
// from another package (flagship: ship), the evaluator shares the imported
// package's value, and they resolve in that package's root, at the lineage's
// path there, which may be no path at all. Unified with something else where
// it is re-exported (flagship: thema.Lineage & ship), the value is evaluated
// afresh, and they resolve in the re-exporting root again. The lineage is the
// same in each case; what differs is the CUE around it.
//
// So the lineage has placements: each root it is declared in, and its path
// there. The first is the root and path BindLineage was given. The others are found
// from the lineage's own structure: every conjunct of the lineage that is a
// reference names a value the lineage is made of, declared in a root at a path
// (a package, flagship: ship; a field, flagship: a; a value in another
// package, flagship: ship.lin), and that value's own conjuncts may be
// references in turn. A reference is inside the lineage exactly when it
// resolves under one of the placements, in that placement's root. Its path
// relative to the lineage is then the same whichever placement it resolved in.
//
// Roots are told apart by their build instance, which the evaluator keeps for
// the root of every value it built from files, package or not, and for no
// other value; a placement found through a reference carries the instance of
// the root the reference resolved in. A field's root is not reachable from
// the field, so the caller names the lineage's own: BindLineage takes the
// root and the path, and refuses a root built in Go, which has no instance
// (checkRoot).

// maxPlacementDepth bounds how far placements follows a chain of references
// to where the lineage is declared (a: ship, b: a, lin: b).
const maxPlacementDepth = 16

// A placement is a root the lineage is declared in, and the lineage's path in
// it.
type placement struct {
	// inst is the root's build instance.
	inst *build.Instance
	path cue.Path
}

// placements are the places one lineage is declared.
type placements struct {
	lin cue.Value
	// list holds the placements, longest path first, so that a path under
	// several is placed under the innermost.
	list []placement
}

// placeLineage returns the placements of lin, bound from root at p.
func placeLineage(lin, root cue.Value, p cue.Path) *placements {
	pl := &placements{lin: lin}
	pl.add(placement{inst: root.BuildInstance(), path: p})
	pl.follow(lin, 0)
	slices.SortStableFunc(pl.list, func(a, b placement) int {
		return len(b.path.Selectors()) - len(a.path.Selectors())
	})
	return pl
}

func (pl *placements) add(x placement) {
	for _, y := range pl.list {
		if y.inst == x.inst && y.path.String() == x.path.String() {
			return
		}
	}
	pl.list = append(pl.list, x)
}

// follow adds a placement for each conjunct of v that is a reference: the root
// it resolves in and its path there. thema's own conjuncts (thema.Lineage, or
// what thema unified in Go) are not where the author declared anything.
func (pl *placements) follow(v cue.Value, depth int) {
	if depth > maxPlacementDepth {
		return
	}
	for _, c := range cuetil.AppendSplit(v, cue.AndOp, nil) {
		if kernelPos(c) {
			continue
		}
		root, p := c.ReferencePath()
		if !root.Exists() || p.Err() != nil {
			continue
		}
		inst := root.BuildInstance()
		if inst == nil || isThemaPackage(inst.ImportPath) {
			continue
		}
		pl.add(placement{inst: inst, path: p})
		pl.follow(root.LookupPath(p), depth+1)
	}
}

// in reports whether root is x's root.
func (pl *placements) in(x placement, root cue.Value) bool {
	return root.BuildInstance() == x.inst
}

// declares reports whether the lineage is declared in root: whether a reference
// that resolves in root and lies outside the lineage is to something beside the
// lineage, rather than in a package of its own.
func (pl *placements) declares(root cue.Value) bool {
	for _, x := range pl.list {
		if pl.in(x, root) {
			return true
		}
	}
	return false
}

// rel returns the path relative to the lineage of p, an absolute path in root,
// if p lies within the lineage.
func (pl *placements) rel(root cue.Value, p cue.Path) (cue.Path, bool) {
	for _, x := range pl.list {
		if cuetil.PathHasPrefix(p, x.path) && pl.in(x, root) {
			return trimPath(p, x.path), true
		}
	}
	return cue.Path{}, false
}

// relByPath is rel for a path whose root is not known: the path of a value the
// evaluator reached by dereferencing a struct it holds, which encloses the
// reference being walked and so lies in the root the reference is evaluated
// in, whichever that is. It is placed under the innermost placement it lies
// under whose relative path the lineage has.
func (pl *placements) relByPath(p cue.Path) (cue.Path, bool) {
	for _, x := range pl.list {
		if !cuetil.PathHasPrefix(p, x.path) {
			continue
		}
		if rel := trimPath(p, x.path); pl.lin.LookupPath(rel).Exists() {
			return rel, true
		}
	}
	return cue.Path{}, false
}

// trimPath returns p, which has prefix, without it. (cuetil.TrimPathPrefix
// leaves a path of one selector as it is, even when it is the prefix.)
func trimPath(p, prefix cue.Path) cue.Path {
	return cue.MakePath(p.Selectors()[len(prefix.Selectors()):]...)
}

// paths returns the lineage's path in each root it is declared in, for
// reading an identifier in the lineage's syntax that names it
// (references_syntax.go). A placement at its root's top has no path, and no
// identifier names it.
func (pl *placements) paths() []cue.Path {
	var out []cue.Path
	for _, x := range pl.list {
		if len(x.path.Selectors()) > 0 {
			out = append(out, x.path)
		}
	}
	return out
}

// narrowings returns the conjuncts of lin that narrow its joinSchema from
// outside the lineage's text (docs/invariants.md, References). The lineage's
// text is the conjuncts that declare its schemas; a joinSchema written in one
// of them is the lineage's own. Any other conjunct that sets a joinSchema
// narrows it: a struct at the use (request & {joinSchema: X}), a field held
// elsewhere (request & _narrow), or a definition the lineage is unified with
// (#Op: headers: thema.Lineage & {joinSchema: X}). References are followed to
// the conjuncts they are made of, as placements are. A lineage that declares
// no schemas has no text to tell a narrowing from, and nothing narrows it.
func narrowings(lin cue.Value) []cue.Value {
	var out []cue.Value
	declared := false
	var walk func(v cue.Value, depth int)
	walk = func(v cue.Value, depth int) {
		if depth > maxPlacementDepth {
			return
		}
		for _, c := range cuetil.AppendSplit(v, cue.AndOp, nil) {
			if kernelPos(c) {
				continue
			}
			if root, p := c.ReferencePath(); root.Exists() && p.Err() == nil {
				if inst := root.BuildInstance(); inst == nil || !isThemaPackage(inst.ImportPath) {
					walk(root.LookupPath(p), depth+1)
				}
				continue
			}
			switch {
			case declaresSchemas(c):
				declared = true
			case c.LookupPath(cue.MakePath(cue.Str(labelJoinSchema))).Exists():
				out = append(out, c)
			}
		}
	}
	walk(lin, 0)
	if !declared {
		return nil
	}
	return out
}

// declaresSchemas reports whether v declares a lineage's schemas.
func declaresSchemas(v cue.Value) bool {
	return v.LookupPath(cue.MakePath(cue.Str(labelSchemas))).Exists()
}
