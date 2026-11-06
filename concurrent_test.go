package thema_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/exemplars"
	"github.com/sdboyer/thema/load"
)

// concurrentCase is data for an exemplar lineage, with the Go type its first
// schema is bound to.
type concurrentCase struct {
	data  string
	typed func(thema.Schema, []byte) (string, error)
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

// concurrentCases are keyed by the exemplar's name, its field in the
// exemplars package.
var concurrentCases = map[string]concurrentCase{
	"rename":        {data: `{"before": "a", "unchanged": "b"}`, typed: typedAs(&rename00{})},
	"expand":        {data: `{"init": "x", "optional": 3, "withDefault": "baz"}`},
	"defaultchange": {data: `{"aunion": "bar"}`},
	"narrowing":     {data: `{"boolish": "true"}`},
	"disjunct":      {data: `{"rootfield": {"branch": 2, "branchtwo": "y"}}`},
	"single":        {data: `{"astring": "s", "anint": 3, "abool": true}`},
}

// exemplarsRoot is the exemplars package, loaded and built once: every
// exemplar lineage is a field of it, and they share its import of thema.
func exemplarsRoot(t *testing.T, rt *thema.Runtime) cue.Value {
	t.Helper()
	bi, err := load.InstancesWithThema(exemplars.CueFS(), ".")
	if err != nil {
		t.Fatal(err)
	}
	return rt.Context().BuildInstance(bi)
}

// bind binds the named exemplar from root.
func bind(root cue.Value, name string, rt *thema.Runtime) (thema.Lineage, error) {
	lin, err := thema.BindLineage(root, cue.MakePath(cue.Str(name), cue.Str("l")), rt)
	if err != nil {
		return nil, fmt.Errorf("%v", diag.Diagnostics(err))
	}
	return lin, nil
}

// exercise records what every operation on a bound lineage reports: each
// version's ID, which version the data is valid against, its translation to
// every version with the lacunas, what the typed path decodes, and that
// invalid data is refused.
func exercise(lin thema.Lineage, c concurrentCase) (string, error) {
	rt := lin.Runtime()
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

// alone binds and exercises every case one at a time, for what each reports.
func alone(t *testing.T, rt *thema.Runtime, root cue.Value) (map[string]thema.Lineage, map[string]string) {
	t.Helper()
	lins := make(map[string]thema.Lineage, len(concurrentCases))
	want := make(map[string]string, len(concurrentCases))
	for name, c := range concurrentCases {
		lin, err := bind(root, name, rt)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want[name], err = exercise(lin, c); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		lins[name] = lin
	}
	return lins, want
}

const workers = 8

// Lineages of one package are bound from it concurrently, each by several
// goroutines at once, and each reports what it reports bound alone. They
// share the package's import of thema, which CUE finalizes lazily when a
// reference into it is first followed: binding takes the Runtime's lock.
// Run it with -race.
func TestConcurrentBind(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	_, want := alone(t, rt, exemplarsRoot(t, rt))

	// A root no bind has yet touched.
	root := exemplarsRoot(t, rt)
	var wg sync.WaitGroup
	for i := range workers {
		for name, c := range concurrentCases {
			wg.Go(func() {
				lin, err := bind(root, name, rt)
				if err != nil {
					t.Errorf("%s, worker %d: %v", name, i, err)
					return
				}
				if got, err := exercise(lin, c); err != nil {
					t.Errorf("%s, worker %d: %v", name, i, err)
				} else if got != want[name] {
					t.Errorf("%s, worker %d, bound concurrently:\n%s\nand alone:\n%s", name, i, got, want[name])
				}
			})
		}
	}
	wg.Wait()
}

// A lineage bound once is used by many goroutines at once, while others bind
// more lineages from the same package: IDs, validation, translation and the
// typed path report what they report run one at a time. Run it with -race.
func TestConcurrentUse(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	root := exemplarsRoot(t, rt)
	lins, want := alone(t, rt, root)

	var wg sync.WaitGroup
	for i := range workers {
		for name, c := range concurrentCases {
			wg.Go(func() {
				if got, err := exercise(lins[name], c); err != nil {
					t.Errorf("%s, worker %d: %v", name, i, err)
				} else if got != want[name] {
					t.Errorf("%s, worker %d, used concurrently:\n%s\nand alone:\n%s", name, i, got, want[name])
				}
			})
			wg.Go(func() {
				if _, err := bind(root, name, rt); err != nil {
					t.Errorf("%s, worker %d: %v", name, i, err)
				}
			})
		}
	}
	wg.Wait()
}
