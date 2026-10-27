package contenthash

import (
	"fmt"

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
	pathVersion    = cue.MakePath(cue.Str("version"))
	pathSchema     = cue.MakePath(cue.Str("schema"))
	pathFrom       = cue.MakePath(cue.Str("from"))
	pathTo         = cue.MakePath(cue.Str("to"))
)

// lensFields are the fields of a lens that are its content. from and to are
// its position, which, like a schema's version, is not hashed.
var lensFields = []string{"input", "result", "lacunas"}

// versionSchema returns the schema of version v in the lineage lin, unified
// with lin's joinSchema if it has one: the schema instances of v are validated
// against.
func versionSchema(lin cue.Value, v Version) (cue.Value, error) {
	iter, err := lin.LookupPath(pathSchemas).List()
	if err != nil {
		return cue.Value{}, fmt.Errorf("lineage schemas: %w", err)
	}
	var found []cue.Value
	for iter.Next() {
		sv, err := schemaVersion(iter.Value())
		if err != nil {
			return cue.Value{}, err
		}
		if sv == v {
			found = append(found, iter.Value().LookupPath(pathSchema))
		}
	}
	switch len(found) {
	case 0:
		return cue.Value{}, fmt.Errorf("lineage has no schema with version %s", v)
	case 1:
	default:
		return cue.Value{}, fmt.Errorf("lineage has %d schemas with version %s", len(found), v)
	}
	if join := lin.LookupPath(pathJoinSchema); join.Exists() {
		return join.Unify(found[0]), nil
	}
	return found[0], nil
}

// schemaVersion returns the version of a schema entry. Written as the author
// wrote it, an entry may omit its version, which then defaults to 0.0
// (SchemaDef.version in lineage.cue).
func schemaVersion(entry cue.Value) (Version, error) {
	var sv Version
	vv := entry.LookupPath(pathVersion)
	if !vv.Exists() {
		return sv, nil
	}
	if err := vv.Decode(&sv); err != nil {
		return sv, fmt.Errorf("schema version: %w", err)
	}
	return sv, nil
}

// versionLenses returns the lenses on the edge into version v of the lineage
// lin: forward, from v's predecessor to v, and backward, from v to its
// predecessor. Either may be absent (an invalid cue.Value): the first version
// has no predecessor, a minor version's forward lens is implicit, and a tip
// under development may lack lenses it will need.
func versionLenses(lin cue.Value, v Version) (forward, backward cue.Value, err error) {
	lenses := lin.LookupPath(pathLenses)
	if !lenses.Exists() {
		return forward, backward, nil
	}
	iter, err := lenses.List()
	if err != nil {
		return forward, backward, fmt.Errorf("lineage lenses: %w", err)
	}
	for iter.Next() {
		lens := iter.Value()
		var from, to Version
		if err := lens.LookupPath(pathFrom).Decode(&from); err != nil {
			return forward, backward, fmt.Errorf("lens from: %w", err)
		}
		if err := lens.LookupPath(pathTo).Decode(&to); err != nil {
			return forward, backward, fmt.Errorf("lens to: %w", err)
		}
		var slot *cue.Value
		switch {
		case to == v && from.less(v):
			slot = &forward
		case from == v && to.less(v):
			slot = &backward
		default:
			continue
		}
		if slot.Exists() {
			return forward, backward, fmt.Errorf("lineage has more than one lens from %s to %s", from, to)
		}
		*slot = lens
	}
	return forward, backward, nil
}
