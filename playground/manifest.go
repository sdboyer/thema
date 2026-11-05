package playground

import (
	"encoding/json"

	"github.com/sdboyer/jenny"
)

// ManifestFile is the file the frontend fetches from beside the page at
// start-up, and registers when there is one: a [RegisterExamplesRequest].
const ManifestFile = "examples.json"

// Manifest is a jenny that writes [ManifestFile] for a request, as indented
// JSON: what a host that serves the playground statically publishes beside
// thema.wasm, and what thema serve playground --examples serves.
var Manifest = jenny.For[RegisterExamplesRequest](jenny.InputName(func(RegisterExamplesRequest) string { return "examples" })).
	New("ExamplesManifest", func(req RegisterExamplesRequest) (jenny.File, error) {
		b, err := json.MarshalIndent(req, "", "  ")
		if err != nil {
			return nil, err
		}
		return jenny.NewFile(ManifestFile, append(b, '\n')), nil
	})
