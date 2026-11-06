// Package datatypes holds the Go types generated for the schema of each
// archive of the data corpus (testdata/data), one package per archive, named
// for it, and [Registry], which binds each to its schema. TestData in
// internal/corpus runs the corpus through them, and TestDataTypes generates
// them: run it with THEMA_UPDATE_GOLDEN=1 after adding an archive or changing
// its schema.
package datatypes

import (
	"encoding/json"

	"cuelang.org/go/cue"

	"github.com/sdboyer/thema"
)

// Typed is the typed API bound to one schema, with its Go type erased, so
// that a suite runs every archive alike. Each value is a pointer to the type.
type Typed struct {
	// Value validates data with ValidateTyped, and returns what Value gives.
	Value func(data cue.Value) (any, error)
	// FromJSON decodes JSON onto a new value of the type, with encoding/json.
	FromJSON func(b []byte) (any, error)
}

// Bind binds T, the Go type generated for sch, to sch with [thema.BindType].
func Bind[T any](sch thema.Schema) (Typed, error) {
	tsch, err := thema.BindType(sch, new(T))
	if err != nil {
		return Typed{}, err
	}
	return Typed{
		Value: func(data cue.Value) (any, error) {
			inst, err := tsch.ValidateTyped(data)
			if err != nil {
				return nil, err
			}
			return inst.Value()
		},
		FromJSON: func(b []byte) (any, error) {
			v := new(T)
			err := json.Unmarshal(b, v)
			return v, err
		},
	}, nil
}
