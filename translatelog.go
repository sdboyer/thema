package thema

import (
	"context"
	"log/slog"
	"time"

	"cuelang.org/go/cue"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/lacuna"
)

// A TranslateOption configures one translation: a call to
// [Instance.Translate], [Instance.AsSuccessor] or [Instance.AsPredecessor].
type TranslateOption func(*translateConfig)

type translateConfig struct {
	log  *slog.Logger
	data bool
}

// WithLogger reports each step of the translation to l, at
// [slog.LevelDebug], as it completes: a record "translation step" with the
// versions it translated from and to, whether a lens ran (lens) or the step
// was plain unification within a major version, the paths of the fields the
// step did not map (unmapped), the lacunas it emitted and how long it took.
// The unmapped fields are those the source schema does not declare and those
// its lens leaves alone; translation decides each one's fate itself, carrying,
// promoting or setting it aside (with a lacuna), or dropping one that only
// held a default. A step that fails is reported as "translation step failed",
// with its error and the codes of its diagnostics. The handler's level decides
// whether anything is reported.
func WithLogger(l *slog.Logger) TranslateOption {
	return func(c *translateConfig) {
		c.log = l
	}
}

// WithStepData adds the instance each step was given (input) and the one it
// produced (output), each unified with its schema as the step sees it, as
// JSON, to the records [WithLogger] reports. Instances
// can be large, and can hold data that should not reach a log, so it is off
// unless asked for. Without WithLogger it does nothing.
func WithStepData() TranslateOption {
	return func(c *translateConfig) {
		c.data = true
	}
}

func newTranslateConfig(opts []TranslateOption) translateConfig {
	var c translateConfig
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// stepInfo is what a step did besides its result and lacunas: whether a lens
// ran, and which fields it did not map.
type stepInfo struct {
	lens     bool
	unmapped []taken
}

func (c translateConfig) enabled() bool {
	return c.log != nil && c.log.Enabled(context.Background(), slog.LevelDebug)
}

// step reports a step that completed.
func (c translateConfig) step(start time.Time, from, to *schemaDef, info stepInfo, input, output cue.Value, step lacuna.TranslationStep) {
	if !c.enabled() {
		return
	}
	unmapped := make([]string, 0, len(info.unmapped))
	for _, x := range info.unmapped {
		unmapped = append(unmapped, x.path.String())
	}
	attrs := []slog.Attr{
		slog.String("from", from.Version().String()),
		slog.String("to", to.Version().String()),
		slog.Bool("lens", info.lens),
		slog.Any("unmapped", unmapped),
		slog.Any("lacunas", step.Lacunas),
		slog.Duration("duration", time.Since(start)),
	}
	if c.data {
		attrs = append(attrs, slog.Any("input", jsonValue{input, from.def}), slog.Any("output", jsonValue{output, to.def}))
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "translation step", attrs...)
}

// failed reports a step that failed.
func (c translateConfig) failed(start time.Time, from, to *schemaDef, input cue.Value, err error) {
	if !c.enabled() {
		return
	}
	var codes []string
	for _, d := range diag.Diagnostics(err) {
		codes = append(codes, string(d.Code))
	}
	attrs := []slog.Attr{
		slog.String("from", from.Version().String()),
		slog.String("to", to.Version().String()),
		slog.Duration("duration", time.Since(start)),
		slog.Any("error", err),
		slog.Any("codes", codes),
	}
	if c.data {
		attrs = append(attrs, slog.Any("input", jsonValue{input, from.def}))
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "translation step failed", attrs...)
}

// jsonValue renders an instance, unified with its schema as a step sees it,
// as JSON, only when a handler takes the record it is in. The schema gives
// the instance its defaults: an instance can be valid only through one. The
// value leaves thema here, as data in a log.
type jsonValue struct{ v, sch cue.Value }

func (j jsonValue) LogValue() slog.Value {
	b, err := j.sch.Unify(j.v).MarshalJSON()
	if err != nil {
		return slog.StringValue("cannot be rendered as JSON: " + err.Error())
	}
	return slog.StringValue(string(b))
}
