package diag

import (
	"strings"

	cerrors "cuelang.org/go/cue/errors"
)

// The shapes of binding a lineage that CUE's evaluation of thema's own CUE
// produces. thema's Go checks catch most of what the kernel guards do before
// the kernel does, and point at the author's line; these shapes describe what
// still reaches the kernel: a lacuna's fields and type, which the kernel
// checks with error() and a disjunction, and the lineage's name.

// The kernel's guard messages. error() in CUE makes the interpolated message
// the format, with %v for each interpolation.
const (
	msgNotLacunaField  = "%v is not a field of a lacuna; a lacuna has only condition, sourceFields, targetFields, message and type"
	msgNotLineageField = "%v is not a field of a lineage; a lineage has only name, joinSchema, schemas and lenses"
	msgNotSchemaField  = "%v is not a field of a schema; a schema entry has only schema and examples"
	msgNotLensField    = "%v is not a field of a lens; the lens from %v.%v to %v.%v has only %v, %v and lacunas"
	msgNotMinorVersion = "%v is not a minor version; schemas are keyed by major, then minor version, each a non-negative integer such as \"0\""
	msgNotMajorVersion = "%v is not a major version; schemas are keyed by major, then minor version, each a non-negative integer such as \"0\""
	msgNotLensEdge     = "%v is not a lens edge; lenses are keyed by \"<from>-><to>\" versions, such as \"1.0->0.1\""
)

func init() {
	shapes = append(shapes,
		shape{name: "bind-lacuna-unknown-field", msg: msgNotLacunaField, kernel: true, ops: []Op{OpBind}, build: bindUnknownField(E0407, "a lacuna", []string{"condition", "sourceFields", "targetFields", "message", "type"})},
		shape{name: "bind-lineage-unknown-field", msg: msgNotLineageField, kernel: true, ops: []Op{OpBind}, build: bindUnknownField(E0123, "a lineage", []string{"name", "joinSchema", "schemas", "lenses"})},
		shape{name: "bind-schema-unknown-field", msg: msgNotSchemaField, kernel: true, ops: []Op{OpBind}, build: bindUnknownField(E0123, "a schema entry", []string{"schema", "examples"})},
		shape{name: "bind-lens-unknown-field", msg: msgNotLensField, kernel: true, ops: []Op{OpBind}, build: bindUnknownField(E0123, "a lens", nil)},
		shape{name: "bind-not-minor-version", msg: msgNotMinorVersion, kernel: true, ops: []Op{OpBind}, build: bindBadKey(E0124, "a minor version")},
		shape{name: "bind-not-major-version", msg: msgNotMajorVersion, kernel: true, ops: []Op{OpBind}, build: bindBadKey(E0124, "a major version")},
		shape{name: "bind-not-lens-edge", msg: msgNotLensEdge, kernel: true, ops: []Op{OpBind}, build: bindBadKey(E0125, "a lens edge")},
		shape{name: "bind-name-pattern", msg: msgOutOfBound, ops: []Op{OpBind}, build: bindNamePattern},
	)
	disjunctionShapes[OpBind] = bindDisjunction
}

// bindUnknownField: the kernel's error() guard on a field thema does not
// define. The field's name is the message's argument; the position CUE gives
// is the guard's, so the author's comes from the lineage value at the path.
func bindUnknownField(code Code, what string, known []string) func(e cerrors.Error, ctx Context) *B {
	return func(e cerrors.Error, ctx Context) *B {
		_, args := e.Msg()
		name := ""
		if len(args) > 0 {
			name = argText(args[0])
		}
		// A lens's fields are named after its versions, which the guard's
		// message carries as its sixth and seventh arguments.
		if known == nil && len(args) == 7 {
			known = []string{argText(args[5]), argText(args[6]), "lacunas"}
		}
		path := ctx.path(e)
		b := New(code).Headlinef("%s is not a field of %s", name, what)
		if a, ok := ctx.addressOf(path); ok {
			b.Addr(a)
		}
		if want, ok := Suggest(name, known); ok {
			b.Helpf("did you mean %s?", want)
		} else {
			b.Helpf("%s has only %s", what, strings.Join(known, ", "))
		}
		b.declareWith = "declared here"
		return b
	}
}

// bindBadKey: the kernel's error() guard on a schemas or lenses key.
func bindBadKey(code Code, what string) func(e cerrors.Error, ctx Context) *B {
	return func(e cerrors.Error, ctx Context) *B {
		_, args := e.Msg()
		key := ""
		if len(args) == 1 {
			key = argText(args[0])
		}
		b := New(code).Headlinef("%q is not %s", key, what)
		if a, ok := ctx.addressOf(ctx.path(e)); ok {
			b.Addr(a)
		}
		switch code {
		case E0124:
			b.Help(`schemas are keyed by major, then minor version, each a non-negative integer such as "0"`)
		case E0125:
			b.Help(`lenses are keyed by "<from>-><to>" versions, such as "1.0->0.1"`)
		}
		return b
	}
}

// bindNamePattern: the lineage's name fails the kernel's identifier pattern.
// Any other out-of-bound error at bind is not this shape.
func bindNamePattern(e cerrors.Error, ctx Context) *B {
	path := ctx.path(e)
	if len(path) == 0 || path[len(path)-1] != "name" {
		return nil
	}
	_, args := e.Msg()
	b := New(E0137).Addr(Address{Kind: AddrName})
	if len(args) == 2 {
		b.Headlinef("the lineage's name is %s, which is not an identifier", oneLine(argText(args[0])))
	}
	b.Help("a name starts with a letter and continues with letters and digits")
	b.declareWith = "declared here"
	return b
}

// bindDisjunction: at bind, an empty disjunction is a lacuna's type failing
// the kernel's or() of the lacuna types; anything else is generic.
func bindDisjunction(header cerrors.Error, arms []cerrors.Error, ctx Context) *B {
	path := ctx.path(header)
	if len(path) < 2 || path[len(path)-1] != "type" || !hasSegment(path, "lacunas") {
		return nil
	}
	// Each arm conflicts the author's value with one lacuna type. The value
	// every arm shares is the author's; the others are the types.
	count := map[string]int{}
	var order []string
	for _, arm := range arms {
		_, args := arm.Msg()
		for _, a := range args[:min(2, len(args))] {
			s := argText(a)
			if count[s] == 0 {
				order = append(order, s)
			}
			count[s]++
		}
	}
	wrote, types := "", []string{}
	for _, s := range order {
		if count[s] == len(arms) && len(arms) > 1 {
			wrote = s
		} else {
			types = append(types, s)
		}
	}
	b := New(E0406)
	if a, ok := ctx.addressOf(path); ok {
		b.Addr(a)
	}
	if wrote != "" {
		b.Headlinef("the lacuna's type is %s, which is not a lacuna type", wrote)
	} else {
		b.Headline("the lacuna's type is not a lacuna type")
	}
	if len(types) > 0 {
		b.Helpf("the lacuna types are %s", strings.Join(types, ", "))
	}
	b.declareWith = "declared here"
	b.dropArms = true
	return b
}

func hasSegment(path []string, s string) bool {
	for _, p := range path {
		if p == s {
			return true
		}
	}
	return false
}

// addressOf applies the context's AddressOf, if any.
func (ctx Context) addressOf(path []string) (Address, bool) {
	if ctx.AddressOf == nil {
		return Address{}, false
	}
	return ctx.AddressOf(path)
}
