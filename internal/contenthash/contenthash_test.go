package contenthash

import (
	"crypto/sha256"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

func compileLineage(t *testing.T, src string) cue.Value {
	t.Helper()
	v := cuecontext.New().CompileString(src)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	return v.LookupPath(cue.ParsePath("lin"))
}

func mustID(t *testing.T, lin cue.Value, v Version) ID {
	t.Helper()
	id, err := VersionID(lin, v)
	if err != nil {
		t.Fatalf("%s: %v", v, err)
	}
	return id
}

func mustLensForm(t *testing.T, lin cue.Value, v Version) string {
	t.Helper()
	f, err := LensForm(lin, v)
	if err != nil {
		t.Fatalf("%s: %v", v, err)
	}
	return f
}

// lineage returns a lineage with versions 0.0, 0.1 and 1.0, whose lens from 1.0
// to 0.1 has the result back.
func lineage(back string) string {
	return `lin: {
	schemas: {
		"0": {
			"0": {schema: {a: string}}
			"1": {schema: {a: string, b?: int}}
		}
		"1": {"0": {schema: {a: string, b: int}}}
	}
	lenses: {
		"0.1->0.0": {
			v0_1: _
			v0_0: {a: v0_1.a}
		}
		"0.1->1.0": {
			v0_1: _
			v1_0: {a: v0_1.a, b: *v0_1.b | 0}
		}
		"1.0->0.1": {
			v1_0: _
			v0_1: ` + back + `
		}
	}
}`
}

// Which lenses each version's lens digest covers: the edge into it.
func TestLensFormEdge(t *testing.T) {
	lin := compileLineage(t, lineage(`{a: v1_0.a, b: v1_0.b}`))

	// The first version has no edge into it: its lens form is that of no
	// lenses, a constant.
	if got := mustLensForm(t, lin, Version{0, 0}); got != "{}" {
		t.Errorf("0.0: lens form %q, want {}", got)
	}
	if got, want := mustID(t, lin, Version{0, 0}).Lens, Digest(sha256.Sum256([]byte("{}"))); got != want {
		t.Errorf("0.0: lens digest %s, want sha256(\"{}\") %s", got, want)
	}

	// A minor version's forward lens is implicit: only the backward one is
	// hashed.
	minor := mustLensForm(t, lin, Version{0, 1})
	if strings.Contains(minor, "forward") || !strings.Contains(minor, "backward") {
		t.Errorf("0.1: want only a backward lens:\n%s", minor)
	}
	major := mustLensForm(t, lin, Version{1, 0})
	if !strings.Contains(major, "forward") || !strings.Contains(major, "backward") {
		t.Errorf("1.0: want a forward and a backward lens:\n%s", major)
	}
	// from and to are position, not content.
	for _, f := range []string{minor, major} {
		if strings.Contains(f, "from") || strings.Contains(f, "to:") {
			t.Errorf("lens form holds from or to:\n%s", f)
		}
	}
}

// Fixing a published lens changes the lens digest of the version whose edge
// it is on, and nothing else.
func TestLensFix(t *testing.T) {
	buggy := compileLineage(t, lineage(`{a: v1_0.a, b: v1_0.b}`))
	fixed := compileLineage(t, lineage(`{a: v1_0.a, if v1_0.b > 0 {b: v1_0.b}}`))
	for _, v := range []Version{{0, 0}, {0, 1}, {1, 0}} {
		b, f := mustID(t, buggy, v), mustID(t, fixed, v)
		if b.Schema != f.Schema {
			t.Errorf("%s: schema digest changed with a lens", v)
		}
		if changed := b.Lens != f.Lens; changed != (v == Version{1, 0}) {
			t.Errorf("%s: lens digest changed: %v", v, changed)
		}
	}
}

// LensForms tells the lenses of an edge apart: fixing the backward lens into
// 1.0 changes its form alone, and an edge without a forward lens has none.
func TestLensForms(t *testing.T) {
	buggy := compileLineage(t, lineage(`{a: v1_0.a, b: v1_0.b}`))
	fixed := compileLineage(t, lineage(`{a: v1_0.a, if v1_0.b > 0 {b: v1_0.b}}`))
	forms := func(lin cue.Value, v Version) (string, string) {
		t.Helper()
		f, b, err := LensForms(lin, v)
		if err != nil {
			t.Fatalf("%s: %v", v, err)
		}
		return f, b
	}
	bf, bb := forms(buggy, Version{1, 0})
	ff, fb := forms(fixed, Version{1, 0})
	if bf == "" || bf != ff {
		t.Errorf("1.0's forward lens: %q, then %q; want one form, unchanged", bf, ff)
	}
	if bb == "" || bb == fb {
		t.Errorf("1.0's backward lens: %q, then %q; want it changed", bb, fb)
	}
	if f, b := forms(buggy, Version{0, 1}); f != "" || b == "" {
		t.Errorf("0.1's lenses: forward %q, backward %q; want only a backward one", f, b)
	}
	if f, b := forms(buggy, Version{0, 0}); f != "" || b != "" {
		t.Errorf("0.0's lenses: forward %q, backward %q; want none", f, b)
	}
}

// A tip under development may lack the lenses its edge needs; its lens form
// holds the ones it has.
func TestLensFormMissing(t *testing.T) {
	lin := compileLineage(t, `lin: {
	schemas: {"0": {"0": {schema: {a: string}}}, "1": {"0": {schema: {b: string}}}}
	lenses: {"1.0->0.0": {v1_0: _, v0_0: {a: v1_0.b}}}
}`)
	got := mustLensForm(t, lin, Version{1, 0})
	if strings.Contains(got, "forward") || !strings.Contains(got, "backward") {
		t.Errorf("want only the backward lens:\n%s", got)
	}
}

// A version's identity is its content, not its position: the same schemas and
// lenses, numbered differently, have the same IDs.
func TestIDPositionIndependent(t *testing.T) {
	a := compileLineage(t, `lin: {
	schemas: {"0": {"0": {schema: {a: string}}}, "1": {"0": {schema: {b: string}}}}
	lenses: {
		"0.0->1.0": {v0_0: _, v1_0: {b: v0_0.a}}
		"1.0->0.0": {v1_0: _, v0_0: {a: v1_0.b}}
	}
}`)
	b := compileLineage(t, `lin: {
	schemas: {
		"0": {"0": {schema: {z: string}}}
		"1": {"0": {schema: {a: string}}}
		"2": {"0": {schema: {b: string}}}
	}
	lenses: {
		"0.0->1.0": {v0_0: _, v1_0: {a: v0_0.z}}
		"1.0->0.0": {v1_0: _, v0_0: {z: v1_0.a}}
		"1.0->2.0": {v1_0: _, v2_0: {b: v1_0.a}}
		"2.0->1.0": {v2_0: _, v1_0: {a: v2_0.b}}
	}
}`)
	if ida, idb := mustID(t, a, Version{1, 0}), mustID(t, b, Version{2, 0}); ida != idb {
		t.Errorf("a 1.0 %s != b 2.0 %s", ida, idb)
	}
}

// What the canonical form of a lens makes equal, and doesn't.
func TestLensFormConstruction(t *testing.T) {
	form := func(lens string) string {
		t.Helper()
		lin := compileLineage(t, `lin: {
	schemas: {"0": {"0": {schema: {a: string, b: int}}, "1": {schema: {a: string, b: int, c?: int}}}}
	lenses: {"0.1->0.0": {v0_1: _, `+lens+`}}
}`)
		return mustLensForm(t, lin, Version{0, 1})
	}
	base := form(`v0_0: {a: v0_1.a, b: v0_1.b}`)
	for name, c := range map[string]struct {
		lens  string
		equal bool
		why   string
	}{
		"field order": {`v0_0: {b: v0_1.b, a: v0_1.a}`, true,
			"as for schemas"},
		"let beside result": {`let A = v0_1.a, v0_0: {a: A, b: v0_1.b}`, true,
			"each field of a lens is exported on its own, so a let outside it is inlined by CUE's export"},
		"alias of input": {`v0_1: _, let I = v0_1, v0_0: {a: I.a, b: v0_1.b}`, true,
			"as for a let beside result"},
		"let inside result": {`v0_0: {let A = v0_1.a, a: A, b: v0_1.b}`, false,
			"lets within a value are kept: see the let vs inline case for schemas"},
		"empty lacunas": {`v0_0: {a: v0_1.a, b: v0_1.b}, lacunas: []`, false,
			"lacunas: [] is written, and lacunas absent is not; thema's kernel fills it in only when the lineage is unified with thema.Lineage, which a hashed lineage isn't"},
	} {
		t.Run(name, func(t *testing.T) {
			got := form(c.lens)
			if (got == base) != c.equal {
				t.Errorf("want equal=%v (%s):\n%s\n---\n%s", c.equal, c.why, got, base)
			}
		})
	}
}

// Of a lacuna, the lens form keeps what translation does with it, and not its
// message.
func TestLensFormLacunas(t *testing.T) {
	form := func(lacunas string) string {
		t.Helper()
		lin := compileLineage(t, `lin: {
	schemas: {"0": {"0": {schema: {a: string}}, "1": {schema: {a: string, c?: int, d?: int}}}}
	lenses: {"0.1->0.0": {
		v0_1: _
		v0_0: {a: v0_1.a}
		`+lacunas+`
	}}
}`)
		return mustLensForm(t, lin, Version{0, 1})
	}
	base := form(`lacunas: [if v0_1.c != _|_ {{sourceFields: [{path: "c", value: v0_1.c}], message: "c has no equivalent in 0.0 and was dropped", type: "DroppedField"}}]`)
	if strings.Contains(base, "message") {
		t.Fatalf("the lens form holds a message:\n%s", base)
	}
	for name, c := range map[string]struct {
		lacunas string
		equal   bool
	}{
		"message reworded": {`lacunas: [if v0_1.c != _|_ {{sourceFields: [{path: "c", value: v0_1.c}], message: "c is dropped", type: "DroppedField"}}]`, true},
		"message in a let": {`let L = {sourceFields: [{path: "c", value: v0_1.c}], message: "c is dropped", type: "DroppedField"}, lacunas: [if v0_1.c != _|_ {L}]`, true},
		"type":             {`lacunas: [if v0_1.c != _|_ {{sourceFields: [{path: "c", value: v0_1.c}], message: "c has no equivalent in 0.0 and was dropped", type: "LossyFieldMapping"}}]`, false},
		"source field":     {`lacunas: [if v0_1.d != _|_ {{sourceFields: [{path: "d", value: v0_1.d}], message: "c has no equivalent in 0.0 and was dropped", type: "DroppedField"}}]`, false},
		"condition":        {`lacunas: [{sourceFields: [{path: "c", value: v0_1.c}], message: "c has no equivalent in 0.0 and was dropped", type: "DroppedField"}]`, false},
		"target field":     {`lacunas: [if v0_1.c != _|_ {{sourceFields: [{path: "c", value: v0_1.c}], targetFields: [{path: "a", value: v0_0.a}], message: "c has no equivalent in 0.0 and was dropped", type: "DroppedField"}}]`, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := form(c.lacunas); (got == base) != c.equal {
				t.Errorf("want equal=%v:\n%s\n---\n%s", c.equal, got, base)
			}
		})
	}

	// A reported field's value is instance data, not a lacuna: a field of it
	// named message is kept.
	kept := form(`lacunas: [{targetFields: [{path: "a", value: {message: "x"}}], message: "m", type: "Placeholder"}]`)
	if strings.Count(kept, "message") != 1 {
		t.Errorf("want the value's message, and only it:\n%s", kept)
	}
}
