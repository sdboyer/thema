package cuetil

import (
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// constraint returns the constraint type of the field labeled name in v.
func constraint(t *testing.T, v cue.Value, name string) cue.SelectorType {
	t.Helper()
	iter, err := v.Fields(cue.Optional(true))
	if err != nil {
		t.Fatal(err)
	}
	for iter.Next() {
		if iter.Selector().Unquoted() == name {
			return iter.Selector().ConstraintType()
		}
	}
	t.Fatalf("no field %s in %v", name, v)
	return 0
}

func TestRequiredPresent(t *testing.T) {
	ctx := cuecontext.New()
	sch := ctx.CompileString(`{
	k!: "U"
	r: "R"
	n: {k!: "U"}
	m!: {k!: "U"}
	f: [{k!: "U"}]
	l: [...{k!: "U"}]
	o?: {k!: "U"}
}`)
	got := RequiredPresent(sch)
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		v    cue.Value
		name string
		want cue.SelectorType
	}{
		{got, "k", 0},
		{got, "r", 0},
		{got, "m", 0},
		{got.LookupPath(cue.ParsePath("n")), "k", 0},
		{got.LookupPath(cue.ParsePath("m")), "k", 0},
		{got.LookupPath(cue.ParsePath("f[0]")), "k", 0},
		// An instance may have no o, and no element of l.
		{got, "o", cue.OptionalConstraint},
		{got.LookupPath(cue.MakePath(cue.Str("o").Optional())), "k", cue.RequiredConstraint},
		{got.LookupPath(cue.MakePath(cue.Str("l"), cue.AnyIndex)), "k", cue.RequiredConstraint},
		// sch itself is unchanged.
		{sch, "k", cue.RequiredConstraint},
		{sch.LookupPath(cue.ParsePath("n")), "k", cue.RequiredConstraint},
	} {
		if ct := constraint(t, c.v, c.name); ct != c.want {
			t.Errorf("%v: %s has constraint %v, want %v", c.v.Path(), c.name, ct, c.want)
		}
	}

	// A reference to a required field of the result is the field's value, so a
	// copy of it is checked against the target like any other value: it unifies
	// with the same constant, and still conflicts with a different one.
	lens := ctx.CompileString(`{in: _, out: {k: in.k, n: k: in.n.k, f: [{k: in.f[0].k}]}}`)
	out := lens.FillPath(cue.ParsePath("in"), got).LookupPath(cue.ParsePath("out"))
	same := ctx.CompileString(`{k!: "U", n: {k!: "U"}, f: [{k!: "U"}]}`)
	if err := out.Unify(same).Validate(cue.Concrete(true)); err != nil {
		t.Errorf("copy of required constants is not an instance of the same schema: %v", err)
	}
	for _, other := range []string{
		`{k!: "V", n: {k!: "U"}, f: [{k!: "U"}]}`,
		`{k!: "U", n: {k!: "V"}, f: [{k!: "U"}]}`,
		`{k!: "U", n: {k!: "U"}, f: [{k!: "V"}]}`,
		`{k!: int, n: {k!: "U"}, f: [{k!: "U"}]}`,
	} {
		if err := out.Unify(ctx.CompileString(other)).Validate(); err == nil {
			t.Errorf("copy of required constants unified with %s", other)
		}
	}
}
