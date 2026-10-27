//go:build !js

package playground

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sdboyer/thema/internal/diagtest"
	"github.com/sdboyer/thema/internal/tsapi"
)

// The frontend's types.ts, in playground/ui, is what internal/tsapi
// generates from the Go types. Regenerate with THEMA_UPDATE_GOLDEN=1 go test
// ./playground.
func TestTypesTSUpToDate(t *testing.T) {
	got, err := tsapi.Generate()
	if err != nil {
		t.Fatal(err)
	}
	diagtest.CompareFile(t, filepath.Join("ui", "src", "api", "types.ts"), got)
}

// The playground generates no Go: Go bindings need an embed directive and a
// filesystem, neither of which a browser has. Importing the generator would
// also grow the wasm build for nothing.
func TestNoGoCodegen(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "github.com/sdboyer/thema/encoding/gocode" {
			t.Fatal("the playground imports encoding/gocode")
		}
	}
}
