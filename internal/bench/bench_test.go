package bench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	jsch "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/encoding/gocode"
	tjsonschema "github.com/sdboyer/thema/encoding/jsonschema"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/util"
	"github.com/sdboyer/thema/load"
)

// decoder turns JSON bytes into an Order, or refuses them.
//
// A CUE pipeline compiles the bytes in a cue.Context of their own, never in
// the Runtime's: a context keeps everything compiled in it for as long as it
// lives, about 8.7KB for each of these orders, and a long-lived one grows
// without bound. Unifying with a schema from another context keeps nothing.
type decoder func(b []byte) (Order, error)

// pipeline is one way from bytes to an Order.
type pipeline struct {
	name string
	// validates is false for the pipeline that only decodes.
	validates bool
	decode    decoder
}

// setup is the lineage's schema 0.0 and every pipeline built on it, each
// built once, as a server would.
type setup struct {
	rt        *thema.Runtime
	sch       thema.Schema
	pipelines []pipeline
}

func newSetup(tb testing.TB) *setup {
	tb.Helper()
	rt := thema.NewRuntime(cuecontext.New())
	lin, err := load.AuthoringLineage(os.DirFS("testdata"), "order", rt)
	if err != nil {
		tb.Fatal(diag.Diagnostics(err))
	}
	sch := lin.First()

	ts, err := thema.BindType(sch, &Order{})
	if err != nil {
		tb.Fatal(err)
	}

	f, err := tjsonschema.GenerateSchema(sch)
	if err != nil {
		tb.Fatal(err)
	}
	doc, err := rt.Context().BuildFile(f).MarshalJSON()
	if err != nil {
		tb.Fatal(err)
	}
	parsed, err := jsch.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		tb.Fatal(err)
	}
	c := jsch.NewCompiler()
	if err = c.AddResource("order.json", parsed); err != nil {
		tb.Fatal(err)
	}
	js, err := c.Compile("order.json")
	if err != nil {
		tb.Fatal(err)
	}

	// Schema.Validate closes the schema on every call; this is the same
	// schema, closed once.
	closed := cuetil.CloseRecursive(rt.Context(), sch.Underlying().LookupPath(cue.MakePath(cue.Hid("_schema", util.ThemaPackageID))))

	return &setup{rt: rt, sch: sch, pipelines: []pipeline{
		{name: "json", decode: func(b []byte) (Order, error) {
			var o Order
			err := json.Unmarshal(b, &o)
			return o, err
		}},
		{name: "jsonschema", validates: true, decode: func(b []byte) (Order, error) {
			var o Order
			inst, err := jsch.UnmarshalJSON(bytes.NewReader(b))
			if err != nil {
				return o, err
			}
			if err = js.Validate(inst); err != nil {
				return o, err
			}
			err = json.Unmarshal(b, &o)
			return o, err
		}},
		{name: "thema", validates: true, decode: func(b []byte) (Order, error) {
			ti, err := ts.ValidateTyped(cuecontext.New().CompileBytes(b))
			if err != nil {
				return Order{}, err
			}
			o, err := ti.Value()
			if err != nil {
				return Order{}, err
			}
			return *o, nil
		}},
		{name: "cue", validates: true, decode: func(b []byte) (Order, error) {
			var o Order
			v := closed.Unify(cuecontext.New().CompileBytes(b))
			if err := v.Validate(cue.Concrete(true)); err != nil {
				return o, err
			}
			err := v.Decode(&o)
			return o, err
		}},
	}}
}

// order is an Order of n items, as JSON, with every optional field.
func order(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"id":"ord_7k2m9x4q","customer":{"name":"Ada Lovelace","email":"ada@example.com","tier":"pro"},"status":"paid","items":[`)
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"sku":"ABC-%04d","quantity":%d,"priceCents":%d}`, i%10000, i%1000+1, (i*7919)%100000000)
	}
	b.WriteString(`],"note":"leave at the door","priority":3}`)
	return []byte(b.String())
}

// sizes are the payloads benchmarked: an order of 1, 10 and 1000 items.
var sizes = []struct {
	name  string
	items int
}{{"1item", 1}, {"10items", 10}, {"1000items", 1000}}

// mutated is a one-item order with one change, made by edit on its decoded
// JSON.
func mutated(edit func(o map[string]any)) []byte {
	var o map[string]any
	if err := json.Unmarshal(order(1), &o); err != nil {
		panic(err)
	}
	edit(o)
	b, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}
	return b
}

