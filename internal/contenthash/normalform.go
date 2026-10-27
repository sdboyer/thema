package contenthash

import (
	"fmt"
	"regexp"
	"strconv"

	"cuelang.org/go/cue"
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

// lensFields are the fields of a lens that are its content. The edge it is
// declared at is its position, which, like a schema's version, is not hashed.
var lensFields = []string{"input", "result", "lacunas"}

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

// versionSchema returns the schema of version v in the lineage lin, unified
// with lin's joinSchema if it has one: the schema instances of v are validated
// against. Schemas are keyed by major, then minor version.
func versionSchema(lin cue.Value, v Version) (cue.Value, error) {
	sch := lin.LookupPath(pathSchemas).LookupPath(cue.MakePath(cue.Str(fmt.Sprint(v[0])), cue.Str(fmt.Sprint(v[1])), cue.Str("schema")))
	if !sch.Exists() {
		return cue.Value{}, fmt.Errorf("lineage has no schema with version %s", v)
	}
	if join := lin.LookupPath(pathJoinSchema); join.Exists() {
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
func versionLenses(lin cue.Value, v Version) (forward, backward edgeLens, err error) {
	lenses := lin.LookupPath(pathLenses)
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
