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
// instance of it, and otherwise set aside with a lacuna; never does
// translation fail because of them (#193).
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
	lossy := func(msg string, src, tgt lacuna.FieldRef) lacuna.Lacuna {
		return lacuna.Lacuna{SourceFields: []lacuna.FieldRef{src}, TargetFields: []lacuna.FieldRef{tgt}, Type: lacuna.LossyFieldMapping, Message: msg}
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
					lossy("meta.team not carried: its value is not an instance of what 1.0 declares for it", ref("meta.team", "three"), ref("meta.team", nil)),
					lossy("items[0].rank not carried: its value is not an instance of what 1.0 declares for it", ref("items[0].rank", "first"), ref("items[0].rank", nil)),
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

// The lineage the field-fate tests translate across. 0.0 leaves its instance,
// meta and the elements of items open; 1.0 closes its instance. The lens to
// 1.0 moves meta to info whole, embeds cfg in conf, writes a list of one
// item, and stamp, and
// declares a DroppedField of its own for gone; it leaves old, which 1.0 does
// not declare, and kept, which it does, alone. 1.0's flag has a default the
// lens does not write.
const fatesLineage = `lin: thema.Lineage & {
	name: "fates"
	schemas: {
		"0": "0": schema: {
			meta: {owner: string, ...}
			cfg: {a: int, ...}
			items: [...{name: string, ...}]
			gone: string
			old?: string
			kept?: string
			...
		}
		"1": "0": schema: {
			info: {owner: string, ...}
			conf: {a: int, b: int, ...}
			items: [...{name: string, ...}]
			kept?: string
			flag: bool | *true
			stamp: string
		}
	}
	lenses: {
		"0.0->1.0": {
			v0_0: _
			v1_0: {
				info:  v0_0.meta
				conf:  {v0_0.cfg, b: 1}
				items: [{name: "first"}]
				stamp: "s"
			}
			lacunas: [{
				type:    "DroppedField"
				message: "gone is gone"
				sourceFields: [{path: "gone", value: v0_0.gone}]
			}]
		}
		"1.0->0.0": {
			v1_0: _
			v0_0: {
				meta: v1_0.info
				cfg: a: v1_0.conf.a
				items: [for x in v1_0.items {name: x.name}]
				gone:  "g"
			}
		}
	}
}`

// The fate of each field no lens maps, by the rules of docs/invariants.md
// ("Fields no lens maps").
func TestTranslateFieldFates(t *testing.T) {
	ctx := cuecontext.New()
	lin, err := bindString(t, NewRuntime(ctx), fatesLineage)
	if err != nil {
		t.Fatal(err)
	}
	ref := func(path string, v interface{}) lacuna.FieldRef { return lacuna.FieldRef{Path: path, Value: v} }
	dropped := func(msg string, refs ...lacuna.FieldRef) lacuna.Lacuna {
		return lacuna.Lacuna{SourceFields: refs, Type: lacuna.DroppedField, Message: msg}
	}

	for name, c := range map[string]struct {
		from, to SyntacticVersion
		data     string
		want     string
		lacunas  []lacuna.Lacuna
	}{
		"forward": {
			from: SV(0, 0), to: SV(1, 0),
			data: `{meta: {owner: "o", tag: "t", z: {}}, cfg: {a: 1, tag: "c"}, items: [{name: "a", color: "red"}, {name: "b"}, {name: "c", color: "blue"}], gone: "g", old: "x", kept: "k", stamp: "mine", flag: false, e: {}, top: 1}`,
			// meta's extras follow it to info, the empty z kept as it is, and
			// cfg's to the conf it is embedded in; the
			// first item keeps its own, as element 0 is taken to be element
			// 0; flag is promoted over 1.0's default; the lens's stamp
			// stands.
			want: `{"info":{"owner":"o","tag":"t","z":{}},"conf":{"a":1,"b":1,"tag":"c"},"items":[{"name":"first","color":"red"}],"flag":false,"stamp":"s"}`,
			lacunas: []lacuna.Lacuna{
				// The lens's own lacuna for gone stands alone.
				dropped("gone is gone", ref("gone", "g")),
				// The elements past the new length are dropped whole.
				dropped("items[1], items[2] set aside: the lens from 0.0 to 1.0 neither maps nor writes them", ref("items[1]", map[string]interface{}{"name": "b"}), ref("items[2]", map[string]interface{}{"name": "c", "color": "blue"})),
				// old, which 0.0 declares and the lens leaves alone, and top, an
				// extra, are dropped by the closed instance alike; the empty e
				// loses nothing.
				dropped("old, top set aside: 1.0 does not declare them in the instance, which it does not leave open", ref("old", "x"), ref("top", int64(1))),
				dropped("kept set aside: the lens from 0.0 to 1.0 neither maps nor writes it", ref("kept", "k")),
				dropped("stamp set aside: the lens from 0.0 to 1.0 writes its own value for it", ref("stamp", "mine")),
			},
		},
		"lossy over a default": {
			from: SV(0, 0), to: SV(1, 0),
			data: `{meta: {owner: "o"}, cfg: {a: 1}, items: [], gone: "g", flag: "no"}`,
			want: `{"info":{"owner":"o"},"conf":{"a":1,"b":1},"items":[{"name":"first"}],"flag":true,"stamp":"s"}`,
			lacunas: []lacuna.Lacuna{
				dropped("gone is gone", ref("gone", "g")),
				{
					SourceFields: []lacuna.FieldRef{ref("flag", "no")},
					TargetFields: []lacuna.FieldRef{ref("flag", true)},
					Type:         lacuna.LossyFieldMapping,
					Message:      "flag not carried: its value is not an instance of what 1.0 declares for it",
				},
			},
		},
		// Backward, info's extras follow it to meta, and the fields 1.0
		// declares that 0.0 does not are carried into 0.0's open instance;
		// kept, which both declare, is not copied. cfg is assembled from a
		// part of conf, not moved whole, so conf's other fields have no conf
		// in 0.0 to go to.
		"backward": {
			from: SV(1, 0), to: SV(0, 0),
			data: `{info: {owner: "o", tag: "t"}, conf: {a: 1, b: 2, tag: "c"}, items: [{name: "a", color: "red"}], kept: "k", flag: false, stamp: "s"}`,
			want: `{"meta":{"owner":"o","tag":"t"},"cfg":{"a":1},"items":[{"name":"a","color":"red"}],"gone":"g","flag":false,"stamp":"s"}`,
			lacunas: []lacuna.Lacuna{
				dropped("conf.b, conf.tag set aside: translating from 1.0 to 0.0 produces no conf to carry them in", ref("conf.b", int64(2)), ref("conf.tag", "c")),
				dropped("kept set aside: the lens from 1.0 to 0.0 neither maps nor writes it", ref("kept", "k")),
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			sch, err := lin.Schema(c.from)
			if err != nil {
				t.Fatal(err)
			}
			in, err := sch.Validate(ctx.CompileString(c.data))
			if err != nil {
				t.Fatal(err)
			}
			out, lac, err := in.Translate(c.to)
			if err != nil {
				t.Fatal(err)
			}
			if got := mustJSON(t, out.Underlying()); got != c.want {
				t.Errorf("got %s\nwant %s", got, c.want)
			}
			want := lacuna.TranslationLacunas{{From: lacuna.SyntacticVersion(c.from), To: lacuna.SyntacticVersion(c.to), Lacunas: c.lacunas}}
			if g, w := mustJSON(t, lac), mustJSON(t, want); g != w {
				t.Errorf("lacunas: got %s\nwant %s", g, w)
			}
		})
	}
}

// lostLineage moves b, and tags, out of the optional struct q in 1.0. Its
// hand-written backward lens writes q, with b and tags, only when the instance
// has a q, and lists no lacuna for the b it then reads but leaves unwritten.
// root is 0.0's root struct beyond q (such as "..." to leave it open), and
// lacunas the backward lens's lacunas.
func lostLineage(root, lacunas string) string {
	return `lin: thema.Lineage & {
	name: "lost"
	schemas: {
		"0": "0": schema: {
			q?: {a!: int, b?: int, tags?: [...string]}
			` + root + `
		}
		"1": "0": schema: {
			q?: {a!: int}
			b?: int
			tags?: [...string]
		}
	}
	lenses: {
		"0.0->1.0": {
			v0_0: _
			v1_0: {
				if v0_0.q != _|_ {
					q: a: v0_0.q.a
					if v0_0.q.b != _|_ {b: v0_0.q.b}
					if v0_0.q.tags != _|_ {tags: v0_0.q.tags}
				}
			}
		}
		"1.0->0.0": {
			v1_0: _
			v0_0: {
				if v1_0.q != _|_ {
					q: {
						a: v1_0.q.a
						if v1_0.b != _|_ {b: v1_0.b}
						if v1_0.tags != _|_ {tags: v1_0.tags}
					}
				}
			}
			lacunas: [` + lacunas + `]
		}
	}
}`
}

// A value the lens reads, where no result field that reads it was written for
// the instance, is lost: translation sets it aside with a DroppedField, unless
// the lens lists its own lacuna for it, or it can be carried.
func TestTranslateLostReads(t *testing.T) {
	ref := func(path string, v interface{}) lacuna.FieldRef { return lacuna.FieldRef{Path: path, Value: v} }
	dropped := func(msg string, refs ...lacuna.FieldRef) lacuna.Lacuna {
		return lacuna.Lacuna{SourceFields: refs, Type: lacuna.DroppedField, Message: msg}
	}
	const bOnly = `{b: 2}`
	const lost = "b set aside: the lens from 1.0 to 0.0 reads it, but wrote nothing that carries it for this instance"
	const ownLacuna = `{
		type:      "DroppedField"
		message:   "b has no q to go in"
		condition: v1_0.q == _|_ && v1_0.b != _|_
		sourceFields: [if v1_0.b != _|_ {{path: "b", value: v1_0.b}}]
	}`

	for name, c := range map[string]struct {
		root, lacunas string
		data          string
		want          string
		lacunasWant   []lacuna.Lacuna
	}{
		"b without q": {
			data: bOnly, want: `{}`,
			lacunasWant: []lacuna.Lacuna{dropped(lost, ref("b", int64(2)))},
		},
		"b with q": {
			data: `{q: {a: 1}, b: 2}`, want: `{"q":{"a":1,"b":2}}`,
		},
		"q alone": {data: `{q: {a: 1}}`, want: `{"q":{"a":1}}`},
		"nothing": {data: `{}`, want: `{}`},
		// tags is read and lost alike, but it is empty: nothing is lost.
		"empty tags": {
			data: `{b: 2, tags: []}`, want: `{}`,
			lacunasWant: []lacuna.Lacuna{dropped(lost, ref("b", int64(2)))},
		},
		"the lens's own lacuna stands alone": {
			lacunas: ownLacuna,
			data:    `{b: 2}`, want: `{}`,
			lacunasWant: []lacuna.Lacuna{dropped("b has no q to go in", ref("b", int64(2)))},
		},
		// Into an open 0.0, b is carried as a field 0.0 does not declare.
		"carried": {
			root: "...",
			data: bOnly, want: `{"b":2}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := cuecontext.New()
			lin, err := bindString(t, NewRuntime(ctx), lostLineage(c.root, c.lacunas))
			if err != nil {
				t.Fatal(err)
			}
			in, err := lin.Latest().Validate(ctx.CompileString(c.data))
			if err != nil {
				t.Fatal(err)
			}
			out, lac, err := in.Translate(SV(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			if got := mustJSON(t, out.Underlying()); got != c.want {
				t.Errorf("got %s\nwant %s", got, c.want)
			}
			if c.lacunasWant == nil {
				c.lacunasWant = []lacuna.Lacuna{}
			}
			want := lacuna.TranslationLacunas{{From: lacuna.SyntacticVersion(SV(1, 0)), To: lacuna.SyntacticVersion(SV(0, 0)), Lacunas: c.lacunasWant}}
			if g, w := mustJSON(t, lac), mustJSON(t, want); g != w {
				t.Errorf("lacunas: got %s\nwant %s", g, w)
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
