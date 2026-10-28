package thema

import (
	"encoding/json"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema/lacuna"
)

// The lineage the extras tests translate across: 0.0 and 0.1 leave every
// struct but shut open, and 1.0 closes the instance and the elements of
// items. 0.1 declares note as _, so a note 0.0 holds as an extra is promoted
// into it; 1.0 declares meta.team and the elements' rank.
const extrasLineage = `lin: thema.Lineage & {
	name: "extras"
	schemas: {
		"0": "0": schema: {
			a: string
			meta: {owner: string, ...}
			items: [...{name: string, ...}]
			shut: {x: int}
			...
		}
		"0": "1": schema: {
			a: string
			meta: {owner: string, ...}
			items: [...{name: string, ...}]
			shut: {x: int}
			note?: _
			...
		}
		"1": "0": schema: {
			a: string
			meta: {owner: string, team?: int, ...}
			items: [...{name: string, rank?: int}]
			shut: {x: int}
		}
	}
	lenses: {
		"0.1->0.0": {
			v0_1: _
			v0_0: {a: v0_1.a, meta: v0_1.meta, items: v0_1.items, shut: v0_1.shut}
		}
		"0.1->1.0": {
			v0_1: _
			v1_0: {a: v0_1.a, meta: owner: v0_1.meta.owner, items: [for x in v0_1.items {name: x.name}], shut: v0_1.shut}
		}
		"1.0->0.1": {
			v1_0: _
			v0_1: {a: v1_0.a, meta: owner: v1_0.meta.owner, items: [for x in v1_0.items {name: x.name}], shut: v1_0.shut}
		}
	}
}`

