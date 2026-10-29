package diag

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	cerrors "cuelang.org/go/cue/errors"
)

func splitDisjuncts(s string) []string    { return strings.Split(s, " | ") }
func joinDisjuncts(parts []string) string { return strings.Join(parts, " | ") }

// The shapes of validating data against a schema. In every headline, the
// subject is the field in the data, and the schema is named by its version:
// "someInt16 in the data is null, but schema 0.0 declares it as int".

func schemaName(ctx Context) string {
	return "schema " + ctx.Base.Version.String()
}

func dataTerm(ctx Context, path []string) string {
	return ctx.vocab().Term(dataAddr(ctx, path))
}

// validateAbsent: "field is required but not present". The data lacks a
// field the schema requires.
func validateAbsent(e cerrors.Error, ctx Context) *B {
	path := ctx.path(e)
	b := New(E0501).
		Headlinef("the data lacks %s, which %s requires", joinPath(path), schemaName(ctx)).
		Addr(dataAddr(ctx, path))
	schemaNote(b, ctx, path, "required here")
	return b
}

// validateIncomplete: "incomplete value %v". With a path, the data lacks a
// field the schema declares without a default, so the value stays the
// schema's constraint; without one, the data itself is not concrete.
func validateIncomplete(e cerrors.Error, ctx Context) *B {
	path := ctx.path(e)
	_, args := e.Msg()
	typ := ""
	if len(args) == 1 {
		typ = typeName(argText(args[0]))
	}
	if len(path) == 0 {
		return New(E0506).
			Headlinef("the data is not concrete: it is still %s", oneLine(typ)).
			Addr(dataAddr(ctx, nil))
	}
	// A field the data has is there but not concrete.
	if ctx.DataValue != nil {
		if _, ok := ctx.DataValue(path); ok {
			b := New(E0506).
				Headlinef("%s is not concrete: it is still %s", dataTerm(ctx, path), oneLine(typ)).
				Addr(dataAddr(ctx, path))
			schemaNote(b, ctx, path, "declared here")
			return b
		}
	}
	b := New(E0501).
		Headlinef("the data lacks %s, which %s declares as %s", joinPath(path), schemaName(ctx), oneLine(typ)).
		Addr(dataAddr(ctx, path))
	schemaNote(b, ctx, path, "declared here")
	return b
}

// validateNotAllowed: "field not allowed". The data has a field the schema
// does not declare. Schemas are closed when validating.
func validateNotAllowed(e cerrors.Error, ctx Context) *B {
	path := ctx.path(e)
	name := ""
	if len(path) > 0 {
		name = unquotePath(path)[len(path)-1]
	}
	b := New(E0502).
		Headlinef("the data has %s, which %s does not declare", joinPath(path), schemaName(ctx)).
		Addr(dataAddr(ctx, path))
	if len(path) > 1 {
		schemaNote(b, ctx, path[:len(path)-1], fmt.Sprintf("%s declares the fields of %s here", schemaName(ctx), joinPath(path[:len(path)-1])))
	} else {
		schemaNote(b, ctx, nil, schemaName(ctx)+" declares its fields here")
	}
	if want, ok := Suggest(name, declaredFields(ctx, path)); ok {
		b.Helpf("did you mean %s?", want)
	} else {
		b.Helpf("remove %s from the data, or declare it in the schema", name)
	}
	return b
}

// validateKind: "conflicting values %s and %s (mismatched types %s and %s)".
// The data's value is of a kind the schema does not accept. The headline
// names the data's value, as dataValueText writes it.
func validateKind(e cerrors.Error, ctx Context) *B {
	path := ctx.path(e)
	_, args := e.Msg()
	d, ok := dataSide(e, ctx)
	s := 1 - d
	b := New(E0503).Addr(dataAddr(ctx, path))
	if len(args) == 4 && ok {
		b.Headlinef("%s is %s, but %s declares it as %s", dataTerm(ctx, path), dataValueText(args[d], args[2+d]), schemaName(ctx), typeName(argText(args[s])))
	} else {
		b.Headlinef("%s is of a kind %s does not accept", dataTerm(ctx, path), schemaName(ctx))
	}
	schemaNote(b, ctx, path, "declared here")
	return b
}

// dataValueText writes the data's value for a headline: a scalar as CUE
// writes it, shortened to one line; a struct or a list by its kind, since
// its contents rarely fit and the conflict is with its kind.
func dataValueText(value, kind any) string {
	switch k, _ := kind.(cue.Kind); k {
	case cue.StructKind:
		return "a struct"
	case cue.ListKind:
		return "a list"
	}
	return oneLine(argText(value))
}

