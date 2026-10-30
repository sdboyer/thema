package thema_test

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/diagtest"
	"github.com/sdboyer/thema/load"
)

const (
	ship00 = "Schema: {\n\ta: string\n}\n"
	ship01 = "Schema: {\n\ta:  string\n\tc?: int\n}\n"
	ship10 = `Schema: {
	a:  string
	b:  int
	c?: int
}

ForwardLens: {
	v0_1: _
	v1_0: {
		b: -1
	}
	lacunas: {
		b: [{
			type:    Placeholder
			message: "-1 is a placeholder"
		}]
	}
}
`
	ship11 = "Schema: {\n\ta:  string\n\tb:  int\n\tc?: int\n\td?: bool\n}\n"
	ship20 = `Schema: {
	b: int
}

ForwardLens: {
	v1_1: _
	v2_0: {
		a: _drop
		c: _drop
		d: _drop
	}
}

BackwardLens: {
	v2_0: _
	v1_1: {
		a: "unknown"
	}
	lacunas: {
		a: [{
			type:    Placeholder
			message: "a is a placeholder"
		}]
	}
}
`
)

// revision binds a revision of a lineage in the authoring form, named name,
// whose history holds files, each a version's number and text, from history/
// alone, as it is published.
func revision(t *testing.T, rt *thema.Runtime, name string, files map[string]string) thema.Lineage {
	t.Helper()
	fsys := fstest.MapFS{}
	for v, text := range files {
		fsys[name+"/history/"+v+".thema"] = &fstest.MapFile{Data: []byte(text)}
	}
	lin, err := load.AuthoringLineage(fsys, name, rt)
	if err != nil {
		t.Fatalf("%s does not bind: %v", name, diag.Diagnostics(err))
	}
	return lin
}

// published is the revision on record: 0.0, 0.1 and 1.0.
func published() map[string]string {
	return map[string]string{"0.0": ship00, "0.1": ship01, "1.0": ship10}
}

func with(files map[string]string, changes map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range files {
		out[k] = v
	}
	for k, v := range changes {
		if v == "" {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}

type finding struct {
	code diag.Code
	file string // the primary position's file; "" for none
}

func TestCheckAppendOnly(t *testing.T) {
	commented := "// The ship.\nSchema: {\n\t// a is its name.\n\ta: string // always\n}\n"
	fixedLens := `Schema: {
	a:  string
	b:  int
	c?: int
}

ForwardLens: {
	v0_1: _
	v1_0: {
		b: -2
	}
	lacunas: {
		b: [{
			type:    Placeholder
			message: "-2 is a placeholder"
		}]
	}
}
`
	// changedSchema is 1.0 accepting more of b: a change to it, and a lineage
	// that still binds.
	changedSchema := strings.Replace(ship10, "\tb:  int\n", "\tb:  int | string\n", 1)
	// noC is 1.0 without 0.1's c.
	noC := strings.Replace(ship10, "\tc?: int\n", "", 1)
	tests := map[string]struct {
		name      string            // the candidate's name; ship if empty
		published map[string]string // the published history; published() if nil
		candidate map[string]string // the candidate's history
		want      []finding
	}{
		"identical":      {candidate: published()},
		"appended minor": {candidate: with(published(), map[string]string{"1.1": ship11})},
		"appended major": {candidate: with(published(), map[string]string{"1.1": ship11, "2.0": ship20})},
		"comments only":  {candidate: with(published(), map[string]string{"0.0": commented})},
		"changed schema": {candidate: with(published(), map[string]string{"1.0": changedSchema}), want: []finding{{diag.E0250, "history/1.0.thema"}}},
		"changed lens":   {candidate: with(published(), map[string]string{"1.0": fixedLens}), want: []finding{{diag.E0251, "history/1.0.thema"}}},
		"removed":        {candidate: with(published(), map[string]string{"1.0": ""}), want: []finding{{diag.E0252, ""}}},
		"renamed":        {name: "boat", candidate: published(), want: []finding{{diag.E0254, "history/1.0.thema"}}},
		"inserted": {
			published: map[string]string{"0.0": ship00, "1.0": strings.Replace(noC, "v0_1", "v0_0", 1)},
			candidate: map[string]string{"0.0": ship00, "0.1": "Schema: {\n\ta:  string\n\tb?: int\n}\n", "1.0": noC},
			// 1.0's derived BackwardLens now copies b to 0.1, where it
			// dropped it on the way to 0.0.
			want: []finding{{diag.E0251, "history/1.0.thema"}, {diag.E0253, "history/0.1.thema"}},
		},
		// Removing 0.1 also changes both of 1.0's lenses: the copies they
		// imply are of 0.0's fields, not 0.1's.
		"every violation": {name: "boat", candidate: map[string]string{"0.0": ship00, "1.0": strings.Replace(changedSchema, "v0_1", "v0_0", 1)}, want: []finding{{diag.E0254, "history/1.0.thema"}, {diag.E0252, ""}, {diag.E0250, "history/1.0.thema"}, {diag.E0251, "history/1.0.thema"}, {diag.E0251, "history/1.0.thema"}}},
	}
	rt := thema.NewRuntime(cuecontext.New())
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.published == nil {
				tt.published = published()
			}
			pub := revision(t, rt, "ship", tt.published)
			lname := tt.name
			if lname == "" {
				lname = "ship"
			}
			err := thema.CheckAppendOnly(pub, revision(t, rt, lname, tt.candidate))
			ds := diag.Diagnostics(err)
			var got []finding
			for _, d := range ds {
				f := finding{code: d.Code}
				if d.Primary != nil {
					f.file = d.Primary.File
				}
				got = append(got, f)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("found %+v, want %+v:\n%+v", got, tt.want, ds)
			}
			diagtest.Lint(t, ds, diagtest.Authoring)
		})
	}
}

