package contenthash

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"cuelang.org/go/cue"

	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/embedreg"
)

// This file is the only code in the package that knows the shape of the normal
// form: where a version's schema and lenses are found in a lineage. Everything
// else works on the values it returns.

// Version is a schema version, as thema's SyntacticVersion.
type Version [2]uint

func (v Version) String() string {
	return fmt.Sprintf("%d.%d", v[0], v[1])
}

func (v Version) less(o Version) bool {
	return v[0] < o[0] || (v[0] == o[0] && v[1] < o[1])
}

var (
	pathSchemas    = cue.MakePath(cue.Str("schemas"))
	pathLenses     = cue.MakePath(cue.Str("lenses"))
	pathJoinSchema = cue.MakePath(cue.Str("joinSchema"))
	pathSchema     = cue.MakePath(cue.Str("schema"))
)

// edgeKey is the key of a lens in a lineage's lenses: "<from>-><to>", as
// lineage.cue's edgeKey.
var edgeKey = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)->(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// parseEdge parses a lens key into the versions it maps from and to.
func parseEdge(key string) (from, to Version, ok bool) {
	m := edgeKey.FindStringSubmatch(key)
	if m == nil {
		return from, to, false
	}
	var n [4]uint
	for i, s := range m[1:] {
		u, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return from, to, false
		}
		n[i] = uint(u)
	}
	return Version{n[0], n[1]}, Version{n[2], n[3]}, true
}

// maxAuthoredDepth bounds how far authored follows a chain of references to
// where the lineage is declared (a: ship, b: a, lin: b).
const maxAuthoredDepth = 16

// authored returns the lineage as its author declared it: lin without what
// thema contributes. An author may unify thema.Lineage in or embed it
// (lin: thema.Lineage & {...}, or a file of the lineage's package that
// embeds it), re-export a lineage that does (flagship: thema.Lineage & ship,
// or flagship: ship where ship embeds it), or not mention it at all, and
// thema unifies it in Go where it binds. Which of these happened is how the
// lineage was written or loaded, not what it is, and would otherwise be
// hashed: thema.Lineage constrains every schema and lens, so its constraints
// show in every export.
//
// The lineage's conjuncts are split, a reference among them is followed to the
// value it names, and what thema contributes is dropped: a reference into
// thema's package, or a conjunct declared in thema's own CUE. The rest are the
// lineage's parts. A lineage with nothing of thema's in it is its one part.
//
// The parts are not unified again into a lineage: what holds a lineage may
// close it, as a definition does a struct literal written beside
// thema.Lineage (#H: [string]: thema.Lineage & {name: N}), and with thema's
// conjunct dropped nothing declares the lineage's fields in that closed
// struct. Each field is read from the parts instead (see lookup).
func authored(lin cue.Value) authoredLineage {
	parts, dropped := authoredParts(lin, 0)
	if !dropped || len(parts) == 0 {
		return authoredLineage{lin}
	}
	return parts
}

// An authoredLineage is a lineage's parts, as authored splits them.
type authoredLineage []cue.Value

// lookup returns the value at p beneath the lineage: the unification of the
// value each part has at p, or an invalid cue.Value when no part has one.
func (a authoredLineage) lookup(p cue.Path) cue.Value {
	var v cue.Value
	found := false
	for _, part := range a {
		pv := part.LookupPath(p)
		switch {
		case !pv.Exists():
		case !found:
			v, found = pv, true
		default:
			v = v.Unify(pv)
		}
	}
	return v
}

func authoredParts(v cue.Value, depth int) (parts []cue.Value, dropped bool) {
	for _, c := range cuetil.AppendSplit(v, cue.AndOp, nil) {
		if embedreg.IsKernel(c.Pos()) {
			dropped = true
			continue
		}
		if root, p := c.ReferencePath(); root.Exists() && p.Err() == nil {
			if bi := root.BuildInstance(); bi != nil {
				if path, _, _ := strings.Cut(bi.ImportPath, "@"); path == themaImportPath {
					dropped = true
					continue
				}
			}
			if depth < maxAuthoredDepth {
				ps, d := authoredParts(root.LookupPath(p), depth+1)
				parts, dropped = append(parts, ps...), dropped || d
				continue
			}
		}
		parts = append(parts, c)
	}
	return parts, dropped
}

// versionSchema returns the schema of version v in the lineage lin, unified
// with lin's joinSchema if it has one: the schema instances of v are validated
// against. Schemas are keyed by major, then minor version.
func versionSchema(lin authoredLineage, v Version) (cue.Value, error) {
	sch := lin.lookup(pathSchemas).LookupPath(cue.MakePath(cue.Str(fmt.Sprint(v[0])), cue.Str(fmt.Sprint(v[1])), cue.Str("schema")))
	if !sch.Exists() {
		return cue.Value{}, fmt.Errorf("lineage has no schema with version %s", v)
	}
	if join := lin.lookup(pathJoinSchema); join.Exists() {
		return join.Unify(sch), nil
	}
	return sch, nil
}

// An edgeLens is a lens and the edge it is declared at.
type edgeLens struct {
	lens     cue.Value
	from, to Version
}

// versionLenses returns the lenses on the edge into version v of the lineage
// lin: forward, from v's predecessor to v, and backward, from v to its
// predecessor. Either may be absent (an invalid cue.Value): the first version
// has no predecessor, a minor version's forward lens is implicit, and a tip
// under development may lack lenses it will need. Lenses are keyed by edge.
func versionLenses(lin authoredLineage, v Version) (forward, backward edgeLens, err error) {
	lenses := lin.lookup(pathLenses)
	if !lenses.Exists() {
		return forward, backward, nil
	}
	iter, err := lenses.Fields()
	if err != nil {
		return forward, backward, fmt.Errorf("lineage lenses: %w", err)
	}
	for iter.Next() {
		key := iter.Selector().Unquoted()
		from, to, ok := parseEdge(key)
		if !ok {
			return forward, backward, fmt.Errorf("lineage lenses: %q is not an edge", key)
		}
		var slot *edgeLens
		switch {
		case to == v && from.less(v):
			slot = &forward
		case from == v && to.less(v):
			slot = &backward
		default:
			continue
		}
		if slot.lens.Exists() {
			return forward, backward, fmt.Errorf("lineage has more than one lens from %s to %s", from, to)
		}
		*slot = edgeLens{lens: iter.Value(), from: from, to: to}
	}
	return forward, backward, nil
}
