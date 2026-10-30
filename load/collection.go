package load

import (
	"io/fs"
	"maps"
	"slices"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
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
// The error is for root itself, when it is not a collection's directory; each
// lineage's own error is in the result's Failed. opts apply to every lineage.
func AuthoringCollection(fsys fs.FS, root string, rt *thema.Runtime, opts ...thema.BindOption) (*Collection[thema.Lineage], error) {
	return collection(fsys, root, func(name string, files []authoring.File) (thema.Lineage, error) {
		return bindAuthoring(rt, name, files, opts)
	})
}

// DevelopmentCollection loads the collection of lineages in root, a directory
// of fsys, and binds each lineage with its tip, on the development path, as
// [DevelopmentLineage] does. It is for developing and testing a collection,
// never for serving it: what is published is what [AuthoringCollection]
// binds. A lineage with no tip.thema fails (E0149), as it does alone.
//
// The error is for root itself, when it is not a collection's directory; each
// lineage's own error is in the result's Failed. opts apply to every lineage.
func DevelopmentCollection(fsys fs.FS, root string, rt *thema.Runtime, opts ...thema.BindOption) (*Collection[*Development], error) {
	return collection(fsys, root, func(name string, files []authoring.File) (*Development, error) {
		return bindDevelopment(rt, name, files, opts)
	})
}

func collection[L any](fsys fs.FS, root string, bind func(name string, files []authoring.File) (L, error)) (*Collection[L], error) {
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
		lin, err := bind(m.Name, m.Files)
		if err != nil {
			c.Failed[m.Name] = err
			continue
		}
		c.Lineages[m.Name] = lin
	}
	return c, nil
}