// Fields an instance holds that no lens maps are carried where the target
// struct is open, promoted into a field the target declares when they are an
// instance of it, and otherwise set aside with a DroppedField lacuna for each
// struct and cause; never does translation fail because of them (#193).
func TestTranslateCarriesExtras(t *testing.T) {
	ctx := cuecontext.New()
	lin, err := bindString(t, NewRuntime(ctx), extrasLineage)
	if err != nil {
		t.Fatal(err)
	}

	type step struct {
		from, to SyntacticVersion
		lacunas  []lacuna.Lacuna
	}
	dropped := func(msg string, refs ...lacuna.FieldRef) lacuna.Lacuna {
		return lacuna.Lacuna{SourceFields: refs, Type: lacuna.DroppedField, Message: msg}
	}
	ref := func(path string, v interface{}) lacuna.FieldRef { return lacuna.FieldRef{Path: path, Value: v} }

	for name, c := range map[string]struct {
		from, to SyntacticVersion
		data     string
		want     string
		steps    []step
		// back, if set, is the result of translating want back to from,
		// which must raise no lacuna.
		back string
	}{
		"minor step carries and promotes": {
			from: SV(0, 0), to: SV(0, 1),
			data:  `{a: "x", meta: {owner: "o", tag: "t"}, items: [{name: "n", color: "red"}], shut: {x: 1}, note: 5, top: true}`,
			want:  `{"a":"x","meta":{"owner":"o","tag":"t"},"items":[{"name":"n","color":"red"}],"shut":{"x":1},"note":5,"top":true}`,
			steps: []step{{SV(0, 0), SV(0, 1), []lacuna.Lacuna{}}},
			back:  `{"a":"x","meta":{"owner":"o","tag":"t"},"items":[{"name":"n","color":"red"}],"shut":{"x":1},"note":5,"top":true}`,
		},
		"nested and list elements, forward": {
			from: SV(0, 0), to: SV(1, 0),
			data: `{a: "x", meta: {owner: "o", team: 3, tag: "t"}, items: [{name: "n", rank: 1, color: "red"}, {name: "m"}], shut: {x: 1}, top: true}`,
			// team and rank are promoted, tag is carried, and color and top
			// are set aside, as 1.0 closes their structs.
			want: `{"a":"x","meta":{"owner":"o","team":3,"tag":"t"},"items":[{"name":"n","rank":1},{"name":"m"}],"shut":{"x":1}}`,
			steps: []step{
				{SV(0, 0), SV(0, 1), []lacuna.Lacuna{}},
				{SV(0, 1), SV(1, 0), []lacuna.Lacuna{
					dropped("items[0].color set aside: 1.0 does not declare it in items[0], which it does not leave open", ref("items[0].color", "red")),
					dropped("top set aside: 1.0 does not declare it in the instance, which it does not leave open", ref("top", true)),
				}},
			},
		},
		"type conflict": {
			from: SV(0, 1), to: SV(1, 0),
			data: `{a: "x", meta: {owner: "o", team: "three", lead: "l"}, items: [{name: "n", rank: "first"}, {name: "m", rank: 2}], shut: {x: 1}}`,
			want: `{"a":"x","meta":{"owner":"o","lead":"l"},"items":[{"name":"n"},{"name":"m","rank":2}],"shut":{"x":1}}`,
			steps: []step{
				{SV(0, 1), SV(1, 0), []lacuna.Lacuna{
					dropped("meta.team set aside: its value is not an instance of what 1.0 declares for it", ref("meta.team", "three")),
					dropped("items[0].rank set aside: its value is not an instance of what 1.0 declares for it", ref("items[0].rank", "first")),
				}},
			},
		},
		"several fields of one struct share a lacuna": {
			from: SV(0, 1), to: SV(1, 0),
			data: `{a: "x", meta: {owner: "o"}, items: [], shut: {x: 1}, p: 1, q: [2]}`,
			want: `{"a":"x","meta":{"owner":"o"},"items":[],"shut":{"x":1}}`,
			steps: []step{
				{SV(0, 1), SV(1, 0), []lacuna.Lacuna{
					dropped("p, q set aside: 1.0 does not declare them in the instance, which it does not leave open", ref("p", int64(1)), ref("q", []interface{}{int64(2)})),
				}},
			},
		},
		// Backward, the fields 1.0 declares that the lens to 0.1 does not
		// map (meta.team, the elements' rank) are carried into 0.1's open
		// structs, and promoted again on the way forward.
		"backward carries what the lens leaves alone": {
			from: SV(1, 0), to: SV(0, 0),
			data:  `{a: "x", meta: {owner: "o", team: 3, tag: "t"}, items: [{name: "n", rank: 1}], shut: {x: 1}}`,
			want:  `{"a":"x","meta":{"owner":"o","team":3,"tag":"t"},"items":[{"name":"n","rank":1}],"shut":{"x":1}}`,
			steps: []step{{SV(1, 0), SV(0, 1), []lacuna.Lacuna{}}, {SV(0, 1), SV(0, 0), []lacuna.Lacuna{}}},
			back:  `{"a":"x","meta":{"owner":"o","team":3,"tag":"t"},"items":[{"name":"n","rank":1}],"shut":{"x":1}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			translate := func(from, to SyntacticVersion, data string) (string, lacuna.TranslationLacunas) {
				t.Helper()
				inst, err := lin.Schema(from)
				if err != nil {
					t.Fatal(err)
				}
				in, err := inst.Validate(ctx.CompileString(data))
				if err != nil {
					t.Fatal(err)
				}
				out, lac, err := in.Translate(to)
				if err != nil {
					t.Fatal(err)
				}
				b, err := json.Marshal(out.Underlying())
				if err != nil {
					t.Fatal(err)
				}
				return string(b), lac
			}
			got, lac := translate(c.from, c.to, c.data)
			if got != c.want {
				t.Errorf("got %s\nwant %s", got, c.want)
			}
			var want lacuna.TranslationLacunas
			for _, s := range c.steps {
				want = append(want, lacuna.TranslationStep{From: lacuna.SyntacticVersion(s.from), To: lacuna.SyntacticVersion(s.to), Lacunas: s.lacunas})
			}
			if g, w := mustJSON(t, lac), mustJSON(t, want); g != w {
				t.Errorf("lacunas: got %s\nwant %s", g, w)
			}
			if c.back == "" {
				return
			}
			back, lac := translate(c.to, c.from, got)
			if back != c.back {
				t.Errorf("back: got %s\nwant %s", back, c.back)
			}
			if len(lac.AsList()) > 0 {
				t.Errorf("back: got lacunas %s", mustJSON(t, lac))
			}
		})
	}
}

// A minor step taken alone, by AsSuccessor, carries extras as Translate does,
// and AsPredecessor carries them back.
func TestAsSuccessorCarriesExtras(t *testing.T) {
	ctx := cuecontext.New()
	lin, err := bindString(t, NewRuntime(ctx), extrasLineage)
	if err != nil {
		t.Fatal(err)
	}
	const data = `{"a":"x","meta":{"owner":"o","tag":"t"},"items":[],"shut":{"x":1},"note":5,"top":true}`
	inst, err := lin.First().Validate(ctx.CompileString(data))
	if err != nil {
		t.Fatal(err)
	}
	next, lac, err := inst.AsSuccessor()
	if err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, next.Underlying()); got != data {
		t.Errorf("AsSuccessor: got %s, want %s", got, data)
	}
	if len(lac.AsList()) > 0 {
		t.Errorf("AsSuccessor: got lacunas %s", mustJSON(t, lac))
	}
	prev, lac, err := next.AsPredecessor()
	if err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, prev.Underlying()); got != data {
		t.Errorf("AsPredecessor: got %s, want %s", got, data)
	}
	if len(lac.AsList()) > 0 {
		t.Errorf("AsPredecessor: got lacunas %s", mustJSON(t, lac))
	}
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
