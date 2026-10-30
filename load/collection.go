package load

import (
	"errors"
	"io/fs"
	"maps"
	"path"
	"slices"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
)

// Collection is a collection of lineages, each loaded, compiled and bound on
// its own: a lineage that fails is reported in Failed, and does not keep the
// others from binding. L is what one lineage binds to: a [thema.Lineage] on
// the normal path ([AuthoringCollection]), a [*Development] on the
// development path ([DevelopmentCollection]).
//
// Every lineage of the collection is in exactly one of the two maps, keyed by
// its name, which is its directory's.
type Collection[L any] struct {
	// Lineages holds each lineage that bound.
	Lineages map[string]L
	// Failed holds the error of each lineage that could not be read, compiled
	// or bound: its diagnostics, positioned in its files by their paths
	// relative to its own directory, as [AuthoringLineage] positions them.
	Failed map[string]error
}

// Names returns the name of every lineage of the collection, bound or
// failed, sorted.
func (c *Collection[L]) Names() []string {
	names := slices.AppendSeq(slices.Collect(maps.Keys(c.Lineages)), maps.Keys(c.Failed))
	slices.Sort(names)
	return names
}

// AuthoringCollection loads the collection of lineages in root, a directory
// of fsys, and binds each lineage as [AuthoringLineage] does: its versions
// under history/, with its tip ignored. The collection's layout is described
// in [authoring.LoadCollection].
//
// Each lineage of a collection also keeps its normal form beside its
// history, in [authoring.LineageFile], for native CUE to import. A lineage
// that binds, but whose lineage.cue is missing or is not
// [authoring.GenerateLineageFile]'s text, fails with E0151: what the
// collection publishes to CUE must be what thema binds.
//
// The error is for root itself, when it is not a collection's directory; each
// lineage's own error is in the result's Failed. opts apply to every lineage.
func AuthoringCollection(fsys fs.FS, root string, rt *thema.Runtime, opts ...thema.BindOption) (*Collection[thema.Lineage], error) {
	return collection(fsys, root, func(dir string, m authoring.Member) (thema.Lineage, error) {
		lin, c, err := bindAuthoring(rt, m.Name, m.Files, opts)
		if err != nil {
			return nil, err
		}
		committed, err := fs.ReadFile(fsys, path.Join(dir, authoring.LineageFile))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			committed = nil
		case err != nil:
			return nil, diag.New(diag.E0601).Headlinef("%s cannot be read", path.Join(dir, authoring.LineageFile)).Detail(diag.OSReason(err)).Err()
		case committed == nil:
			committed = []byte{}
		}
		if err := authoring.CheckLineageFile(c, committed); err != nil {
			return nil, err
		}
		return lin, nil
	})
}

// DevelopmentCollection loads the collection of lineages in root, a directory
// of fsys, and binds each lineage with its tip, on the development path, as
// [DevelopmentLineage] does. It is for developing and testing a collection,
// never for serving it: what is published is what [AuthoringCollection]
// binds. A lineage with no tip.thema fails (E0149), as it does alone. Its
// lineage.cue is not checked: it holds the history, which is what
// [AuthoringCollection] checks, and never the tip being developed.
//
// The error is for root itself, when it is not a collection's directory; each
// lineage's own error is in the result's Failed. opts apply to every lineage.
func DevelopmentCollection(fsys fs.FS, root string, rt *thema.Runtime, opts ...thema.BindOption) (*Collection[*Development], error) {
	return collection(fsys, root, func(_ string, m authoring.Member) (*Development, error) {
		return bindDevelopment(rt, m.Name, m.Files, opts)
	})
}

// collection binds each lineage of the collection in root with bind, which is
// given the lineage's directory and what LoadCollection read of it.
func collection[L any](fsys fs.FS, root string, bind func(dir string, m authoring.Member) (L, error)) (*Collection[L], error) {
	members, err := authoring.LoadCollection(fsys, root)
	if err != nil {
		return nil, err
	}
	c := &Collection[L]{Lineages: map[string]L{}, Failed: map[string]error{}}
	for _, m := range members {
		if m.Err != nil {
			c.Failed[m.Name] = m.Err
			continue
		}
		lin, err := bind(path.Join(root, m.Name), m)
		if err != nil {
			c.Failed[m.Name] = err
			continue
		}
		c.Lineages[m.Name] = lin
	}
	return c, nil
}
