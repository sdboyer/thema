package thema

import (
	"encoding/json"
	"reflect"
	"testing"

	"cuelang.org/go/cue/cuecontext"
)

const hydrateLineage = `
name: "ship"
schemas: {
	"0": "0": {
		schema: {
			title: string
			crew:  int | *1
			hull: {
				material: string | *"steel"
			}
		}
	}
}
`

// noDefaults is an instance that takes none of its schema's defaults:
// hydrating and dehydrating it both leave it as it is.
const noDefaults = `{"title": "Argo", "crew": 3, "hull": {"material": "oak"}}`

// Hydrate fills an instance with its schema's defaults, and Dehydrate
// removes the values its defaults imply; each walks the schema itself, not
// the schema's entry in the lineage.
func TestHydrateDehydrate(t *testing.T) {
	ctx := cuecontext.New()
	rt := NewRuntime(ctx)
	lin, err := BindLineage(ctx.CompileString(hydrateLineage), rt)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		in   string
		f    func(*Instance) *Instance
		want string
	}{
		"hydrate fills defaults": {
			in:   `{"title": "Argo", "hull": {}}`,
			f:    (*Instance).Hydrate,
			want: `{"title": "Argo", "crew": 1, "hull": {"material": "steel"}}`,
		},
		"hydrate keeps given values": {
			in:   noDefaults,
			f:    (*Instance).Hydrate,
			want: noDefaults,
		},
		"dehydrate removes defaulted values": {
			in:   `{"title": "Argo", "crew": 1, "hull": {"material": "steel"}}`,
			f:    (*Instance).Dehydrate,
			want: `{"title": "Argo", "hull": {}}`,
		},
		"dehydrate keeps other values": {
			in:   noDefaults,
			f:    (*Instance).Dehydrate,
			want: noDefaults,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			inst, err := lin.First().Validate(ctx.CompileString(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			b, err := tt.f(inst).Underlying().MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %s, want %s", b, tt.want)
			}
		})
	}
}
