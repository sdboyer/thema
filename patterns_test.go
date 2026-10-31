package thema

import (
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/lacuna"
)

// The lineage the pattern tests translate across. 0.1 declares labels.team,
// which 0.0 matches only by a pattern. The lens to 1.0 copies labels whole
// into tags and meta whole into info, and writes counts, whose pattern in
// 1.0 accepts fewer names and more values; 1.0 declares counts.total and
// info.team.
const patternsLineage = `lin: thema.Lineage & {
	name: "patterns"
	schemas: {
		"0": "0": schema: {
			labels: [string]: string
			counts: [string]: string
			meta: {owner: string, [string]: string}
		}
		"0": "1": schema: {
			labels: {team?: string, [string]: string}
			counts: [string]: string
			meta: {owner: string, [string]: string}
		}
		"1": "0": schema: {
			tags: [string]: string
			counts: {total?: int, [=~"^n"]: int | string}
			info: {owner: string, team?: string, [string]: string}
		}
	}
	lenses: {
		"0.1->0.0": {
			v0_1: _
			v0_0: {labels: v0_1.labels, counts: v0_1.counts, meta: v0_1.meta}
		}
		"0.1->1.0": {
			v0_1: _
			v1_0: {tags: v0_1.labels, counts: {}, info: v0_1.meta}
		}
		"1.0->0.1": {
			v1_0: _
			v0_1: {labels: v1_0.tags, counts: {}, meta: v1_0.info}
		}
	}
}`

