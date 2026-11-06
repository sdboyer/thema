// Package bench compares ways of going from JSON bytes to a populated Go
// value of a lineage's schema, run in parallel: encoding/json alone; JSON
// Schema validators, given the schema thema exports from the lineage, then
// encoding/json; Go types go-jsonschema generates from that schema, which
// validate as they unmarshal; thema's typed path; and CUE with the schema
// closed once. Nothing is translated.
//
// Order, in types.go, is what thema lineage gen gotypes writes for version 0.0
// of the lineage in testdata/order, and jsgen is what go-jsonschema generates
// from testdata/order.schema.json, the JSON Schema thema exports for it.
// TestTypesCurrent and TestSchemaCurrent fail when either has drifted.
//
// It is a module of its own, so that the validators it compares are not
// dependencies of thema:
//
//	cd internal/bench && go test -run '^$' -bench . -cpu 1,2,4,6,12
package bench

//go:generate go run github.com/sdboyer/thema/cmd/thema lineage gen gotypes -l testdata/order -v 0.0 --pkgname bench -o .
//go:generate sh -c "go run github.com/sdboyer/thema/cmd/thema lineage gen jsonschema -l testdata/order > testdata/order.schema.json && echo >> testdata/order.schema.json"
//go:generate go run github.com/atombender/go-jsonschema@v0.24.1 -p jsgen -o jsgen/order.go testdata/order.schema.json
