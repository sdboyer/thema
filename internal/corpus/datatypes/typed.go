// Package datatypes holds the Go types generated for the schema of each
// archive of the data corpus (testdata/data), one package per archive, named
// for it, and [Registry], which binds each to its schema. TestData in
// internal/corpus runs the corpus through them, and TestDataTypes generates
// them: run it with THEMA_UPDATE_GOLDEN=1 after adding an archive or changing
// its schema.
package datatypes

import (
	"errors"
	"math/big"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/sdboyer/thema"
)

// Typed is the typed API bound to one schema, with its Go type erased, so
// that a suite runs every archive alike. Each value is a pointer to the type.
type Typed struct {
	// Value validates data with ValidateTyped, and returns what Value gives.
	Value func(data cue.Value) (any, error)
	// FromJSON decodes JSON onto a new value of the type, with
	// go-json-experiment, which gives each Go field exactly the name its tag
	// gives it, never one that differs only by case. A number it decodes
	// into an any is exact: see [Number].
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
			err := json.Unmarshal(b, v, json.WithUnmarshalers(anyNumbers))
			return v, err
		},
	}, nil
}

// anyNumbers decodes a JSON number into an any as [Number] says, where
// go-json-experiment would give every one a float64.
var anyNumbers = json.UnmarshalFromFunc(func(dec *jsontext.Decoder, v *any) error {
	if dec.PeekKind() != '0' {
		return errors.ErrUnsupported
	}
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	*v, err = Number(tok.String())
	return err
})

// Number is the Go value a JSON number, n, takes where the Go type holds it
// as any: an int64, a *big.Int for an integer outside int64's range, or a
// float64 for a number with a fraction or an exponent, which is a float
// even when its value is integral (docs/translation.md, A7). It is what
// CUE's Decode gives, and every suite must give the same.
func Number(n string) (any, error) {
	if strings.ContainsAny(n, ".eE") {
		return strconv.ParseFloat(n, 64)
	}
	if i, err := strconv.ParseInt(n, 10, 64); err == nil {
		return i, nil
	}
	const decimal = 10
	i, ok := new(big.Int).SetString(n, decimal)
	if !ok {
		return nil, strconv.ErrSyntax
	}
	return i, nil
}
