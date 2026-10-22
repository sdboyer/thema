package thema

import (
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
)

func TestRequiredSchemaFields(t *testing.T) {
	ctx := cuecontext.New()
	rt := NewRuntime(ctx)
	lin, err := bindString(t, rt, `lin: thema.Lineage & {
	name: "req"
	schemas: [{
		version: [0, 0]
		schema: {a!: int, b?: string, n?: {c!: string}}
	}]
}`)
	if err != nil {
		t.Fatalf("lineage with required schema fields failed to bind: %v", err)
	}
	sch := lin.First()

	for data, wantErr := range map[string]string{
		`{a: 1}`:              "",
		`{a: 1, n: {c: "x"}}`: "",
		`{b: "x"}`:            "a",
		`{a: 1, n: {}}`:       "c",
	} {
		_, err := sch.Validate(ctx.CompileString(data))
		switch {
		case wantErr == "" && err != nil:
			t.Errorf("%s: unexpected error: %v", data, err)
		case wantErr != "" && err == nil:
			t.Errorf("%s: missing required field %s was accepted", data, wantErr)
		case wantErr != "" && !strings.Contains(err.Error(), wantErr):
			t.Errorf("%s: error does not name the missing field %s: %q", data, wantErr, err.Error())
		}
	}
}
