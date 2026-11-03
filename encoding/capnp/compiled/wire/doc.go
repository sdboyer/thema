// Package wire holds the Cap'n Proto bindings for the compiled-lineage
// artifact, generated from compiled.capnp. The schema is the published
// language (compiled-artifact in .thresh/thresh.cue); the Go here is derived
// from it and is regenerated, never edited.
//
// Regenerate with go generate, which needs the capnp tool on PATH; the
// capnpc-go plugin and its /go.capnp come from the go-capnp module.
package wire

//go:generate sh -c "capnp compile -I $(go list -m -f '{{.Dir}}' capnproto.org/go/capnp/v3)/std -o- compiled.capnp | go run capnproto.org/go/capnp/v3/capnpc-go"
