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
				// The lens writes its own name for the first item without
				// reading the instance's.
				dropped("items[0].name set aside: the lens from 0.0 to 1.0 writes its own value for it", ref("items[0].name", "a")),
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
// has a q, and lists no lacuna for the b it refers to but leaves unwritten.
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

// A value the lens refers to has an effect on the target, even where no result
// field that refers to it was written for the instance: translation raises no
// lacuna for it, and leaves it to the lens, which may list its own.
func TestTranslateReferencedUnwritten(t *testing.T) {
	ref := func(path string, v interface{}) lacuna.FieldRef { return lacuna.FieldRef{Path: path, Value: v} }
	dropped := func(msg string, refs ...lacuna.FieldRef) lacuna.Lacuna {
		return lacuna.Lacuna{SourceFields: refs, Type: lacuna.DroppedField, Message: msg}
	}
	const bOnly = `{b: 2}`
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
		"b without q": {data: bOnly, want: `{}`},
		"b with q": {
			data: `{q: {a: 1}, b: 2}`, want: `{"q":{"a":1,"b":2}}`,
		},
		"q alone":    {data: `{q: {a: 1}}`, want: `{"q":{"a":1}}`},
		"nothing":    {data: `{}`, want: `{}`},
		"empty tags": {data: `{b: 2, tags: []}`, want: `{}`},
		"the lens's own lacuna stands alone": {
			lacunas: ownLacuna,
			data:    `{b: 2}`, want: `{}`,
			lacunasWant: []lacuna.Lacuna{dropped("b has no q to go in", ref("b", int64(2)))},
		},
		// Into an open 0.0, b is still the lens's: it is not carried as a
		// field 0.0 does not declare.
		"open target": {
			root: "...",
			data: bOnly, want: `{}`,
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

// discriminatorLineage's forward lens refers to kind only in its arms' conditions,
// to tell which of isA's values to write. root is 1.0's root struct beyond
// isA and n (such as "kind?: string", or "..." to leave it open), and kind
// what the lens writes into the instance besides.
func discriminatorLineage(root, kind string) string {
	return `lin: thema.Lineage & {
	name: "discriminator"
	schemas: {
		"0": "0": schema: {kind: "a" | "b", n?: int}
		"1": "0": schema: {
			isA: bool
			n?:  int
			` + root + `
		}
	}
	lenses: {
		"0.0->1.0": {
			v0_0: _
			v1_0: {
				if v0_0.kind == "a" {isA: true}
				if v0_0.kind != "a" {isA: false}
				if v0_0.n != _|_ {n: v0_0.n}
				` + kind + `
			}
		}
		"1.0->0.0": {
			v1_0: _
			v0_0: {
				if v1_0.isA {kind: "a"}
				if !v1_0.isA {kind: "b"}
				if v1_0.n != _|_ {n: v1_0.n}
			}
		}
	}
}`
}

// overwriteLineage's forward lens writes b, which both versions declare,
// without referring to the instance's b: as the constant lit, or from a.
func overwriteLineage(b string) string {
	return `lin: thema.Lineage & {
	name: "overwrite"
	schemas: {
		"0": "0": schema: {a: string, b: int}
		"1": "0": schema: {a: string, b: int, c: string}
	}
	lenses: {
		"0.0->1.0": {
			v0_0: _
			v1_0: {a: v0_0.a, b: ` + b + `, c: "c"}
		}
		"1.0->0.0": {
			v1_0: _
			v0_0: {a: v1_0.a, b: v1_0.b}
		}
	}
}`
}

// armsLineage's forward lens writes x by one arm of three, picked by mode: it
// copies b, computes from it, or writes a constant, through a let, without
// referring to it.
const armsLineage = `lin: thema.Lineage & {
	name: "arms"
	schemas: {
		"0": "0": schema: {mode: "copy" | "upper" | "fixed", b: string}
		"1": "0": schema: {mode: "copy" | "upper" | "fixed", x: string}
	}
	lenses: {
		"0.0->1.0": {
			v0_0: _
			let F = "fixed"
			v1_0: {
				mode: v0_0.mode
				if v0_0.mode == "copy" {x: v0_0.b}
				if v0_0.mode == "upper" {x: v0_0.b + "!"}
				if v0_0.mode == "fixed" {x: F}
			}
		}
		"1.0->0.0": {
			v1_0: _
			v0_0: {mode: v1_0.mode, b: v1_0.x}
		}
	}
}`

// A source value the lens refers to anywhere has an effect on the target, so
// translation raises no lacuna for it: one referred to only in a condition (a
// discriminator), or only by an arm that did not fire. A field both versions
// declare that the lens writes without referring to it has no effect, and is
// set aside with a DroppedField.
func TestTranslateReferenceIsEffect(t *testing.T) {
	ref := func(path string, v interface{}) lacuna.FieldRef { return lacuna.FieldRef{Path: path, Value: v} }
	dropped := func(msg string, refs ...lacuna.FieldRef) lacuna.Lacuna {
		return lacuna.Lacuna{SourceFields: refs, Type: lacuna.DroppedField, Message: msg}
	}
	const (
		bWritten = "b set aside: the lens from 0.0 to 1.0 writes its own value for it"
		kindA    = `{kind: "a"}`
	)

	for name, c := range map[string]struct {
		lineage, data, want string
		lacunas             []lacuna.Lacuna
	}{
		// Referred to in a condition only: kind decides which isA is written.
		// It is the lens's, whatever the target makes of it.
		"condition: closed target": {
			lineage: discriminatorLineage("", ""),
			data:    `{kind: "a", n: 1}`, want: `{"isA":true,"n":1}`,
		},
		"condition: target declares it": {
			lineage: discriminatorLineage("kind?: string", ""),
			data:    `{kind: "b"}`, want: `{"isA":false}`,
		},
		"condition: open target": {
			lineage: discriminatorLineage("...", ""),
			data:    kindA, want: `{"isA":true}`,
		},
		"condition: written back": {
			lineage: discriminatorLineage("kind?: string", `if v0_0.kind == "a" {kind: "a"}`),
			data:    kindA, want: `{"isA":true,"kind":"a"}`,
		},
		"condition: written otherwise": {
			lineage: discriminatorLineage("kind?: string", `kind: "z"`),
			data:    kindA, want: `{"isA":true,"kind":"z"}`,
		},

		// Written without being referred to.
		"written: constant": {
			lineage: overwriteLineage("0"),
			data:    `{a: "x", b: 5}`, want: `{"a":"x","b":0,"c":"c"}`,
			lacunas: []lacuna.Lacuna{dropped(bWritten, ref("b", int64(5)))},
		},
		"written: from another field": {
			lineage: overwriteLineage("len(v0_0.a)"),
			data:    `{a: "xyz", b: 5}`, want: `{"a":"xyz","b":3,"c":"c"}`,
			lacunas: []lacuna.Lacuna{dropped(bWritten, ref("b", int64(5)))},
		},
		"written: the same value": {
			lineage: overwriteLineage("0"),
			data:    `{a: "x", b: 0}`, want: `{"a":"x","b":0,"c":"c"}`,
		},

		// Referred to by an arm, whichever arm fired.
		"arm: copy": {
			lineage: armsLineage,
			data:    `{mode: "copy", b: "v"}`, want: `{"mode":"copy","x":"v"}`,
		},
		"arm: computed": {
			lineage: armsLineage,
			data:    `{mode: "upper", b: "v"}`, want: `{"mode":"upper","x":"v!"}`,
		},
		"arm: constant": {
			lineage: armsLineage,
			data:    `{mode: "fixed", b: "v"}`, want: `{"mode":"fixed","x":"fixed"}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := cuecontext.New()
			lin, err := bindString(t, NewRuntime(ctx), c.lineage)
			if err != nil {
				t.Fatal(err)
			}
			in, err := lin.First().Validate(ctx.CompileString(c.data))
			if err != nil {
				t.Fatal(err)
			}
			out, lac, err := in.Translate(SV(1, 0))
			if err != nil {
				t.Fatal(err)
			}
			if got := mustJSON(t, out.Underlying()); got != c.want {
				t.Errorf("got %s\nwant %s", got, c.want)
			}
			if c.lacunas == nil {
				c.lacunas = []lacuna.Lacuna{}
			}
			want := lacuna.TranslationLacunas{{From: lacuna.SyntacticVersion(SV(0, 0)), To: lacuna.SyntacticVersion(SV(1, 0)), Lacunas: c.lacunas}}
			if g, w := mustJSON(t, lac), mustJSON(t, want); g != w {
				t.Errorf("lacunas: got %s\nwant %s", g, w)
			}
		})
	}
}

// defaultsLineage steps from 0.0, whose schema is s0, to 1.0, whose schema
// is s1, through a lens that writes body.
func defaultsLineage(s0, s1, body string) string {
	return `lin: thema.Lineage & {
	name: "defaults"
	schemas: {
		"0": "0": schema: ` + s0 + `
		"1": "0": schema: ` + s1 + `
	}
	lenses: {
		"0.0->1.0": {
			v0_0: _
			v1_0: ` + body + `
		}
		"1.0->0.0": {
			v1_0: _
			v0_0: {a: v1_0.a}
		}
	}
}`
}

// minorDefaultLineage's 0.1 adds a defaulted z, which the step back to 0.0
// loses.
const minorDefaultLineage = `lin: thema.Lineage & {
	name: "minordefault"
	schemas: {
		"0": "0": schema: {a: string}
		"0": "1": schema: {a: string, z: *0 | int}
	}
	lenses: "0.1->0.0": {
		v0_1: _
		v0_0: {a: v0_1.a}
	}
}`

// A value equal to what the source schema fills by itself and absence are the
// same information, so setting one aside raises no lacuna: a scalar equal to
// its default, or a struct whose fields all are. Whether the instance wrote
// the value or the schema filled it is not asked.
func TestTranslateLostDefaults(t *testing.T) {
	ref := func(path string, v interface{}) lacuna.FieldRef { return lacuna.FieldRef{Path: path, Value: v} }
	dropped := func(msg string, refs ...lacuna.FieldRef) lacuna.Lacuna {
		return lacuna.Lacuna{SourceFields: refs, Type: lacuna.DroppedField, Message: msg}
	}
	var (
		removed = defaultsLineage(`{a: string, x: *1 | int}`, `{a: string}`, `{a: v0_0.a}`)
		structs = defaultsLineage(`{a: string, o?: {x: *1 | int, y?: int}}`, `{a: string}`, `{a: v0_0.a}`)
		lists   = defaultsLineage(`{a: string, o: {l: [...int]}}`, `{a: string}`, `{a: v0_0.a}`)
		mixed   = defaultsLineage(`{a: string, x: *1 | int, w: *"w" | string}`, `{a: string}`, `{a: v0_0.a}`)
		written = defaultsLineage(`{a: string, b: *0 | int}`, `{a: string, b: int}`, `{a: v0_0.a, b: 7}`)
		fixed   = defaultsLineage(`{a: string, k: "k"}`, `{a: string}`, `{a: v0_0.a}`)
		closedX = "x set aside: 1.0 does not declare it in the instance, which it does not leave open"
	)

	for name, c := range map[string]struct {
		lineage, data, want string
		minor               bool // from 0.1 to 0.0, rather than 0.0 to 1.0
		lacunas             []lacuna.Lacuna
	}{
		"removed: default written": {lineage: removed, data: `{a: "q", x: 1}`, want: `{"a":"q"}`},
		"removed: default filled":  {lineage: removed, data: `{a: "q"}`, want: `{"a":"q"}`},
		"removed: other than default": {
			lineage: removed, data: `{a: "q", x: 5}`, want: `{"a":"q"}`,
			lacunas: []lacuna.Lacuna{dropped(closedX, ref("x", int64(5)))},
		},
		"removed: constant": {lineage: fixed, data: `{a: "q", k: "k"}`, want: `{"a":"q"}`},

		"minor: default": {
			lineage: minorDefaultLineage, data: `{a: "q", z: 0}`, want: `{"a":"q"}`,
			minor: true,
		},
		"minor: other than default": {
			lineage: minorDefaultLineage, data: `{a: "q", z: 5}`, want: `{"a":"q"}`,
			minor:   true,
			lacunas: []lacuna.Lacuna{dropped("z set aside: 0.0 does not declare it in the instance, which it does not leave open", ref("z", int64(5)))},
		},

		"struct: empty":      {lineage: structs, data: `{a: "q", o: {}}`, want: `{"a":"q"}`},
		"struct: empty list": {lineage: lists, data: `{a: "q", o: {l: []}}`, want: `{"a":"q"}`},
		"struct: list": {
			lineage: lists, data: `{a: "q", o: {l: [1]}}`, want: `{"a":"q"}`,
			lacunas: []lacuna.Lacuna{dropped("o set aside: 1.0 does not declare it in the instance, which it does not leave open", ref("o", map[string]interface{}{"l": []interface{}{1.0}}))},
		},
		"struct: defaults written": {lineage: structs, data: `{a: "q", o: {x: 1}}`, want: `{"a":"q"}`},
		"struct: other than default": {
			lineage: structs, data: `{a: "q", o: {x: 2}}`, want: `{"a":"q"}`,
			lacunas: []lacuna.Lacuna{dropped("o set aside: 1.0 does not declare it in the instance, which it does not leave open", ref("o", map[string]interface{}{"x": 2.0}))},
		},
		"struct: optional field present": {
			lineage: structs, data: `{a: "q", o: {y: 3}}`, want: `{"a":"q"}`,
			lacunas: []lacuna.Lacuna{dropped("o set aside: 1.0 does not declare it in the instance, which it does not leave open", ref("o", map[string]interface{}{"y": 3.0}))},
		},

		// One lacuna per struct per cause names only the fields that held
		// more than their default.
		"mixed: one default": {
			lineage: mixed, data: `{a: "q", x: 1, w: "v"}`, want: `{"a":"q"}`,
			lacunas: []lacuna.Lacuna{dropped("w set aside: 1.0 does not declare it in the instance, which it does not leave open", ref("w", "v"))},
		},
		"mixed: neither default": {
			lineage: mixed, data: `{a: "q", x: 5, w: "v"}`, want: `{"a":"q"}`,
			lacunas: []lacuna.Lacuna{dropped("x, w set aside: 1.0 does not declare them in the instance, which it does not leave open", ref("x", int64(5)), ref("w", "v"))},
		},

		// The lens writes its own value without referring to the source's.
		"written: source default": {lineage: written, data: `{a: "q", b: 0}`, want: `{"a":"q","b":7}`},
		"written: other than default": {
			lineage: written, data: `{a: "q", b: 3}`, want: `{"a":"q","b":7}`,
			lacunas: []lacuna.Lacuna{dropped("b set aside: the lens from 0.0 to 1.0 writes its own value for it", ref("b", int64(3)))},
		},
	} {
		t.Run(name, func(t *testing.T) {
			from, to := SV(0, 0), SV(1, 0)
			if c.minor {
				from, to = SV(0, 1), SV(0, 0)
			}
			ctx := cuecontext.New()
			lin, err := bindString(t, NewRuntime(ctx), c.lineage)
			if err != nil {
				t.Fatal(err)
			}
			sch, err := lin.Schema(from)
			if err != nil {
				t.Fatal(err)
			}
			in, err := sch.Validate(ctx.CompileString(c.data))
			if err != nil {
				t.Fatal(err)
			}
			out, lac, err := in.Translate(to)
			if err != nil {
				t.Fatal(err)
			}
			if got := mustJSON(t, out.Underlying()); got != c.want {
				t.Errorf("got %s\nwant %s", got, c.want)
			}
			if c.lacunas == nil {
				c.lacunas = []lacuna.Lacuna{}
			}
			want := lacuna.TranslationLacunas{{From: lacuna.SyntacticVersion(from), To: lacuna.SyntacticVersion(to), Lacunas: c.lacunas}}
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
