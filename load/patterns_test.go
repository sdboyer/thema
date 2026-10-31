package load

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/lacuna"
)

// Only a field with a predictable name is declared: a key a pattern
// constraint matches is undeclared data (docs/translation.md, A4). The
// implicit copy of a map both versions declare copies it whole, keys and all;
// a lens reads or writes no key under a pattern by name, and the diagnostic
// says how to declare one. Declaring it optional in a minor version first lets
// the values instances carry there land in it, for the next lens to map.
func TestAuthoringPatternKeys(t *testing.T) {
	const first, tip = "history/0.0.thema", "1.0.thema"
	const labels = "Schema: {\n\tlabels: [string]: string\n}\n"
	type translation struct {
		from, to thema.SyntacticVersion
		data     string
		want     string
		lacunas  []lacuna.Type
	}
	for name, c := range map[string]struct {
		files        map[string]string
		translations []translation
		// err, if set, is the code loading the lineage fails with, and help
		// text its help must contain.
		err  diag.Code
		help string
	}{
		"implicit copy carries the keys": {
			files: map[string]string{
				first: "Schema: {\n\tlabels: [string]: string\n\ta: string\n}\n",
				tip:   "Schema: {\n\tlabels: [string]: string\n\ta: int\n}\n\nForwardLens: {\n\tv0_0: _\n\tv1_0: {\n\t\ta: 1\n\t}\n}\n\nBackwardLens: {\n\tv1_0: _\n\tv0_0: {\n\t\ta: \"x\"\n\t}\n}\n",
			},
			// Each lens writes its own a without referring to the source's,
			// so the source's a is set aside (B6).
			translations: []translation{
				{from: thema.SV(0, 0), to: thema.SV(1, 0), data: `{labels: {env: "prod"}, a: "y"}`, want: `{"labels":{"env":"prod"},"a":1}`, lacunas: []lacuna.Type{lacuna.DroppedField}},
				{from: thema.SV(1, 0), to: thema.SV(0, 0), data: `{labels: {env: "prod"}, a: 2}`, want: `{"labels":{"env":"prod"},"a":"x"}`, lacunas: []lacuna.Type{lacuna.DroppedField}},
			},
		},
		"reads a key": {
			files: map[string]string{
				first: labels,
				tip:   "Schema: {\n\tlabels: [string]: string\n\tenv:    string\n}\n\nForwardLens: {\n\tv0_0: _\n\tv1_0: {\n\t\tenv: _coalesce(v0_0.labels.env, \"none\")\n\t}\n}\n",
			},
			err:  diag.E0340,
			help: "declare labels.env as an optional field in a new minor version",
		},
		"writes a key": {
			files: map[string]string{
				first: "Schema: {\n\tenv: string\n}\n",
				tip:   "Schema: {\n\tlabels: [string]: string\n}\n\nForwardLens: {\n\tv0_0: _\n\tv1_0: {\n\t\tlabels: {\n\t\t\tenv: v0_0.env\n\t\t}\n\t}\n}\n",
			},
			err:  diag.E0341,
			help: "declare labels.env in 1.0, or copy the struct that holds it whole",
		},
		// The remedy: 0.1 declares labels.env, so a value instances carry
		// there is promoted into it, and the lens from 0.1 moves it to env.
		// The other keys of labels ride along.
		"declared first, then mapped": {
			files: map[string]string{
				first:       labels,
				"0.1.thema": "Schema: {\n\tlabels: {\n\t\tenv?: string\n\t\t[string]: string\n\t}\n}\n",
				tip:         "Schema: {\n\tlabels: [string]: string\n\tenv:    string\n}\n\nForwardLens: {\n\tv0_1: _\n\tv1_0: {\n\t\tenv: _coalesce(v0_1.labels.env, \"none\")\n\t}\n}\n",
			},
			translations: []translation{
				{from: thema.SV(0, 0), to: thema.SV(1, 0), data: `{labels: {env: "prod", x: "y"}}`, want: `{"labels":{"x":"y"},"env":"prod"}`},
				{from: thema.SV(0, 0), to: thema.SV(1, 0), data: `{labels: {x: "y"}}`, want: `{"labels":{"x":"y"},"env":"none"}`},
				// Back to 0.0, env has nowhere to go, and labels comes back whole.
				{from: thema.SV(1, 0), to: thema.SV(0, 0), data: `{labels: {env: "prod", x: "y"}, env: "dev"}`, want: `{"labels":{"env":"prod","x":"y"}}`, lacunas: []lacuna.Type{lacuna.DroppedField}},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := fstest.MapFS{}
			for p, text := range c.files {
				dir["ship/"+p] = &fstest.MapFile{Data: []byte(text)}
			}
			ctx := cuecontext.New()
			lin, err := AuthoringLineage(dir, "ship", thema.NewRuntime(ctx))
			if c.err != "" {
				var found bool
				for _, d := range diag.Diagnostics(err) {
					if d.Code != c.err {
						continue
					}
					found = true
					if !strings.Contains(strings.Join(d.Help, "\n"), c.help) {
						t.Errorf("%s: help %q does not contain %q", d.Code, d.Help, c.help)
					}
				}
				if !found {
					t.Fatalf("loading: got %v, want %s", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(cerrors.Details(err, nil))
			}
			for _, tr := range c.translations {
				inst, err := thema.SchemaP(lin, tr.from).Validate(ctx.CompileString(tr.data))
				if err != nil {
					t.Fatal(err)
				}
				out, lac, err := inst.Translate(tr.to)
				if err != nil {
					t.Fatal(err)
				}
				got, err := json.Marshal(out.Underlying())
				if err != nil {
					t.Fatal(err)
				}
				var types []lacuna.Type
				for _, l := range lac.AsList() {
					types = append(types, l.Type)
				}
				if string(got) != tr.want {
					t.Errorf("%s %s -> %s: got %s, want %s", tr.data, tr.from, tr.to, got, tr.want)
				}
				if fmt.Sprint(types) != fmt.Sprint(tr.lacunas) {
					t.Errorf("%s %s -> %s: got lacunas %v, want %v", tr.data, tr.from, tr.to, types, tr.lacunas)
				}
			}
		})
	}
}
