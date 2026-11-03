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
// there. The first is the path of the value passed to BindLineage, not of one
// thema rebuilt from that value's conjuncts, whose path is the path of
// whichever conjunct came first. The others are found from the
// lineage's own structure: every conjunct of the lineage that is a reference
// names a value the lineage is made of, declared in a root at a path (a
// package, flagship: ship; a field, flagship: a; a value in another package,
// flagship: ship.lin), and that value's own conjuncts may be references in
// turn. A conjunct of the lineage that is no reference is declared at its own
// path in its own root: the lineage's text compiled on its own and filled
// whole into the value that uses it (FillPath) is declared at the top of the
// root it was compiled in, and its references resolve there, while a
// narrowing written at the use is declared at the use. A reference is inside
// the lineage exactly when it resolves under one of the placements, in that
// placement's root. Its path relative to the lineage is then the same
// whichever placement it resolved in.
//
// Roots are told apart by their build instance, which the evaluator keeps one
// of for every root, package or not; a placement found through a reference
// carries the instance of the root the reference resolved in. The lineage's
// own root is known the same way when the lineage is a root itself. When it is
// a field (lin: {...}), its root is not reachable from the value, and neither
// is the root a reference in filled text resolves in; each is recognized as
// the root that holds, at the placement's path, a value declared in the files
// of the conjuncts the placement was found from (declaredAt). Each conjunct's
// placement is recognized by that conjunct's files alone: the lineage's files
// together would recognize, at the top of the text's root, any root a
// narrowing is written in, and place everything there inside the lineage.

// maxPlacementDepth bounds how far placements follows a chain of references
// to where the lineage is declared (a: ship, b: a, lin: b).
const maxPlacementDepth = 16

// A placement is a root the lineage is declared in, and the lineage's path in
// it.
type placement struct {
	// inst is the root's build instance, or nil when the root is not
	// reachable from the lineage (declaredAt).
	inst *build.Instance
	path cue.Path
	// files are the files the placement's conjuncts are declared in, for
	// recognizing its root when inst is nil.
	files map[string]bool
}

// placements are the places one lineage is declared.
type placements struct {
	lin cue.Value
	// list holds the placements, longest path first, so that a path under
	// several is placed under the innermost.
	list []placement
}

// placeLineage places lin, the lineage as thema unified it. at is the value
// passed to BindLineage.
func placeLineage(lin, at cue.Value) *placements {
	pl := &placements{lin: lin}
	own := placement{path: at.Path(), files: make(map[string]bool)}
	if len(own.path.Selectors()) == 0 {
		own.inst = at.BuildInstance()
	}
	for _, f := range declaringFiles(lin) {
		own.files[f] = true
	}
	if own.inst != nil || len(own.path.Selectors()) > 0 {
		// A value with neither is no root's: it was unified in Go, and its
		// conjuncts' placements say where it is declared.
		pl.add(own)
	}
	pl.follow(lin, 0)
	slices.SortStableFunc(pl.list, func(a, b placement) int {
		return len(b.path.Selectors()) - len(a.path.Selectors())
	})
	return pl
}

func (pl *placements) add(x placement) {
	for _, y := range pl.list {
		if y.inst == x.inst && y.path.String() == x.path.String() {
			for f := range x.files {
				y.files[f] = true
			}
			return
		}
	}
	pl.list = append(pl.list, x)
}

// follow adds a placement for each conjunct of v that is a reference: the root
// it resolves in and its path there. A conjunct of the lineage itself (depth
// 0) that is no reference adds one too: its own path, in the root its files
// declare. One with no positions says nothing of where it is declared, and
// one reached through a reference is placed by the reference. thema's own
// conjuncts (thema.Lineage, or what thema unified in Go) are not where the
// author declared anything.
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
			if depth == 0 && c.Path().Err() == nil {
				decl := placement{path: c.Path(), files: make(map[string]bool)}
				for _, f := range declaringFiles(c) {
					decl.files[f] = true
				}
				if len(decl.files) > 0 {
					pl.add(decl)
				}
			}
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
	return x.declaredAt(root)
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

// declaredAt reports whether root is x's root when x has no build instance:
// whether root holds, at x's path, a value declared in x's files. Each
// package is a root of its own, declared in its own files, so another
// package's value at the same path, if any, is declared elsewhere. The
// lineage value itself cannot be compared with root: it may be a value thema
// unified in Go from several declarations, and a value's position tells where
// its first conjunct is, not what it is. The files are matched two ways, as a
// root knows them differently by how it was built: an instance loaded from
// files lists them (BuildInstance), and one built from syntax (BuildFile) has
// only the positions its nodes carry.
func (x placement) declaredAt(root cue.Value) bool {
	at := root.LookupPath(x.path)
	if !at.Exists() {
		return false
	}
	if len(x.files) == 0 {
		// A placement with no positions: nothing more to compare.
		return true
	}
	if bi := root.BuildInstance(); bi != nil {
		for _, f := range bi.Files {
			if x.files[f.Filename] {
				return true
			}
		}
	}
	for _, f := range declaringFiles(at) {
		if x.files[f] {
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
