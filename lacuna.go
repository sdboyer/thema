package thema

import (
	"fmt"

	"cuelang.org/go/cue"

	"github.com/sdboyer/thema/internal/cuetil"
)

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
