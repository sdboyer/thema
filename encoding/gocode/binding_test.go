package gocode

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/load"
)

// The generated binding compiles against this tree and binds the lineage at
// the path it was generated for.
func TestGeneratedBindingBinds(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a Go module")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not found in PATH")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("../../internal/examples/ship.cue")
	if err != nil {
		t.Fatal(err)
	}
	// InstanceWithThema loads the package named by the module's last element.
	const cuemod, file = "example.com/thema_example", "ship.cue"
	ctx := cuecontext.New()
	inst, err := load.InstanceWithThema(load.AsModFS(fstest.MapFS{file: &fstest.MapFile{Data: src}}, cuemod), ".")
	if err != nil {
		t.Fatal(err)
	}
	lin, err := thema.BindLineage(ctx.BuildInstance(inst), cue.ParsePath("lin"), thema.NewRuntime(ctx))
	if err != nil {
		t.Fatal(err)
	}
	gen, err := GenerateLineageBinding(lin, &BindingConfig{
		PackageName: "ship",
		EmbedPath:   file,
		LoadDir:     ".",
		CueModName:  cuemod,
		CuePath:     cue.ParsePath("lin"),
	})
	if err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile("../../go.sum")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"go.mod":              []byte("module example.com/ship\n\ngo 1.27.0\n\nrequire github.com/sdboyer/thema v0.0.0\n\nreplace github.com/sdboyer/thema => " + repo + "\n"),
		"go.sum":              sum,
		file:                  src,
		"ship_binding_gen.go": gen,
		"ship_test.go": []byte(`package ship

import (
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/sdboyer/thema"
)

func TestLineage(t *testing.T) {
	lin, err := Lineage(thema.NewRuntime(cuecontext.New()))
	if err != nil {
		t.Fatal(err)
	}
	if lin.Name() != "Ship" {
		t.Errorf("bound %q, want Ship", lin.Name())
	}
}
`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOSUMDB=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the generated binding does not bind: %v\n%s\n%s", err, out, gen)
	}
}
