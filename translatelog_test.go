package thema

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema/diag"
)

// logRecords translates with a JSON logger at level and returns each record
// it wrote.
func logRecords(t *testing.T, level slog.Level, run func(TranslateOption) error) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level}))
	if err := run(WithLogger(l)); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

// steps summarizes each record: its message, versions and whether a lens ran.
func steps(recs []map[string]any) []string {
	var out []string
	for _, r := range recs {
		out = append(out, strings.Join([]string{r["msg"].(string), r["from"].(string), r["to"].(string), jsonText(r["lens"])}, " "))
	}
	return out
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// WithLogger reports each step at Debug: its versions, whether a lens ran,
// and its lacunas, with no instance data unless WithStepData asks for it.
func TestTranslateLogger(t *testing.T) {
	ctx := cuecontext.New()
	lin, err := bindString(t, NewRuntime(ctx), lacunaLineage)
	if err != nil {
		t.Fatal(err)
	}
	sch, err := lin.Schema(SV(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	inst, err := sch.Validate(ctx.CompileString(`{a: "x"}`))
	if err != nil {
		t.Fatal(err)
	}
	translate := func(opts ...TranslateOption) func(TranslateOption) error {
		return func(log TranslateOption) error {
			_, _, terr := inst.Translate(SV(1, 0), append([]TranslateOption{log}, opts...)...)
			return terr
		}
	}

	recs := logRecords(t, slog.LevelDebug, translate())
	want := []string{"translation step 0.0 0.1 false", "translation step 0.1 1.0 true"}
	if got := steps(recs); !slices.Equal(got, want) {
		t.Fatalf("steps %q, want %q", got, want)
	}
	if lacs := recs[1]["lacunas"].([]any); len(lacs) != 1 || !strings.Contains(jsonText(lacs[0]), "placeholder") {
		t.Errorf("lacunas of 0.1->1.0: %s", jsonText(recs[1]["lacunas"]))
	}
	for _, r := range recs {
		if r["level"] != "DEBUG" || r["duration"] == nil {
			t.Errorf("record %s lacks its level or duration", jsonText(r))
		}
		if _, ok := r["input"]; ok {
			t.Errorf("record %s holds data without WithStepData", jsonText(r))
		}
	}

	recs = logRecords(t, slog.LevelDebug, translate(WithStepData()))
	if in, out := recs[1]["input"], recs[1]["output"]; in != `{"a":"x"}` || out != `{"a":"x","id":-1}` {
		t.Errorf("step data: input %v, output %v", in, out)
	}

	if recs := logRecords(t, slog.LevelInfo, translate(WithStepData())); len(recs) != 0 {
		t.Errorf("a logger at Info got %d records", len(recs))
	}

	// Logging changes nothing about the translation.
	plain, plainLac, err := inst.Translate(SV(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	logged, loggedLac, err := inst.Translate(SV(1, 0), WithLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))), WithStepData())
	if err != nil {
		t.Fatal(err)
	}
	if !plain.Underlying().Equals(logged.Underlying()) || !reflect.DeepEqual(plainLac, loggedLac) {
		t.Errorf("translating with a logger gave %v and %v, without one %v and %v", logged.Underlying(), loggedLac, plain.Underlying(), plainLac)
	}

	// Backward steps run lenses, minor ones too; AsPredecessor takes the
	// options.
	back := logRecords(t, slog.LevelDebug, func(log TranslateOption) error {
		_, _, err := logged.Translate(SV(0, 0), log)
		return err
	})
	if got, want := steps(back), []string{"translation step 1.0 0.1 true", "translation step 0.1 0.0 true"}; !slices.Equal(got, want) {
		t.Errorf("backward steps %q, want %q", got, want)
	}
	pred := logRecords(t, slog.LevelDebug, func(log TranslateOption) error {
		_, _, err := logged.AsPredecessor(log)
		return err
	})
	if got, want := steps(pred), []string{"translation step 1.0 0.1 true"}; !slices.Equal(got, want) {
		t.Errorf("AsPredecessor steps %q, want %q", got, want)
	}
}

// An instance valid only through its schema's defaults renders with them, as
// the step sees it.
func TestTranslateLoggerDefaults(t *testing.T) {
	ctx := cuecontext.New()
	lin, err := bindString(t, NewRuntime(ctx), `lin: thema.Lineage & {
	name: "defaults"
	schemas: {
		"0": "0": schema: {a: int | *1}
		"0": "1": schema: {a: int | *1, b?: string}
	}
	lenses: "0.1->0.0": {v0_1: _, v0_0: a: v0_1.a}
}`)
	if err != nil {
		t.Fatal(err)
	}
	sch, err := lin.Schema(SV(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	inst, err := sch.Validate(ctx.CompileString(`{a: int}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func(TranslateOption) error{
		"Translate": func(log TranslateOption) error {
			_, _, err := inst.Translate(SV(0, 1), log, WithStepData())
			return err
		},
		"AsSuccessor": func(log TranslateOption) error {
			_, _, err := inst.AsSuccessor(log, WithStepData())
			return err
		},
	} {
		recs := logRecords(t, slog.LevelDebug, run)
		if len(recs) != 1 || recs[0]["input"] != `{"a":1}` || recs[0]["output"] != `{"a":1}` {
			t.Errorf("%s reported %s", name, jsonText(recs))
		}
	}
}

// AsSuccessor within a major version reports its one step, with the fields it
// did not map; Translate across a lens reports those the lens did not map,
// including one set aside, which is a lacuna too.
func TestTranslateLoggerUnmapped(t *testing.T) {
	ctx := cuecontext.New()
	lin, err := bindString(t, NewRuntime(ctx), `lin: thema.Lineage & {
	name: "open"
	schemas: {
		"0": "0": schema: {a: string, [=~"^x"]: _}
		"0": "1": schema: {a: string, b?: string, [=~"^x"]: _}
		"1": "0": schema: {c: string}
	}
	lenses: {
		"0.1->0.0": {v0_1: _, v0_0: a: v0_1.a}
		"0.1->1.0": {v0_1: _, v1_0: c: v0_1.a}
		"1.0->0.1": {v1_0: _, v0_1: a: v1_0.c}
	}
}`)
	if err != nil {
		t.Fatal(err)
	}
	sch, err := lin.Schema(SV(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	inst, err := sch.Validate(ctx.CompileString(`{a: "x", xtra: 1}`))
	if err != nil {
		t.Fatal(err)
	}
	recs := logRecords(t, slog.LevelDebug, func(log TranslateOption) error {
		_, _, err := inst.AsSuccessor(log)
		return err
	})
	if got := steps(recs); !slices.Equal(got, []string{"translation step 0.0 0.1 false"}) || jsonText(recs[0]["unmapped"]) != `["xtra"]` {
		t.Errorf("AsSuccessor reported %q, unmapped %s", got, jsonText(recs[0]["unmapped"]))
	}

	recs = logRecords(t, slog.LevelDebug, func(log TranslateOption) error {
		_, _, err := inst.Translate(SV(1, 0), log)
		return err
	})
	if len(recs) != 2 || jsonText(recs[1]["unmapped"]) != `["xtra"]` || recs[1]["lens"] != true || !strings.Contains(jsonText(recs[1]["lacunas"]), "xtra") {
		t.Errorf("Translate reported %s", jsonText(recs))
	}
}

// A step that fails is reported with its error and its diagnostics' codes,
// and its input with WithStepData. A bound lineage's lenses are total, so no
// step of one fails on purpose; the report is made directly.
func TestTranslateLoggerFailed(t *testing.T) {
	ctx := cuecontext.New()
	lin, err := bindString(t, NewRuntime(ctx), lacunaLineage)
	if err != nil {
		t.Fatal(err)
	}
	from, err := lin.Schema(SV(0, 1))
	if err != nil {
		t.Fatal(err)
	}
	to, err := lin.Schema(SV(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	cfg := newTranslateConfig([]TranslateOption{WithLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))), WithStepData()})
	cfg.failed(time.Now(), from.(*schemaDef), to.(*schemaDef), ctx.CompileString(`{a: "x"}`), diag.New(diag.E0307).Headline("the lens broke").Err())
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["msg"] != "translation step failed" || rec["error"] != "the lens broke" || jsonText(rec["codes"]) != `["E0307"]` || rec["input"] != `{"a":"x"}` || rec["from"] != "0.1" || rec["to"] != "1.0" {
		t.Errorf("reported %s", jsonText(rec))
	}
}