// validateConflict: "conflicting values %s and %s", of the same kind. The
// schema fixes a value and the data has another.
func validateConflict(e cerrors.Error, ctx Context) *B {
	path := ctx.path(e)
	_, args := e.Msg()
	d, ok := dataSide(e, ctx)
	b := New(E0504).Addr(dataAddr(ctx, path))
	if len(args) == 2 && ok {
		b.Headlinef("%s is %s, but %s requires %s", dataTerm(ctx, path), oneLine(argText(args[d])), schemaName(ctx), oneLine(typeName(argText(args[1-d]))))
	} else {
		b.Headlinef("%s has a value %s does not accept", dataTerm(ctx, path), schemaName(ctx))
	}
	schemaNote(b, ctx, path, "required here")
	return b
}

// validateBound: "invalid value %v (out of bound %s)". The data's value is
// outside a bound the schema sets, or fails a pattern.
func validateBound(e cerrors.Error, ctx Context) *B {
	path := ctx.path(e)
	_, args := e.Msg()
	b := New(E0504).Addr(dataAddr(ctx, path))
	if len(args) == 2 {
		b.Headlinef("%s is %s, outside the bound %s that %s sets", dataTerm(ctx, path), oneLine(argText(args[0])), oneLine(argText(args[1])), schemaName(ctx))
	} else {
		b.Headlinef("%s is outside a bound %s sets", dataTerm(ctx, path), schemaName(ctx))
	}
	schemaNote(b, ctx, path, "the bound is set here")
	return b
}

// validateConstraint: "invalid value %s (does not satisfy %s)". The data's
// value fails a validator the schema applies.
func validateConstraint(e cerrors.Error, ctx Context) *B {
	path := ctx.path(e)
	_, args := e.Msg()
	b := New(E0504).Addr(dataAddr(ctx, path))
	if len(args) == 2 {
		b.Headlinef("%s is %s, which fails %s in %s", dataTerm(ctx, path), oneLine(argText(args[0])), oneLine(argText(args[1])), schemaName(ctx))
	} else {
		b.Headlinef("%s fails a constraint %s applies", dataTerm(ctx, path), schemaName(ctx))
	}
	schemaNote(b, ctx, path, "the constraint is applied here")
	return b
}

// validateDisjunction: the data's value matches none of the alternatives the
// schema allows. The arms become notes through the caller.
func validateDisjunction(header cerrors.Error, _ []cerrors.Error, ctx Context) *B {
	path := ctx.path(header)
	b := New(E0505).
		Headlinef("%s matches none of the alternatives %s allows", dataTerm(ctx, path), schemaName(ctx)).
		Addr(dataAddr(ctx, path))
	schemaNote(b, ctx, path, "the alternatives are declared here")
	return b
}

// typeName gives the name an author would use for a CUE type that CUE spells
// out as bounds: uint8 for int & >=0 & <=255.
func typeName(s string) string {
	parts := splitDisjuncts(s)
	for i, p := range parts {
		if m, ok := typeNames[p]; ok {
			parts[i] = m
		}
	}
	return joinDisjuncts(parts)
}

var typeNames = map[string]string{
	"int & >=0 & <=255":                                    "uint8",
	"int & >=0 & <=65535":                                  "uint16",
	"int & >=0 & <=4294967295":                             "uint32",
	"int & >=0 & <=18446744073709551615":                   "uint64",
	">=0 & <=255 & int":                                    "uint8",
	">=0 & <=65535 & int":                                  "uint16",
	">=0 & <=4294967295 & int":                             "uint32",
	">=0 & <=18446744073709551615 & int":                   "uint64",
	"int & >=-128 & <=127":                                 "int8",
	">=-128 & <=127 & int":                                 "int8",
	"int & >=-32768 & <=32767":                             "int16",
	">=-32768 & <=32767 & int":                             "int16",
	"int & >=-2147483648 & <=2147483647":                   "int32",
	">=-2147483648 & <=2147483647 & int":                   "int32",
	"int & >=-9223372036854775808 & <=9223372036854775807": "int64",
	">=-9223372036854775808 & <=9223372036854775807 & int": "int64",
	">=-340282346638528859811704183484516925440 & <=340282346638528859811704183484516925440":               "float32",
	">=-1.797693134862315708145274237317043567981E+308 & <=1.797693134862315708145274237317043567981E+308": "float64",
}
