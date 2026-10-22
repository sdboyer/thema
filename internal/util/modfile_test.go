package util_test

import (
	"io/fs"
	"testing"

	"cuelang.org/go/mod/modfile"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/util"
)

// The Go constants describing thema's CUE module must match the module.cue that
// is actually embedded.
func TestModuleConstantsMatchEmbeddedModule(t *testing.T) {
	b, err := fs.ReadFile(thema.CueJointFS, "cue.mod/module.cue")
	if err != nil {
		t.Fatal(err)
	}
	mf, err := modfile.Parse(b, "cue.mod/module.cue")
	if err != nil {
		t.Fatal(err)
	}
	if mf.Module != util.ThemaModulePath {
		t.Errorf("embedded module path is %q, util.ThemaModulePath is %q", mf.Module, util.ThemaModulePath)
	}
	if got := mf.Language.Version; got != util.LanguageVersion {
		t.Errorf("embedded language version is %q, util.LanguageVersion is %q", got, util.LanguageVersion)
	}
}

func TestModuleFileParses(t *testing.T) {
	mf, err := modfile.Parse([]byte(util.ModuleFile("example.com/m")), "module.cue")
	if err != nil {
		t.Fatal(err)
	}
	if dep, ok := mf.Deps[util.ThemaModulePath]; !ok || dep.Version != util.ThemaModuleVersion {
		t.Errorf("expected dependency on %s at %s, got %v", util.ThemaModulePath, util.ThemaModuleVersion, mf.Deps)
	}
}
