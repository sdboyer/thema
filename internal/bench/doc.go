// Package bench compares ways of going from JSON bytes to a populated Go
// value of a lineage's schema, run in parallel: encoding/json alone, a JSON
// Schema exported from the lineage then encoding/json, thema's typed path,
// and CUE with the schema closed once. Nothing is translated.
//
// Order, in types.go, is what thema lineage gen gotypes writes for version 0.0
// of the lineage in testdata/order; TestTypesCurrent fails when it is not.
//
//	go test ./internal/bench -run '^$' -bench . -cpu 1,2,4,8
package bench

//go:generate go run ../../cmd/thema lineage gen gotypes -l testdata/order -v 0.0 --pkgname bench -o .
