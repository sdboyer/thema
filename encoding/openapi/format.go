package openapi

import (
	"math/big"
	"strconv"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"
)

// nullableFormats are the OpenAPI formats that the generator derives from CUE's
// sized numeric types, keyed by schema type, with the bounds those types imply.
var nullableFormats = map[string][]struct {
	format   string
	min, max string
}{
	"integer": {
		{"int32", "-2147483648", "2147483647"},
		{"int64", "-9223372036854775808", "9223372036854775807"},
	},
	"number": {
		{"float", "-3.40282346638528859811704183484516925440e+38", "3.40282346638528859811704183484516925440e+38"},
		{"double", "-1.797693134862315708145274237317043567981e+308", "1.797693134862315708145274237317043567981e+308"},
	},
}

// restoreNullableFormats rewrites nullable numeric schemas whose bounds are
// exactly those of a sized CUE type (e.g. int32 | null) to use the
// corresponding format, as the generator does for the non-nullable type.
//
// The generator only extracts a format from a value as a whole, and int32 |
// null is a disjunction, so it falls back to emitting int32's bounds as
// minimum and maximum. The two are equivalent; the format is what consumers
// (and code generators) expect.
func restoreNullableFormats(n ast.Node) {
	ast.Walk(n, func(n ast.Node) bool {
		if s, ok := n.(*ast.StructLit); ok {
			restoreNullableFormat(s)
		}
		return true
	}, nil)
}

func restoreNullableFormat(s *ast.StructLit) {
	var typ string
	var nullable bool
	var min, max *ast.Field
	for _, d := range s.Elts {
		f, ok := d.(*ast.Field)
		if !ok {
			continue
		}
		switch fieldLabel(f) {
		case "format":
			return
		case "type":
			if lit, ok := f.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				typ, _ = strconv.Unquote(lit.Value)
			}
		case "nullable":
			if lit, ok := f.Value.(*ast.BasicLit); ok {
				nullable = lit.Kind == token.TRUE
			}
		case "minimum":
			min = f
		case "maximum":
			max = f
		}
	}
	if !nullable || min == nil || max == nil {
		return
	}

	for _, nf := range nullableFormats[typ] {
		if !litEquals(min.Value, nf.min) || !litEquals(max.Value, nf.max) {
			continue
		}
		elts := s.Elts[:0]
		for _, d := range s.Elts {
			switch d {
			case ast.Decl(min):
				elts = append(elts, &ast.Field{Label: ast.NewString("format"), Value: ast.NewString(nf.format)})
			case ast.Decl(max):
			default:
				elts = append(elts, d)
			}
		}
		s.Elts = elts
		return
	}
}

func fieldLabel(f *ast.Field) string {
	name, _, _ := ast.LabelName(f.Label)
	return name
}

// litEquals reports whether x is a numeric literal exactly equal to the decimal
// number in num. Negative numbers may be either a literal or a negated one.
func litEquals(x ast.Expr, num string) bool {
	sign := ""
	if u, ok := x.(*ast.UnaryExpr); ok && u.Op == token.SUB {
		sign, x = "-", u.X
	}
	lit, ok := x.(*ast.BasicLit)
	if !ok || (lit.Kind != token.INT && lit.Kind != token.FLOAT) {
		return false
	}
	a, ok := new(big.Rat).SetString(sign + lit.Value)
	if !ok {
		return false
	}
	b, _ := new(big.Rat).SetString(num)
	return a.Cmp(b) == 0
}
