package cue

import (
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/astutil"
	"github.com/sdboyer/thema/internal/util"
	tload "github.com/sdboyer/thema/load"
)

func TestNewLineage(t *testing.T) {
	tests := map[string]struct {
		schema string
		want   string
		// valid and invalid are data that the generated lineage's 0.0 schema
		// must accept and reject, respectively.
		valid, invalid string
	}{
		"simple": {
			schema: `{a: string, b: int}`,
			want: `package foo

import "github.com/sdboyer/thema"

thema.Lineage
name: "foo"
schemas: "0": "0": schema: {
	a: string
	b: int
}
`,
			valid:   `{a: "x", b: 1}`,
			invalid: `{a: 1, b: 1}`,
		},
		"nested": {
			schema: `{a: {b: {c: string}, d: [...int]}}`,
			want: `package foo

import "github.com/sdboyer/thema"

thema.Lineage
name: "foo"
schemas: "0": "0": schema: a: {
	b: c: string
	d: [...int]
}
`,
			valid:   `{a: {b: {c: "x"}, d: [1, 2]}}`,
			invalid: `{a: {b: {c: "x"}, d: ["y"]}}`,
		},
		"optional and required": {
			schema: `{a: string, b?: int, c!: bool}`,
			want: `package foo

import "github.com/sdboyer/thema"

thema.Lineage
name: "foo"
schemas: "0": "0": schema: {
	a:  string
	b?: int
	c!: bool
}
`,
			valid:   `{a: "x", c: true}`,
			invalid: `{a: "x", b: 1}`,
		},
		"comments": {
			schema: `{
	// a is documented.
	a: string
	// n is documented,
	// across two lines.
	n: {
		// x is documented too.
		x: int
	}
}`,
			want: `package foo

import "github.com/sdboyer/thema"

thema.Lineage
name: "foo"
schemas: "0": "0": schema: {
	// a is documented.
	a: string
	// n is documented,
	// across two lines.
	n: {
		// x is documented too.
		x: int
	}
}
`,
			valid:   `{a: "x", n: {x: 1}}`,
			invalid: `{a: "x", n: {x: "y"}}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			f, err := NewLineage(ctx.CompileString(tt.schema), "foo", "foo")
			if err != nil {
				t.Fatal(err)
			}
			got := string(astutil.FmtNodeP(f))
			if got != tt.want {
				t.Fatalf("generated lineage did not match expected:\n--- got\n%s\n--- want\n%s", got, tt.want)
			}

			lin := bindGenerated(t, got)
			sch := lin.Latest()
			if v := sch.Version(); v != thema.SV(0, 0) {
				t.Fatalf("expected latest version 0.0, got %s", v)
			}
			if _, err := sch.Validate(ctx.CompileString(tt.valid)); err != nil {
				t.Errorf("expected %s to validate against generated schema: %s", tt.valid, err)
			}
			if _, err := sch.Validate(ctx.CompileString(tt.invalid)); err == nil {
				t.Errorf("expected %s to fail validation against generated schema", tt.invalid)
			}
		})
	}
}

func TestNewLineageNoPackage(t *testing.T) {
	f, err := NewLineage(ctx.CompileString(`{a: string}`), "foo", "")
	if err != nil {
		t.Fatal(err)
	}
	want := `import "github.com/sdboyer/thema"

thema.Lineage
name: "foo"
schemas: "0": "0": schema: a: string
`
	if got := string(astutil.FmtNodeP(f)); got != want {
		t.Fatalf("generated lineage did not match expected:\n--- got\n%s\n--- want\n%s", got, want)
	}
}

// bindGenerated loads src as the sole file of package foo in a CUE module that
// depends on thema, and binds the lineage at its root.
func bindGenerated(t *testing.T, src string) thema.Lineage {
	t.Helper()
	mfs := fstest.MapFS{
		"cue.mod/module.cue": &fstest.MapFile{Data: []byte(util.ModuleFile("example.com/foo"))},
		"foo.cue":            &fstest.MapFile{Data: []byte(src)},
	}
	binst, err := tload.InstanceWithThema(mfs, ".")
	if err != nil {
		t.Fatal(err)
	}
	v := ctx.BuildInstance(binst)
	if err = v.Validate(cue.All()); err != nil {
		t.Fatalf("generated lineage does not evaluate: %s", err)
	}
	lin, err := thema.BindLineage(v, rt)
	if err != nil {
		t.Fatalf("generated lineage does not bind: %s", err)
	}
	return lin
}
