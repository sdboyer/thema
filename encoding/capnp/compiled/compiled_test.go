package compiled_test

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// The compiled package must not depend on CUE, directly or transitively: it is
// what an engine that never links CUE loads.
func TestNoCUEDependency(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not found in PATH")
	}
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list -deps: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("go list -deps: %v", err)
	}
	var cueDeps []string
	for sc := bufio.NewScanner(bytes.NewReader(out)); sc.Scan(); {
		if dep := sc.Text(); strings.HasPrefix(dep, "cuelang.org/") {
			cueDeps = append(cueDeps, dep)
		}
	}
	if len(cueDeps) > 0 {
		t.Errorf("package compiled must not depend on CUE, but depends on:\n\t%s", strings.Join(cueDeps, "\n\t"))
	}
}