func customer(o map[string]any) map[string]any { return o["customer"].(map[string]any) }
func item(o map[string]any) map[string]any {
	return o["items"].([]any)[0].(map[string]any)
}

// invalid is data schema 0.0 refuses, each for one reason.
var invalid = map[string][]byte{
	"no id":                  mutated(func(o map[string]any) { delete(o, "id") }),
	"id off pattern":         mutated(func(o map[string]any) { o["id"] = "order-1" }),
	"tier not in enum":       mutated(func(o map[string]any) { customer(o)["tier"] = "gold" }),
	"status not in enum":     mutated(func(o map[string]any) { o["status"] = "lost" }),
	"empty name":             mutated(func(o map[string]any) { customer(o)["name"] = "" }),
	"name over 100 runes":    mutated(func(o map[string]any) { customer(o)["name"] = strings.Repeat("é", 101) }),
	"email off pattern":      mutated(func(o map[string]any) { customer(o)["email"] = "ada" }),
	"quantity below 1":       mutated(func(o map[string]any) { item(o)["quantity"] = 0 }),
	"quantity over 1000":     mutated(func(o map[string]any) { item(o)["quantity"] = 1001 }),
	"quantity not integral":  mutated(func(o map[string]any) { item(o)["quantity"] = 1.5 }),
	"negative price":         mutated(func(o map[string]any) { item(o)["priceCents"] = -1 }),
	"priority over 10":       mutated(func(o map[string]any) { o["priority"] = 11 }),
	"note not a string":      mutated(func(o map[string]any) { o["note"] = 5 }),
	"undeclared field":       mutated(func(o map[string]any) { o["coupon"] = "SAVE10" }),
	"undeclared item field":  mutated(func(o map[string]any) { item(o)["color"] = "red" }),
	"undeclared nested":      mutated(func(o map[string]any) { customer(o)["phone"] = "555" }),
	"item not an object":     mutated(func(o map[string]any) { o["items"] = []any{"ABC-0001"} }),
	"customer missing email": mutated(func(o map[string]any) { delete(customer(o), "email") }),
}

// Every validating pipeline accepts what schema 0.0 accepts and decodes it
// to the Order encoding/json decodes, and refuses what it refuses: the
// benchmarks compare pipelines that do the same job.
func TestPipelinesAgree(t *testing.T) {
	s := newSetup(t)
	valid := map[string][]byte{"without optional fields": mutated(func(o map[string]any) { delete(o, "note"); delete(o, "priority") })}
	for _, sz := range sizes {
		valid[sz.name] = order(sz.items)
	}
	for name, b := range valid {
		var want Order
		if err := json.Unmarshal(b, &want); err != nil {
			t.Fatal(err)
		}
		for _, p := range s.pipelines {
			got, err := p.decode(b)
			if err != nil {
				t.Errorf("%s refuses %s: %v", p.name, name, err)
			} else if !reflect.DeepEqual(got, want) {
				t.Errorf("%s decodes %s as %+v, want %+v", p.name, name, got, want)
			}
		}
	}
	for name, b := range invalid {
		for _, p := range s.pipelines {
			if _, err := p.decode(b); p.validates && err == nil {
				t.Errorf("%s accepts data with %s: %s", p.name, name, b)
			}
		}
	}
}

// Order is what thema lineage gen gotypes writes for schema 0.0.
func TestTypesCurrent(t *testing.T) {
	s := newSetup(t)
	fs, err := gocode.SchemaTypes(gocode.TypeConfig{PackageName: "bench"}).GenerateFS(s.sch)
	if err != nil {
		t.Fatal(err)
	}
	if err = fs.Verify(t.Context(), "."); err != nil {
		t.Errorf("types.go is not what gen gotypes writes; run go generate: %v", err)
	}
}

// BenchmarkDecode runs every pipeline on every payload from parallel
// goroutines. Vary -cpu to see how each scales.
func BenchmarkDecode(b *testing.B) {
	s := newSetup(b)
	for _, sz := range sizes {
		payload := order(sz.items)
		for _, p := range s.pipelines {
			b.Run(sz.name+"/"+p.name, func(b *testing.B) {
				b.SetBytes(int64(len(payload)))
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						if _, err := p.decode(payload); err != nil {
							b.Error(err)
							return
						}
					}
				})
			})
		}
	}
}

// BenchmarkContext is what a cue.Context of its own costs each CUE
// pipeline's call.
func BenchmarkContext(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cuecontext.New()
		}
	})
}
