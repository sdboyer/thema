package thema

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/lacuna"
)

// renderedDiagnostics is everything err's diagnostics say, as text, for
// matching expectations against headlines, notes and help alike.
func renderedDiagnostics(err error) string {
	var b strings.Builder
	diag.Render(&b, diag.Diagnostics(err), diag.RenderOptions{})
	return b.String()
}

// A lineage whose lenses emit lacunas: 1.0 adds a required field that the
// forward lens fills with a placeholder, and the backward lens drops it.
const lacunaLineage = `lin: thema.Lineage & {
	name: "lacunas"
	schemas: {
		"0": "0": {
			schema: {a: string}
		}
		"0": "1": {
			schema: {a: string, note?: string}
		}
		"1": "0": {
			schema: {a: string, id: int}
		}
	}
	lenses: {
		"0.1->0.0": {
			v0_1: _
			v0_0: a: v0_1.a
			lacunas: [thema.Lacuna & {
				condition: v0_1.note != _|_
				sourceFields: [{path: "note", value: v0_1.note}]
				message: "note dropped"
				type: thema.LacunaTypes.DroppedField
			}]
		}
		"1.0->0.1": {
			v1_0: _
			v0_1: a: v1_0.a
			lacunas: [thema.Lacuna & {
				sourceFields: [{path: "id", value: v1_0.id}]
				message: "id dropped"
				type: thema.LacunaTypes.DroppedField
			}]
		}
		"0.1->1.0": {
			v0_1: _
			v1_0: {a: v0_1.a, id: -1}
			lacunas: [thema.Lacuna & {
				targetFields: [{path: "id", value: v1_0.id}]
				message: "-1 is a placeholder"
				type: thema.LacunaTypes.Placeholder
			}]
		}
	}
}`

func TestLacunaEmission(t *testing.T) {
	ctx := cuecontext.New()
	lin, err := bindString(t, NewRuntime(ctx), lacunaLineage)
	if err != nil {
		t.Fatal(err)
	}

	type step struct {
		from, to SyntacticVersion
		types    []lacuna.Type
	}
	steps := func(tl lacuna.TranslationLacunas) []step {
		var out []step
		for _, s := range tl {
			st := step{from: SyntacticVersion(s.From), to: SyntacticVersion(s.To)}
			for _, l := range s.Lacunas {
				st.types = append(st.types, l.Type)
			}
			out = append(out, st)
		}
		return out
	}

	for name, c := range map[string]struct {
		from SyntacticVersion
		data string
		to   SyntacticVersion
		want []step
	}{
		"condition false": {SV(0, 1), `{a: "x"}`, SV(0, 0), []step{{SV(0, 1), SV(0, 0), nil}}},
		"condition true":  {SV(0, 1), `{a: "x", note: "n"}`, SV(0, 0), []step{{SV(0, 1), SV(0, 0), []lacuna.Type{lacuna.DroppedField}}}},
		"forward, multi-step": {SV(0, 0), `{a: "x"}`, SV(1, 0), []step{
			{SV(0, 0), SV(0, 1), nil},
			{SV(0, 1), SV(1, 0), []lacuna.Type{lacuna.Placeholder}},
		}},
		"backward, multi-step": {SV(1, 0), `{a: "x", id: 3}`, SV(0, 0), []step{
			{SV(1, 0), SV(0, 1), []lacuna.Type{lacuna.DroppedField}},
			{SV(0, 1), SV(0, 0), nil},
		}},
		"identity": {SV(0, 0), `{a: "x"}`, SV(0, 0), nil},
	} {
		t.Run(name, func(t *testing.T) {
			inst, err := mustSchema(lin, c.from.String()).Validate(ctx.CompileString(c.data))
			if err != nil {
				t.Fatal(err)
			}
			_, lac, err := inst.Translate(c.to)
			if err != nil {
				t.Fatal(cerrors.Details(err, nil))
			}
			if got := steps(lac); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got steps %+v, want %+v", got, c.want)
			}
		})
	}

	t.Run("field values", func(t *testing.T) {
		inst, _ := lin.First().Validate(ctx.CompileString(`{a: "x"}`))
		_, lac, err := inst.Translate(SV(1, 0))
		if err != nil {
			t.Fatal(err)
		}
		l := lac.AsList()
		if len(l) != 1 || len(l[0].TargetFields) != 1 || l[0].TargetFields[0].Path != "id" || l[0].Message != "-1 is a placeholder" {
			t.Fatalf("unexpected lacunas: %+v", l)
		}
		if fmt.Sprint(l[0].TargetFields[0].Value) != "-1" {
			t.Errorf("target field value = %#v, want -1", l[0].TargetFields[0].Value)
		}
	})
}

