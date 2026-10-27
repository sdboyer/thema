package compile

import (
	"embed"
	"io/fs"
)

//go:embed testdata/roundtrip/*.txtar
var roundtrip embed.FS

// RoundtripFixtures returns the round-trip fixtures, testdata/roundtrip, each
// a lineage in the authoring form with what it compiles to: the playground
// offers some as examples. The fixtures are this package's tests first; an
// example drawn from them changes when they do.
func RoundtripFixtures() fs.FS {
	sub, err := fs.Sub(roundtrip, "testdata/roundtrip")
	if err != nil {
		panic(err)
	}
	return sub
}
