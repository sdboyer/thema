package thema

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
)

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
			input: _
			result: a: input.a
			lacunas: [thema.Lacuna & {
				condition: input.note != _|_
				sourceFields: [{path: "note", value: input.note}]
				message: "note dropped"
				type: thema.LacunaTypes.DroppedField
			}]
		}
		"1.0->0.1": {
			input: _
			result: a: input.a
			lacunas: [thema.Lacuna & {
				sourceFields: [{path: "id", value: input.id}]
				message: "id dropped"
				type: thema.LacunaTypes.DroppedField
			}]
		}
		"0.1->1.0": {
			input: _
			result: {a: input.a, id: -1}
			lacunas: [thema.Lacuna & {
				targetFields: [{path: "id", value: result.id}]
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
		types    []LacunaType
	}
	steps := func(tl TranslationLacunas) []step {
		var out []step
		for _, s := range tl {
			st := step{from: s.From, to: s.To}
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
		"condition true":  {SV(0, 1), `{a: "x", note: "n"}`, SV(0, 0), []step{{SV(0, 1), SV(0, 0), []LacunaType{LacunaDroppedField}}}},
		"forward, multi-step": {SV(0, 0), `{a: "x"}`, SV(1, 0), []step{
			{SV(0, 0), SV(0, 1), nil},
			{SV(0, 1), SV(1, 0), []LacunaType{LacunaPlaceholder}},
		}},
		"backward, multi-step": {SV(1, 0), `{a: "x", id: 3}`, SV(0, 0), []step{
			{SV(1, 0), SV(0, 1), []LacunaType{LacunaDroppedField}},
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
		input: _
		result: {a: input.a, nested: input.nested}
		lacunas: ` + lacunas + `
	}
}`
	}

	for name, c := range map[string]struct {
		lacunas string
		errText string
	}{
		"valid":                          {`[thema.Lacuna & {sourceFields: [{path: "b", value: input.b}], targetFields: [{path: "nested.x", value: result.nested.x}], message: "m", type: "DroppedField"}]`, ""},
		"conditional list":               {`[if input.b != _|_ {thema.Lacuna & {message: "m", type: "DroppedField"}}]`, "listed unconditionally"},
		"conditional list via let":       {"L\n\t\tlet L = [if input.b != _|_ {thema.Lacuna & {message: \"m\", type: \"DroppedField\"}}]", "listed unconditionally"},
		"source path not in from schema": {`[thema.Lacuna & {sourceFields: [{path: "nope", value: 1}], message: "m", type: "DroppedField"}]`, `"nope" is not declared by schema 0.1`},
		"target path not in to schema":   {`[thema.Lacuna & {targetFields: [{path: "b", value: 1}], message: "m", type: "DroppedField"}]`, `"b" is not declared by schema 0.0`},
		"nested path not declared":       {`[thema.Lacuna & {targetFields: [{path: "nested.y", value: 1}], message: "m", type: "DroppedField"}]`, `"nested.y" is not declared`},
		"invalid path syntax":            {`[thema.Lacuna & {targetFields: [{path: "a..b", value: 1}], message: "m", type: "DroppedField"}]`, "invalid path"},
		"missing message":                {`[thema.Lacuna & {type: "DroppedField"}]`, "message is required"},
		"missing type":                   {`[thema.Lacuna & {message: "m"}]`, "type is required"},
		"unknown type":                   {`[thema.Lacuna & {message: "m", type: "Bogus"}]`, "Bogus"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := bindString(t, rt, lineage(c.lacunas))
			switch {
			case c.errText == "" && err != nil:
				t.Fatalf("unexpected bind error: %v", cerrors.Details(err, nil))
			case c.errText != "" && err == nil:
				t.Fatal("bind succeeded, want an error")
			case c.errText != "" && !strings.Contains(cerrors.Details(err, nil), c.errText):
				t.Fatalf("error does not mention %q: %v", c.errText, cerrors.Details(err, nil))
			}
		})
	}
}

func TestGoLensLacunas(t *testing.T) {
	ctx := cuecontext.New()
	rt := NewRuntime(ctx)
	src := `lin: thema.Lineage & {
	name: "golens"
	schemas: {
		"0": "0": {schema: {a: string}}
		"0": "1": {schema: {a: string, b?: int}}
	}
}`
	bind := func(lacunas []Lacuna) Lineage {
		t.Helper()
		v := loadLineageValue(t, ctx, src)
		lin, err := BindLineage(v, rt, ImperativeLenses(ImperativeLens{From: SV(0, 1), To: SV(0, 0),
			Mapper: func(inst *Instance, to Schema) (*Instance, []Lacuna, error) {
				out, err := to.Validate(ctx.CompileString(`{a: "x"}`))
				return out, lacunas, err
			}}))
		if err != nil {
			t.Fatal(err)
		}
		return lin
	}
	translate := func(lin Lineage) (TranslationLacunas, error) {
		inst, err := lin.Latest().Validate(ctx.CompileString(`{a: "x", b: 2}`))
		if err != nil {
			t.Fatal(err)
		}
		_, lac, err := inst.Translate(SV(0, 0))
		return lac, err
	}

	lac, err := translate(bind([]Lacuna{{Type: LacunaDroppedField, Message: "b dropped", SourceFields: []FieldRef{{Path: "b", Value: 2}}}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(lac) != 1 || lac[0].From != SV(0, 1) || lac[0].To != SV(0, 0) || len(lac[0].Lacunas) != 1 || lac[0].Lacunas[0].Type != LacunaDroppedField {
		t.Fatalf("unexpected lacunas: %+v", lac)
	}

	for name, l := range map[string]Lacuna{
		"unknown type":      {Type: "Bogus", Message: "m"},
		"missing message":   {Type: LacunaDroppedField},
		"undeclared path":   {Type: LacunaDroppedField, Message: "m", SourceFields: []FieldRef{{Path: "nope"}}},
		"wrong side's path": {Type: LacunaDroppedField, Message: "m", TargetFields: []FieldRef{{Path: "b"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := translate(bind([]Lacuna{l})); err == nil {
				t.Error("invalid lacuna from a Go lens was accepted")
			}
		})
	}
}

// The Go lacuna type constants must match the types defined in thema's CUE.
func TestLacunaTypesMatchCUE(t *testing.T) {
	rt := NewRuntime(cuecontext.New())
	iter, err := rt.Underlying().LookupPath(cue.ParsePath("LacunaTypes")).Fields()
	if err != nil {
		t.Fatal(err)
	}
	var cueTypes []string
	for iter.Next() {
		s, err := iter.Value().String()
		if err != nil || s != iter.Selector().String() {
			t.Errorf("LacunaTypes.%s must be the string %q, got %v", iter.Selector(), iter.Selector(), iter.Value())
		}
		cueTypes = append(cueTypes, s)
	}
	var goTypes []string
	for lt := range lacunaTypes {
		goTypes = append(goTypes, string(lt))
	}
	sort.Strings(cueTypes)
	sort.Strings(goTypes)
	if !reflect.DeepEqual(cueTypes, goTypes) {
		t.Errorf("CUE lacuna types %v differ from Go constants %v", cueTypes, goTypes)
	}
}
