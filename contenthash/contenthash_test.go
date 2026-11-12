package contenthash_test

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/contenthash"
)

func compile(t *testing.T, ctx *cue.Context, src string) cue.Value {
	t.Helper()
	v := ctx.CompileString(src)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	return v
}

func mustOf(t *testing.T, v cue.Value) contenthash.Digest {
	t.Helper()
	d, err := contenthash.Of(v)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Comments, layout, the order of fields and the names of lets don't change a
// value's digest; what the value means does.
func TestOf(t *testing.T) {
	ctx := cuecontext.New()
	base := mustOf(t, compile(t, ctx, `{route: "GET /things", body: "g2.ab", size: int & >0}`))

	let := mustOf(t, compile(t, ctx, `{let r = {m: "GET"}, route: r.m, body: "g2.ab", size: int & >0}`))
	if renamed := mustOf(t, compile(t, ctx, `{let other = {m: "GET"}, route: other.m, body: "g2.ab", size: int & >0}`)); renamed != let {
		t.Errorf("a renamed let: digest %s, want %s", renamed, let)
	}

	for name, src := range map[string]string{
		"comments and layout": `
// The route.
route: "GET /things"
body:  "g2.ab" // an ID
size:  int & >0
`,
		"field order":   `{size: int & >0, body: "g2.ab", route: "GET /things"}`,
		"operand order": `{route: "GET /things", body: "g2.ab", size: >0 & int}`,
	} {
		if got := mustOf(t, compile(t, ctx, src)); got != base {
			t.Errorf("%s: digest %s, want %s", name, got, base)
		}
	}

	for name, src := range map[string]string{
		"a changed value":      `{route: "GET /others", body: "g2.ab", size: int & >0}`,
		"a changed constraint": `{route: "GET /things", body: "g2.ab", size: int & >=0}`,
		"an optional field":    `{route: "GET /things", body?: "g2.ab", size: int & >0}`,
		"an added field":       `{route: "GET /things", body: "g2.ab", size: int & >0, extra: 1}`,
	} {
		if got := mustOf(t, compile(t, ctx, src)); got == base {
			t.Errorf("%s: digest unchanged, %s", name, got)
		}
	}
}

// Of is the digest of Form, tagged with this build's generation.
func TestOfIsFormDigest(t *testing.T) {
	v := compile(t, cuecontext.New(), `{a: string, b?: [...int]}`)
	form, err := contenthash.Form(v)
	if err != nil {
		t.Fatal(err)
	}
	d := mustOf(t, v)
	if d.Sum != sha256.Sum256([]byte(form)) {
		t.Errorf("Of's sum is not the SHA-256 of Form %q", form)
	}
	if d.Generation != contenthash.Generation {
		t.Errorf("generation %d, want %d", d.Generation, contenthash.Generation)
	}
	if tag := fmt.Sprintf("g%d", contenthash.Generation); !strings.HasPrefix(d.String(), tag) {
		t.Errorf("%s does not have the tag %s", d, tag)
	}
}

// A schema's digest from Of is the schema half of its version's ID: the
// rules are the same.
func TestOfMatchesSchemaID(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	src := `
name: "things"
schemas: {
	"0": {"0": {schema: {
		// A comment that doesn't count.
		a:  string
		b?: int & >0
	}}}
}
lenses: {}
`
	val := compile(t, rt.Context(), src)
	lin, err := thema.BindLineage(val, cue.Path{}, rt)
	if err != nil {
		t.Fatal(err)
	}
	id, err := lin.First().ID()
	if err != nil {
		t.Fatal(err)
	}
	got := mustOf(t, val.LookupPath(cue.ParsePath(`schemas."0"."0".schema`)))
	if got != id.Schema {
		t.Errorf("Of(schema) = %s, want the schema digest %s", got, id.Schema)
	}
}

// A value that holds an error has no canonical form.
func TestOfError(t *testing.T) {
	v := cuecontext.New().CompileString(`{a: 1 & 2}`)
	if _, err := contenthash.Of(v); err == nil {
		t.Error("Of of a value with an error: no error")
	}
	if _, err := contenthash.Form(v); err == nil {
		t.Error("Form of a value with an error: no error")
	}
}
