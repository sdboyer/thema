package authoring

import (
	"encoding/json"
	"fmt"
	"path"

	"cuelang.org/go/cue"
	"github.com/sdboyer/jenny"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/provisional"
)

var cuts = jenny.For[*Cut](jenny.InputName(func(c *Cut) string { return c.Version.String() }))

// FrozenVersion is a jenny that writes the version a cut froze,
// history/<major>.<minor>.thema, as [CutTip] made it. A clean cut froze
// nothing, and FrozenVersion writes nothing for it.
var FrozenVersion = cuts.New("FrozenVersion", func(c *Cut) (jenny.File, error) {
	for _, f := range c.Written {
		if f.Path != TipFile {
			return jenny.NewFile(f.Path, f.Text), nil
		}
	}
	return nil, nil
})

// CleanTip is a jenny that writes tip.thema as a cut leaves it: clean, its
// Schema alone. A clean cut changed nothing, and CleanTip writes nothing for
// it.
var CleanTip = cuts.New("CleanTip", func(c *Cut) (jenny.File, error) {
	for _, f := range c.Written {
		if f.Path == TipFile {
			return jenny.NewFile(f.Path, f.Text), nil
		}
	}
	return nil, nil
})

// DecompiledFiles is a jenny that writes the authoring files of a lineage in
// normal form, as [Decompile] recovers them: a version file under history/
// for each version, and a clean tip.thema. Paths are relative to the
// lineage's directory.
var DecompiledFiles = jenny.For[cue.Value](jenny.InputName(lineageName)).New("DecompiledFiles", func(lin cue.Value) (jenny.Files, error) {
	_, files, err := Decompile(lin)
	if err != nil {
		return nil, err
	}
	out := make(jenny.Files, 0, len(files))
	for _, f := range files {
		out = append(out, jenny.NewFile(f.Path, f.Text))
	}
	return out, nil
})

// InitialTip is a jenny that writes the tip.thema of a new lineage whose one
// version is the schema it is given, as [NewTip] does. A lineage of that tip
// alone has an empty history, and its tip cuts as 0.0.
var InitialTip = jenny.For[cue.Value]().New("InitialTip", func(schema cue.Value) (jenny.File, error) {
	tip, err := NewTip(schema)
	if err != nil {
		return nil, err
	}
	return jenny.NewFile(tip.Path, tip.Text), nil
})

// VersionIndex is a jenny that writes a lineage's version index, IndexFile:
// the [thema.VersionID] of each version that was cut, with its schema and
// lens digests, so that a tool can look versions up by content without
// thema. A provisional version, the tip of a lineage bound on the development
// path, was not cut and is left out; a lineage with no other version gets no
// index.
//
// The index is derived: the lineage is the record, and thema never reads an
// index. It is written beside the files it describes, never in them, so
// writing it changes no version's ID. An index is computed under one
// generation of the canonical form ([thema.Digest]); after a generation
// changes, write it again.
var VersionIndex = jenny.For[thema.Lineage](jenny.InputName(func(lin thema.Lineage) string { return lin.Name() })).New("VersionIndex", func(lin thema.Lineage) (jenny.File, error) {
	p, hasProvisional := provisional.Version(lin)
	idx := Index{Lineage: lin.Name()}
	for sch := lin.First(); sch != nil; sch = sch.Successor() {
		if hasProvisional && [2]uint(sch.Version()) == p {
			break
		}
		id, err := sch.ID()
		if err != nil {
			return nil, err
		}
		idx.Versions = append(idx.Versions, IndexedVersion{
			Version: sch.Version().String(),
			ID:      id.String(),
			Schema:  id.Schema.String(),
			Lens:    id.Lens.String(),
		})
	}
	if len(idx.Versions) == 0 {
		return nil, nil
	}
	text, err := json.MarshalIndent(idx, "", "\t")
	if err != nil {
		return nil, fmt.Errorf("the index of %s: %w", lin.Name(), err)
	}
	return jenny.NewFile(IndexFile, append(text, '\n')), nil
})

// Formatted is a mapper that lays out every version file, a file named
// *.thema, as [Format] does, and passes every other file through unchanged.
var Formatted = jenny.BytesMapper(func(f jenny.File, src []byte) ([]byte, error) {
	if path.Ext(f.Path()) != Ext {
		return src, nil
	}
	return Format(src)
})

// lineageName names a lineage in normal form by its name field, or by its
// path where it has none to read, as one built from source at its root has
// none.
func lineageName(lin cue.Value) string {
	if name, err := lin.LookupPath(cue.MakePath(cue.Str("name"))).String(); err == nil {
		return name
	}
	if p := lin.Path().String(); p != "" {
		return p
	}
	return "an unnamed lineage"
}
