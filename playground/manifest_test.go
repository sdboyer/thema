package playground

import (
	"encoding/json"
	"testing"

	"github.com/sdboyer/thema/diag"
)

// Manifest writes the walked request as ManifestFile, which decodes back to
// the same examples and registers as the walk itself does.
func TestManifest(t *testing.T) {
	req, err := WalkExamples(lineages(), walkRoot)
	if err != nil {
		t.Fatal(diag.Diagnostics(err))
	}
	fs, err := Manifest.GenerateFS(req)
	if err != nil {
		t.Fatal(err)
	}
	if fs.Len() != 1 {
		t.Fatalf("Manifest wrote %d files, want only %s", fs.Len(), ManifestFile)
	}
	b, err := fs.ReadFile(ManifestFile)
	if err != nil {
		t.Fatal(err)
	}
	if b[len(b)-1] != '\n' {
		t.Errorf("%s does not end in a newline", ManifestFile)
	}
	var back RegisterExamplesRequest
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("%s is not a register-examples request: %v", ManifestFile, err)
	}
	if len(back.Examples) != len(req.Examples) {
		t.Fatalf("%s holds %d examples, want %d", ManifestFile, len(back.Examples), len(req.Examples))
	}
	for i, h := range back.Examples {
		if h.Example != req.Examples[i].Example {
			t.Errorf("example %d is %+v, want %+v", i, h.Example, req.Examples[i].Example)
		}
	}
	if reg := newSession(t).RegisterExamples(back); !reg.OK || len(reg.Registered) != len(req.Examples) {
		t.Errorf("registering %s: %+v", ManifestFile, reg)
	}
}
