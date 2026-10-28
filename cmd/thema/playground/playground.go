// Package playground holds the playground's web bundle for thema serve
// playground: the frontend in playground/ui, built, and this thema's own
// thema.wasm, wasm_exec.js and thema-fs.js, so that the playground served
// always matches the CLI serving it.
//
// The bundle is generated, not committed. go generate fills dist/, which
// needs node and the frontend's dependencies (npm ci in playground/ui), and
// a build with the playgroundui tag embeds it:
//
//	go generate ./cmd/thema/playground
//	go build -tags playgroundui ./cmd/thema
//
// Without the tag, [Bundle] reports how to build one.
package playground

//go:generate go run ../../../internal/cmd/playgroundgen -out dist

import (
	"errors"
	"io/fs"
)

// ErrNotBuilt is returned by [Bundle] in a build without the playground.
var ErrNotBuilt = errors.New("this thema was built without the playground")

// Manifest describes a bundle: the versions it was built from.
type Manifest struct {
	// ThemaVersion is the thema module version thema.wasm was built from.
	ThemaVersion string `json:"themaVersion"`
	// WasmSHA256 is the digest of thema.wasm, which serve uses as its ETag.
	WasmSHA256 string `json:"wasmSHA256"`
}

// Bundle returns the playground's files, or an error saying how to build
// them into this program.
func Bundle() (fs.FS, error) {
	return bundle()
}
