package thema

import (
	"slices"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/build"

	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/embedreg"
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
// there. The first is where BindLineage was handed it. The others are found
// from the lineage's own structure: every conjunct of the lineage that is a
// reference names a value the lineage is made of, declared in a root at a path
// (a package, flagship: ship; a field, flagship: a; a value in another
// package, flagship: ship.lin), and that value's own conjuncts may be
// references in turn. A reference is inside the lineage exactly when it
// resolves under one of the placements, in that placement's root. Its path
// relative to the lineage is then the same whichever placement it resolved in.
//
// Roots are told apart by their build instance, which the evaluator keeps one
// of for every root, package or not; a placement found through a reference
// carries the instance of the root the reference resolved in. The lineage's
// own root is known the same way when the lineage is a root itself. When it is
// a field (lin: {...}), its root is not reachable from the value, and is
// recognized as the root that holds the lineage's declaration at the
// lineage's path (isOwnRoot).

// maxPlacementDepth bounds how far placements follows a chain of references
// to where the lineage is declared (a: ship, b: a, lin: b).
const maxPlacementDepth = 16

// A placement is a root the lineage is declared in, and the lineage's path in
// it.
type placement struct {
	// inst is the root's build instance, or nil for the lineage's own
	// placement when the lineage is not itself a root.
	inst *build.Instance
	path cue.Path
}

// placements are the places one lineage is declared.
type placements struct {
	lin cue.Value
	// list holds the placements, longest path first, so that a path under
	// several is placed under the innermost.
	list []placement
	// files are the files the lineage is declared in, for recognizing its own
	// root when it is not a root itself (isOwnRoot).
	files map[string]bool
}

func placeLineage(lin cue.Value) *placements {
	pl := &placements{lin: lin, files: make(map[string]bool)}
	for _, f := range declaringFiles(lin) {
		pl.files[f] = true
	}
	own := placement{path: lin.Path()}
	if len(own.path.Selectors()) == 0 {
		own.inst = lin.BuildInstance()
	}
	pl.add(own)
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
	if x.inst != nil {
		return root.BuildInstance() == x.inst
	}
	return pl.isOwnRoot(root)
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

// isOwnRoot reports whether root holds the lineage's own placement when the
// lineage is not a root itself: whether root holds, at the lineage's path, a
// value declared in the lineage's files. Each package is a root of its own,
// declared in its own files, so another package's value at the same path, if
// any, is declared elsewhere. The lineage value itself cannot be compared with
// root: it may be a value thema unified in Go from several declarations, and a
// value's position tells where its first conjunct is, not what it is. The
// files are matched two ways, as a root knows them differently by how it was
// built: an instance loaded from files lists them (BuildInstance), and one
// built from syntax (BuildFile) has only the positions its nodes carry.
func (pl *placements) isOwnRoot(root cue.Value) bool {
	at := root.LookupPath(pl.lin.Path())
	if !at.Exists() {
		return false
	}
	if len(pl.files) == 0 {
		// A lineage with no positions: nothing more to compare.
		return true
	}
	if bi := root.BuildInstance(); bi != nil {
		for _, f := range bi.Files {
			if pl.files[f.Filename] {
				return true
			}
		}
	}
	for _, f := range declaringFiles(at) {
		if pl.files[f] {
			return true
		}
	}
	return false
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

// declaringFiles returns the files a value is declared in: the filenames of
// the positions of its conjuncts and of its top-level fields' conjuncts,
// outside thema's own CUE. A value thema unified in Go has no position of its
// own, but its conjuncts and fields keep theirs.
func declaringFiles(v cue.Value) []string {
	var files []string
	note := func(v cue.Value) {
		for _, c := range cuetil.AppendSplit(v, cue.AndOp, nil) {
			if pos := c.Pos(); pos.IsValid() && !embedreg.IsKernel(pos) {
				files = append(files, pos.Filename())
			}
		}
	}
	note(v)
	if iter, err := v.Fields(); err == nil {
		for iter.Next() {
			note(iter.Value())
		}
	}
	return files
}