func TestLacunaBindChecks(t *testing.T) {
	ctx := cuecontext.New()
	rt := NewRuntime(ctx)
	lineage := func(lacunas string) string {
		return `lin: thema.Lineage & {
	name: "lacunacheck"
	schemas: "0": {
		"0": schema: {a: string, nested: {x: int}}
		"1": schema: {a: string, nested: {x: int}, b?: int}
	}
	lenses: "0.1->0.0": {
		v0_1: _
		v0_0: {a: v0_1.a, nested: v0_1.nested}
		lacunas: ` + lacunas + `
	}
}`
	}

	for name, c := range map[string]struct {
		lacunas string
		errText string
	}{
		"well-formed":                    {`[thema.Lacuna & {condition: v0_1.b != _|_, sourceFields: [{path: "b", value: v0_1.b}], targetFields: [{path: "nested.x", value: v0_0.nested.x}], message: "m", type: "DroppedField"}]`, ""},
		"value may be absent":            {`[thema.Lacuna & {sourceFields: [{path: "b", value: v0_1.b}], message: "m", type: "DroppedField"}]`, "source-absent"},
		"conditional list":               {`[if v0_1.b != _|_ {thema.Lacuna & {message: "m", type: "DroppedField"}}]`, "listed unconditionally"},
		"conditional list via let":       {"L\n\t\tlet L = [if v0_1.b != _|_ {thema.Lacuna & {message: \"m\", type: \"DroppedField\"}}]", "listed unconditionally"},
		"source path not in from schema": {`[thema.Lacuna & {sourceFields: [{path: "nope", value: 1}], message: "m", type: "DroppedField"}]`, `names nope in sourceFields[0], which schema 0.1 does not declare`},
		"target path not in to schema":   {`[thema.Lacuna & {targetFields: [{path: "b", value: 1}], message: "m", type: "DroppedField"}]`, `names b in targetFields[0], which schema 0.0 does not declare`},
		"nested path not declared":       {`[thema.Lacuna & {targetFields: [{path: "nested.y", value: 1}], message: "m", type: "DroppedField"}]`, `names nested.y in targetFields[0]`},
		"invalid path syntax":            {`[thema.Lacuna & {targetFields: [{path: "a..b", value: 1}], message: "m", type: "DroppedField"}]`, "is not a path"},
		"missing message":                {`[thema.Lacuna & {type: "DroppedField"}]`, "has no message"},
		"missing type":                   {`[thema.Lacuna & {message: "m"}]`, "has no type"},
		"unknown type":                   {`[thema.Lacuna & {message: "m", type: "Bogus"}]`, "Bogus"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := bindString(t, rt, lineage(c.lacunas))
			switch {
			case c.errText == "" && err != nil:
				t.Fatalf("unexpected bind error: %v", cerrors.Details(err, nil))
			case c.errText != "" && err == nil:
				t.Fatal("bind succeeded, want an error")
			case c.errText != "" && !strings.Contains(renderedDiagnostics(err), c.errText):
				t.Fatalf("error does not mention %q:\n%s", c.errText, renderedDiagnostics(err))
			}
		})
	}
}