// Only a field with a predictable name is declared: a key a pattern
// constraint matches is undeclared data, carried like data under ..., and a
// lens copies it only with the struct that holds it (docs/translation.md, A4).
func TestTranslateCarriesPatternKeys(t *testing.T) {
	ctx := cuecontext.New()
	lin, err := bindString(t, NewRuntime(ctx), patternsLineage)
	if err != nil {
		t.Fatal(err)
	}
	ref := func(path string, v interface{}) lacuna.FieldRef { return lacuna.FieldRef{Path: path, Value: v} }
	dropped := func(msg string, refs ...lacuna.FieldRef) lacuna.Lacuna {
		return lacuna.Lacuna{SourceFields: refs, Type: lacuna.DroppedField, Message: msg}
	}
	lossy := func(msg string, src, tgt lacuna.FieldRef) lacuna.Lacuna {
		return lacuna.Lacuna{SourceFields: []lacuna.FieldRef{src}, TargetFields: []lacuna.FieldRef{tgt}, Type: lacuna.LossyFieldMapping, Message: msg}
	}
	type step struct {
		from, to SyntacticVersion
		lacunas  []lacuna.Lacuna
	}

	for name, c := range map[string]struct {
		from, to SyntacticVersion
		data     string
		want     string
		steps    []step
		// back, if set, is the result of translating want back to from,
		// which must raise no lacuna.
		back string
	}{
		// labels.team is promoted into the field 0.1 declares; the other keys
		// are carried under the patterns that admit them.
		"minor step promotes and carries": {
			from: SV(0, 0), to: SV(0, 1),
			data:  `{labels: {team: "t", env: "e"}, counts: {a: "1"}, meta: {owner: "o", x: "y"}}`,
			want:  `{"labels":{"team":"t","env":"e"},"counts":{"a":"1"},"meta":{"owner":"o","x":"y"}}`,
			steps: []step{{SV(0, 0), SV(0, 1), []lacuna.Lacuna{}}},
			back:  `{"labels":{"team":"t","env":"e"},"counts":{"a":"1"},"meta":{"owner":"o","x":"y"}}`,
		},
		// The keys of labels and meta ride along with the structs the lens
		// copies whole (C6); meta.team lands in the info.team 1.0 declares.
		// counts the lens writes itself: its keys keep their path, where n1
		// is carried under the pattern of 1.0 that admits it (C1), total is a
		// field 1.0 declares with another value (C3), and z is a name 1.0
		// admits not at all (C5).
		"major step": {
			from: SV(0, 0), to: SV(1, 0),
			data: `{labels: {team: "t", env: "e"}, counts: {n1: "1", total: "5", z: "q"}, meta: {owner: "o", team: "t2", x: "y"}}`,
			want: `{"tags":{"team":"t","env":"e"},"counts":{"n1":"1"},"info":{"owner":"o","team":"t2","x":"y"}}`,
			steps: []step{
				{SV(0, 0), SV(0, 1), []lacuna.Lacuna{}},
				{SV(0, 1), SV(1, 0), []lacuna.Lacuna{
					lossy("counts.total not carried: its value is not an instance of what 1.0 declares for it", ref("counts.total", "5"), ref("counts.total", nil)),
					dropped("counts.z set aside: 1.0 does not declare it in counts, which it does not leave open", ref("counts.z", "q")),
				}},
			},
		},
		// Back from 1.0, counts.total, which 1.0 declares and 0.1 matches
		// only by a pattern, is carried like the keys beside it, where its
		// value fits the pattern; tags and info come back whole.
		"backward": {
			from: SV(1, 0), to: SV(0, 0),
			data: `{tags: {env: "e"}, counts: {n1: 1, n2: "s", total: 2}, info: {owner: "o", team: "t2"}}`,
			want: `{"labels":{"env":"e"},"counts":{"n2":"s"},"meta":{"owner":"o","team":"t2"}}`,
			steps: []step{
				{SV(1, 0), SV(0, 1), []lacuna.Lacuna{
					dropped("counts.n1, counts.total set aside: the pattern constraint of 0.1 in counts does not accept their values", ref("counts.n1", int64(1)), ref("counts.total", int64(2))),
				}},
				{SV(0, 1), SV(0, 0), []lacuna.Lacuna{}},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			translate := func(from, to SyntacticVersion, data string) (string, lacuna.TranslationLacunas) {
				t.Helper()
				sch, err := lin.Schema(from)
				if err != nil {
					t.Fatal(err)
				}
				in, err := sch.Validate(ctx.CompileString(data))
				if err != nil {
					t.Fatal(err)
				}
				out, lac, err := in.Translate(to)
				if err != nil {
					t.Fatal(err)
				}
				return mustJSON(t, out.Underlying()), lac
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

// A lens neither reads nor writes a key that only a pattern constraint
// matches, nor does a lacuna name one; each diagnostic says how to declare it.
func TestBindRefusesPatternKeys(t *testing.T) {
	const schemas = `
		"0": "0": schema: {labels: [string]: string, env?: string}
		"1": "0": schema: {tags: [string]: string, env?: string}`
	for name, c := range map[string]struct {
		forward string
		code    diag.Code
		help    string
	}{
		"reads a key": {
			forward: `v1_0: {tags: v0_0.labels, env: v0_0.labels.env}`,
			code:    diag.E0362,
			help:    "declare labels.env as an optional field in a new minor version of the source schema",
		},
		"copies a key into a key": {
			forward: `v1_0: {tags: {env: v0_0.env}}`,
			code:    diag.E0305,
			help:    "declare tags.env in the target schema to write it",
		},
		"writes a constant key": {
			forward: `v1_0: {tags: {env: "prod"}}`,
			code:    diag.E0305,
			help:    "declare tags.env by name in schema 1.0",
		},
		"a lacuna names a key": {
			forward: `v1_0: {tags: v0_0.labels}
			lacunas: [{type: "LossyFieldMapping", message: "m", sourceFields: [{path: "labels.env", value: "x"}]}]`,
			code: diag.E0404,
			help: "declare labels.env by name in schema 0.0",
		},
	} {
		t.Run(name, func(t *testing.T) {
			src := `lin: thema.Lineage & {
	name: "patternkeys"
	schemas: {` + schemas + `
	}
	lenses: {
		"0.0->1.0": {
			v0_0: _
			` + c.forward + `
		}
		"1.0->0.0": {
			v1_0: _
			v0_0: {labels: v1_0.tags}
		}
	}
}`
			_, err := bindString(t, NewRuntime(cuecontext.New()), src)
			if err == nil {
				t.Fatal("bound")
			}
			var found bool
			for _, d := range diag.Diagnostics(err) {
				if d.Code != c.code {
					continue
				}
				found = true
				helped := false
				for _, h := range d.Help {
					helped = helped || strings.Contains(h, c.help)
				}
				if !helped {
					t.Errorf("%s: no help containing %q in %v", d.Code, c.help, d.Help)
				}
			}
			if !found {
				t.Errorf("no %s in %v", c.code, err)
			}
		})
	}
}
