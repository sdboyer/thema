package thema

import (
	"context"
	"log/slog"
	"time"

	"cuelang.org/go/cue"

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
// was plain unification within a major version, the paths of the fields
// carried around the lens (carried), the lacunas it emitted and how long it
// took. A step that fails is reported as "translation step failed", with its
// error. The handler's level decides whether anything is reported.
func WithLogger(l *slog.Logger) TranslateOption {
	return func(c *translateConfig) {
		c.log = l
	}
}

// WithStepData adds the instance each step was given (input) and the one it
// produced (output), as JSON, to the records [WithLogger] reports. Instances
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
// ran, and which fields were carried around it.
type stepInfo struct {
	lens    bool
	carried []taken
}

func (c translateConfig) enabled() bool {
	return c.log != nil && c.log.Enabled(context.Background(), slog.LevelDebug)
}

// step reports a step that completed.
func (c translateConfig) step(start time.Time, from, to SyntacticVersion, info stepInfo, input, output cue.Value, step lacuna.TranslationStep) {
	if !c.enabled() {
		return
	}
	carried := make([]string, 0, len(info.carried))
	for _, x := range info.carried {
		carried = append(carried, x.path.String())
	}
	attrs := []slog.Attr{
		slog.String("from", from.String()),
		slog.String("to", to.String()),
		slog.Bool("lens", info.lens),
		slog.Any("carried", carried),
		slog.Any("lacunas", step.Lacunas),
		slog.Duration("duration", time.Since(start)),
	}
	if c.data {
		attrs = append(attrs, slog.Any("input", jsonValue{input}), slog.Any("output", jsonValue{output}))
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "translation step", attrs...)
}

// failed reports a step that failed.
func (c translateConfig) failed(start time.Time, from, to SyntacticVersion, input cue.Value, err error) {
	if !c.enabled() {
		return
	}
	attrs := []slog.Attr{
		slog.String("from", from.String()),
		slog.String("to", to.String()),
		slog.Duration("duration", time.Since(start)),
		slog.Any("error", err),
	}
	if c.data {
		attrs = append(attrs, slog.Any("input", jsonValue{input}))
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "translation step failed", attrs...)
}

// jsonValue renders an instance as JSON, only when a handler takes the
// record it is in. The value leaves thema here, as data in a log.
type jsonValue struct{ v cue.Value }

func (j jsonValue) LogValue() slog.Value {
	b, err := j.v.MarshalJSON()
	if err != nil {
		return slog.StringValue("cannot be rendered as JSON: " + err.Error())
	}
	return slog.StringValue(string(b))
}
