package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sdboyer/thema/diag"
)

// Strings the gen tests repeat.
const (
	jsonschemaCmd = "jsonschema"
	genCmd        = "gen"
	gobindingsCmd = "gobindings"
	goKeyword     = "func"
)

// An -f that names no format openapi and jsonschema write is E0620, reported
// as --diag-format asks, before anything is generated.
func TestGenFormat(t *testing.T) {
	dir := writeTipDir(t, "Schema: {\n\tname: string\n}\n")
	for _, sub := range []string{openapiCmd, jsonschemaCmd} {
		t.Run(sub, func(t *testing.T) {
			if got := initCodes(t, t.TempDir(), lineageCmd, genCmd, sub, "-l", dir, "-f", "bogus"); !slices.Equal(got, []diag.Code{diag.E0620}) {
				t.Errorf("codes %v, want [E0620]", got)
			}
		})
	}
}

// A --pkgname that is not a Go package name is E0620 for gobindings, as it
// is for gotypes, before anything is generated.
func TestGenGoBindingsPkgname(t *testing.T) {
	dir := writeTipDir(t, "Schema: {\n\tname: string\n}\n")
	for _, name := range []string{"9bad", goKeyword, "_"} {
		t.Run(name, func(t *testing.T) {
			if got := initCodes(t, t.TempDir(), lineageCmd, genCmd, gobindingsCmd, "-l", dir, "--pkgname", name, "--stdout"); !slices.Equal(got, []diag.Code{diag.E0620}) {
				t.Errorf("codes %v, want [E0620]", got)
			}
		})
	}
}

// A lineage whose name, in lower case, is not a Go package name cannot give
// gobindings, or gotypes with -v, its default package name: that is E0620,
// asking for --pkgname, before anything is generated.
func TestGenDerivedPkgname(t *testing.T) {
	for _, name := range []string{goKeyword, "Type"} {
		dir := filepath.Join(filepath.Dir(writeTipDir(t, "Schema: {\n\tname: string\n}\n")), name)
		if err := os.Rename(filepath.Join(filepath.Dir(dir), shipName), dir); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{gobindingsCmd, stdoutFlg}, {"gotypes", "-v", v00}} {
			t.Run(name+" "+args[0], func(t *testing.T) {
				line := append([]string{lineageCmd, genCmd, "-l", dir}, args...)
				if got := initCodes(t, t.TempDir(), line...); !slices.Equal(got, []diag.Code{diag.E0620}) {
					t.Errorf("codes %v, want [E0620]", got)
				}
			})
		}
	}
}
