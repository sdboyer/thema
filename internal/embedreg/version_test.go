package embedreg

import (
	"context"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue/load"
	"cuelang.org/go/mod/module"

	"github.com/sdboyer/thema/internal/util"
)

// A requirement with no version is refused, not served: semver orders an
// invalid version before every valid one. CUE's module.Version admits no other
// version that isn't one.
func TestNotAVersion(t *testing.T) {
	cfg := &load.Config{Env: []string{"CUE_REGISTRY=none"}}
	themaFS := fstest.MapFS{"cue.mod/module.cue": {Data: []byte(util.ModuleFile(util.ThemaModulePath))}}
	if err := Configure(cfg, themaFS); err != nil {
		t.Fatal(err)
	}
	m, err := module.NewVersion(util.ThemaModulePath, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Registry.Fetch(context.Background(), m); err == nil {
		t.Error("a requirement with no version was served")
	}
}
