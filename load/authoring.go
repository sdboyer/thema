package load

import (
	"io/fs"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
)

// AuthoringLineage loads the authoring files of the lineage in dir, a
// directory of fsys, compiles them to the normal form and binds the result.
// Errors are positioned in the authoring files, by their paths relative to
// dir, and worded in the authoring form's terms. For the pieces of that
// sequence, see [github.com/sdboyer/thema/authoring].
func AuthoringLineage(fsys fs.FS, dir string, rt *thema.Runtime, opts ...thema.BindOption) (thema.Lineage, error) {
	name, files, err := authoring.Load(fsys, dir)
	if err != nil {
		return nil, err
	}
	c, err := authoring.Compile(rt.Context(), name, files)
	if err != nil {
		return nil, err
	}
	v := rt.Context().BuildFile(c.File)
	if err := v.Err(); err != nil {
		return nil, diag.Convert(err, diag.Context{Op: diag.OpCompile, Vocabulary: authoring.Vocabulary(c), Generic: diag.E0112})
	}
	return thema.BindLineage(v, rt, append([]thema.BindOption{thema.WithVocabulary(authoring.Vocabulary(c))}, opts...)...)
}