// A version's identity covers its schema and its lenses separately: a fixed
// lens leaves the schema digest alone, and comments change neither.
func TestVersionID(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	id := func(lin thema.Lineage, v thema.SyntacticVersion) thema.VersionID {
		t.Helper()
		got, err := thema.SchemaP(lin, v).ID()
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	pub := revision(t, rt, "ship", published())
	fixed := revision(t, rt, "ship", with(published(), map[string]string{"1.0": `Schema: {
	a:  string
	b:  int
	c?: int
}

ForwardLens: {
	v0_1: _
	v1_0: {
		b: 0
	}
}
`}))
	commented := revision(t, rt, "ship", with(published(), map[string]string{"1.0": "// The ship, with b.\n" + ship10}))

	was, now := id(pub, thema.SV(1, 0)), id(fixed, thema.SV(1, 0))
	if was.Schema != now.Schema || was.Lens == now.Lens {
		t.Errorf("fixing 1.0's lens: %s, then %s; want the schema digest kept and the lens digest changed", was, now)
	}
	if was != id(commented, thema.SV(1, 0)) {
		t.Error("a comment changed 1.0's identity")
	}
	if a, b := id(pub, thema.SV(0, 0)), id(pub, thema.SV(0, 1)); a.Schema == b.Schema {
		t.Errorf("0.0 and 0.1 have one schema digest, %s", a.Schema)
	}
	if s := was.String(); len(s) != 64+1+64 || s[64] != ':' {
		t.Errorf("ID %q is not <schema>:<lens>", s)
	}
}

// The tip is never part of the check: a candidate bound with its tip is
// compared by its history, and so is a published lineage.
func TestCheckAppendOnlyIgnoresTip(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	fsys := fstest.MapFS{}
	for v, text := range published() {
		fsys["ship/history/"+v+".thema"] = &fstest.MapFile{Data: []byte(text)}
	}
	fsys["ship/tip.thema"] = &fstest.MapFile{Data: []byte(ship11)}
	dev, err := load.DevelopmentLineage(fsys, "ship", rt)
	if err != nil {
		t.Fatal(diag.Diagnostics(err))
	}
	if dev.Tip.Clean {
		t.Fatal("the tip is clean; the test needs a provisional version")
	}
	pub := revision(t, rt, "ship", published())
	if err := thema.CheckAppendOnly(pub, dev.Lineage); err != nil {
		t.Errorf("a candidate's tip was checked: %v", diag.Diagnostics(err))
	}
	if err := thema.CheckAppendOnly(dev.Lineage, pub); err != nil {
		t.Errorf("a published lineage's tip was checked: %v", diag.Diagnostics(err))
	}
}
