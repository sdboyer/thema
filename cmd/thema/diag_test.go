package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sdboyer/thema/authoring"
)

// A diagnostic about a lineage in the authoring form, run from outside its
// directory, shows the excerpt from the version file and names the lens as
// the author wrote it. Positions in authoring files are relative to the
// working directory, naming the lineage's directory as -l gave it, when the
// lineage lies beneath it; otherwise they are relative to the lineage's
// parent, naming it by its base name.
func TestAuthoringDiagnosticExcerpt(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "fleet", "ship")
	if err := os.MkdirAll(filepath.Join(dir, "history"), 0o750); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		historyV00: "Schema: {\n\ta: int32\n}\n",
		"history/1.0.thema": `Schema: {
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
		authoring.TipFile: "Schema: {\n\ta: int32 & >=0\n}\n",
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

	cases := []struct {
		name, wd, lineage, pos string
	}{
		{"beneath the working directory", parent, filepath.Join("fleet", "ship"), "fleet/ship/history/1.0.thema:8:6"},
		{"elsewhere", t.TempDir(), dir, "ship/history/1.0.thema:8:6"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "data", "validate", "-l", tc.lineage, data) //nolint:gosec // re-executes this test binary
			cmd.Dir = tc.wd
			cmd.Env = append(os.Environ(), runMainEnv+"=1")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err == nil {
				t.Fatal("want the lineage to fail to bind: the forward lens computes a value the target field may reject")
			}
			out := stderr.String()
			for _, want := range []string{"the ForwardLens of v1.0", "--> " + tc.pos + "\n", "a: v0_0.a"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "0.0->1.0") {
				t.Errorf("output names the lens by its normal-form edge:\n%s", out)
			}
		})
	}
}
