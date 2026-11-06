package thema_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/exemplars"
)

// concurrentCase is an exemplar and data for it, with the Go type its first
// schema is bound to.
type concurrentCase struct {
	lineage thema.LineageFactory
	data    string
	typed   func(thema.Schema, []byte) (string, error)
}

type rename00 struct {
	Before    string `json:"before"`
	Unchanged string `json:"unchanged"`
}

// typedAs binds T to a schema and reports what ValidateTyped decodes from
// data.
func typedAs[T thema.Assignee](t T) func(thema.Schema, []byte) (string, error) {
	return func(sch thema.Schema, data []byte) (string, error) {
		if err := thema.AssignableTo(sch, t); err != nil {
			return "", err
		}
		ts, err := thema.BindType(sch, t)
		if err != nil {
			return "", err
		}
		ti, err := ts.ValidateTyped(sch.Lineage().Runtime().Context().CompileBytes(data))
		if err != nil {
			return "", err
		}
		v, err := ti.Value()
		return fmt.Sprintf("%+v", v), err
	}
}

var concurrentCases = map[string]concurrentCase{
	"rename":        {lineage: exemplars.RenameLineage, data: `{"before": "a", "unchanged": "b"}`, typed: typedAs(&rename00{})},
	"expand":        {lineage: exemplars.ExpandLineage, data: `{"init": "x", "optional": 3, "withDefault": "baz"}`},
	"defaultchange": {lineage: exemplars.DefaultChangeLineage, data: `{"aunion": "bar"}`},
	"narrowing":     {lineage: exemplars.NarrowingLineage, data: `{"boolish": "true"}`},
	"disjunct":      {lineage: exemplars.DisjunctLineage, data: `{"rootfield": {"branch": 2, "branchtwo": "y"}}`},
	"single":        {lineage: exemplars.SingleLineage, data: `{"astring": "s", "anint": 3, "abool": true}`},
}

// exercise binds the case's lineage with rt, and records what every
// operation on it reports: each version's ID, which version the data is
// valid against, its translation to every version with the lacunas, what the
// typed path decodes, and that invalid data is refused.
func exercise(rt *thema.Runtime, c concurrentCase) (string, error) {
	lin, err := c.lineage(rt)
	if err != nil {
		return "", fmt.Errorf("%v", diag.Diagnostics(err))
	}
	var b strings.Builder
	for _, sch := range lin.All() {
		id, err := sch.ID()
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s %s\n", sch.Version(), id)
	}
	ctx := rt.Context()
	inst := lin.ValidateAny(ctx.CompileString(c.data))
	if inst == nil {
		return "", fmt.Errorf("%s is valid against no version of %s", c.data, lin.Name())
	}
	fmt.Fprintf(&b, "valid as %s\n", inst.Schema().Version())
	for _, to := range lin.All() {
		tinst, lacunas, err := inst.Translate(to.Version())
		if err != nil {
			return "", err
		}
		out, err := json.Marshal(tinst.Underlying())
		if err != nil {
			return "", err
		}
		lac, err := json.Marshal(lacunas)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "-> %s %s %s\n", to.Version(), out, lac)
	}
	if _, err := lin.First().Validate(ctx.CompileString(`{"undeclared": 1}`)); err == nil {
		return "", fmt.Errorf("%s accepts a field it does not declare", lin.First().Version())
	}
	if c.typed != nil {
		v, err := c.typed(lin.First(), []byte(c.data))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "typed %s\n", v)
	}
	return b.String(), nil
}

// One Runtime serves every operation from many goroutines at once: binding,
// IDs, validation, translation and the typed path report what they report
// run one at a time. Run it with -race.
func TestConcurrentRuntime(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	want := make(map[string]string, len(concurrentCases))
	for name, c := range concurrentCases {
		got, err := exercise(rt, c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want[name] = got
	}

	const workers = 8
	var wg sync.WaitGroup
	for i := range workers {
		for name, c := range concurrentCases {
			wg.Go(func() {
				got, err := exercise(rt, c)
				if err != nil {
					t.Errorf("%s, worker %d: %v", name, i, err)
				} else if got != want[name] {
					t.Errorf("%s, worker %d, run concurrently:\n%s\nand run alone:\n%s", name, i, got, want[name])
				}
			})
		}
	}
	wg.Wait()
}
