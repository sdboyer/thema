package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A diagnostic about a lineage in the authoring form, run from outside its
// directory, shows the excerpt from the version file and names the lens as
// the author wrote it. Positions in authoring files are relative to the
// lineage's directory, not to the working directory.
func TestAuthoringDiagnosticExcerpt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ship")
	if err := os.MkdirAll(filepath.Join(dir, "history"), 0o750); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"history/0.0.thema": "Schema: {\n\ta: int32\n}\n",
		"1.0.thema": `Schema: {
	a: int32 & >=0
}

ForwardLens: {
	v0_0: _
	v1_0: {
		a: v0_0.a * 2
	}
}

BackwardLens: {
	v1_0: _
	v0_0: {
		a: v1_0.a
	}
}
`,
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data := filepath.Join(t.TempDir(), "data.json")
	if err := os.WriteFile(data, []byte(`{"a": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "data", "validate", "-l", dir, data) //nolint:gosec // re-executes this test binary
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), runMainEnv+"=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("want the lineage to fail to bind: the forward lens computes a value the target field may reject")
	}
	out := stderr.String()
	for _, want := range []string{"the ForwardLens in 1.0.thema", "a: v0_0.a"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "0.0->1.0") {
		t.Errorf("output names the lens by its normal-form edge:\n%s", out)
	}
}
