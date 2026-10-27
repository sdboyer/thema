package thema

import (
	"fmt"

	"cuelang.org/go/cue"

	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/lacuna"
)

// checkLacuna checks that l, emitted by a lens from sch to tsch, has a known type
// and a message, and that its field references are declared by the schemas they
// refer to: source fields by sch, target fields by tsch.
func checkLacuna(ctx *cue.Context, l lacuna.Lacuna, sch, tsch *schemaDef) error {
	if !l.Type.Known() {
		return fmt.Errorf("lacuna has unknown type %q", l.Type)
	}
	if l.Message == "" {
		return fmt.Errorf("%s lacuna has no message", l.Type)
	}
	for _, fr := range l.SourceFields {
		if err := checkFieldRefPath(ctx, fr.Path, sch); err != nil {
			return fmt.Errorf("%s lacuna source field: %w", l.Type, err)
		}
	}
	for _, fr := range l.TargetFields {
		if err := checkFieldRefPath(ctx, fr.Path, tsch); err != nil {
			return fmt.Errorf("%s lacuna target field: %w", l.Type, err)
		}
	}
	return nil
}

// checkFieldRefPath checks that path is a valid CUE path to a field sch declares.
func checkFieldRefPath(ctx *cue.Context, path string, sch *schemaDef) error {
	p := cue.ParsePath(path)
	if err := p.Err(); err != nil {
		return fmt.Errorf("invalid path %q: %w", path, err)
	}
	if !cuetil.DeclaresPath(ctx, sch.def, p) {
		return fmt.Errorf("path %q is not declared by schema %s", path, sch.Version())
	}
	return nil
}
