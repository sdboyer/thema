package openapi

import (
	"testing"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/encoding/json"
)

func TestRestoreNullableFormats(t *testing.T) {
	for _, tc := range []struct {
		name, in, out string
	}{
		{
			name: "int32",
			in:   `{"type": "integer", "minimum": -2147483648, "maximum": 2147483647, "nullable": true}`,
			out:  `{"type": "integer", "format": "int32", "nullable": true}`,
		},
		{
			name: "double",
			in:   `{"type": "number", "minimum": -1.797693134862315708145274237317043567981E+308, "maximum": 1.797693134862315708145274237317043567981E+308, "nullable": true}`,
			out:  `{"type": "number", "format": "double", "nullable": true}`,
		},
		{
			name: "float",
			in:   `{"type": "number", "minimum": -340282346638528859811704183484516925440, "maximum": 340282346638528859811704183484516925440, "nullable": true}`,
			out:  `{"type": "number", "format": "float", "nullable": true}`,
		},
		{
			name: "nested",
			in:   `{"properties": {"a": {"type": "integer", "minimum": -9223372036854775808, "maximum": 9223372036854775807, "nullable": true}}}`,
			out:  `{"properties": {"a": {"type": "integer", "format": "int64", "nullable": true}}}`,
		},
		{
			name: "not nullable",
			in:   `{"type": "integer", "minimum": -2147483648, "maximum": 2147483647}`,
			out:  `{"type": "integer", "minimum": -2147483648, "maximum": 2147483647}`,
		},
		{
			name: "narrower bounds",
			in:   `{"type": "integer", "minimum": 0, "maximum": 2147483647, "nullable": true}`,
			out:  `{"type": "integer", "minimum": 0, "maximum": 2147483647, "nullable": true}`,
		},
		{
			name: "integer bounds on number",
			in:   `{"type": "number", "minimum": -2147483648, "maximum": 2147483647, "nullable": true}`,
			out:  `{"type": "number", "minimum": -2147483648, "maximum": 2147483647, "nullable": true}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, err := json.Extract("in", []byte(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			restoreNullableFormats(in)
			want, err := json.Extract("out", []byte(tc.out))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := fmtExpr(t, in), fmtExpr(t, want); got != want {
				t.Errorf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// fmtExpr renders x as JSON, so that equivalent label spellings compare equal
// while field order still matters.
func fmtExpr(t *testing.T, x ast.Expr) string {
	t.Helper()
	b, err := cuecontext.New().BuildExpr(x).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
