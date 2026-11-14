package diag_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/diagtest"
	"github.com/sdboyer/thema/internal/embedreg"
)

// The renderer's goldens: diag/testdata/render/<case>.txtar holds diag.json,
// a list of diagnostics, and the files their positions point into; out/text
// records the rendering, with the archive's files as the source. A #rel
// directive sets RenderOptions.Rel. $THEMA_ROOT in diag.json is the embedded
// module's root, which names thema's version, so that a position in thema's
// own CUE stays one across releases.
func TestRender(t *testing.T) {
	for _, a := range diagtest.Archives(t, "testdata/render") {
		t.Run(a.Name(), func(t *testing.T) {
			src, ok := a.File("diag.json")
			if !ok {
				t.Fatal("no diag.json")
			}
			src = strings.ReplaceAll(src, "$THEMA_ROOT", embedreg.Root())
			var ds []diag.Diagnostic
			if err := json.Unmarshal([]byte(src), &ds); err != nil {
				t.Fatal(err)
			}
			dirs, _ := a.Directives()
			var b bytes.Buffer
			diag.Render(&b, ds, diag.RenderOptions{Source: a.Source, Rel: dirs["rel"]})
			a.Compare(t, map[string]string{"out/text": b.String()})
		})
	}
}
