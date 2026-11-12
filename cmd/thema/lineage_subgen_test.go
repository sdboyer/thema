package main

import (
	"slices"
	"testing"

	"github.com/sdboyer/thema/diag"
)

const jsonschemaCmd = "jsonschema"

// An -f that names no format openapi and jsonschema write is E0620, reported
// as --diag-format asks, before anything is generated.
func TestGenFormat(t *testing.T) {
	dir := writeTipDir(t, "Schema: {\n\tname: string\n}\n")
	for _, sub := range []string{openapiCmd, jsonschemaCmd} {
		t.Run(sub, func(t *testing.T) {
			if got := initCodes(t, t.TempDir(), lineageCmd, "gen", sub, "-l", dir, "-f", "bogus"); !slices.Equal(got, []diag.Code{diag.E0620}) {
				t.Errorf("codes %v, want [E0620]", got)
			}
		})
	}
}
