package thema

import (
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/provisional"
)

// A lineage bound on the authoring layer's development path has a provisional
// newest version, its tip, whose edge from its predecessor may lack the
// lenses the edge needs. Nothing in thema's API asks for one: the option is
// reachable only through internal/provisional, which load.DevelopmentLineage
// calls. BindLineage called with any option a caller can construct binds a
// complete lineage, as it always has.
func init() {
	provisional.Option = func(v [2]uint) any {
		sv := SyntacticVersion(v)
		return BindOption(func(c *bindConfig) {
			c.provisional = &sv
		})
	}
}

// tolerated reports whether bind tolerates the lens id's absence: it is on
// the edge into the provisional version.
func (cfg *bindConfig) tolerated(id lensID) bool {
	if cfg == nil || cfg.provisional == nil {
		return false
	}
	p := *cfg.provisional
	return id.To == p || id.From == p
}

// missingLens returns the E1002 error for translating from one version to
// another across a lens the provisional version does not have yet, or nil
// when the translation needs no missing lens. The provisional version is the
// newest, so a translation into it crosses the forward lens of its edge, and
// one out of it the backward lens.
func (lin *baseLineage) missingLens(from, to SyntacticVersion) error {
	if lin.provisional == nil || from == to {
		return nil
	}
	p := *lin.provisional
	var id lensID
	switch {
	case to == p:
		id = lid(lin.allv[len(lin.allv)-2], p)
	case from == p:
		id = lid(p, lin.allv[len(lin.allv)-2])
	default:
		return nil
	}
	if !lin.missing[id] {
		return nil
	}
	a := lensAddr(id.From, id.To)
	err := diag.New(diag.E1002).Addr(a).
		Headlinef("translating from %s to %s needs %s, which is not written yet", from, to, lin.vocab.Term(a)).
		Notef(token.NoPos, "%s is provisional: %s, which is still being worked on, and whose lenses may not be written yet", p, lin.vocab.Term(schemaAddr(p))).
		Helpf("write %s", lin.vocab.Term(a)).
		Err()
	// Converted in the lineage's vocabulary, which renders the path.
	return diag.Convert(err, diag.Context{Op: diag.OpTranslate, Vocabulary: lin.vocab})
}
