package lensir_test

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/sdboyer/thema/internal/lensir"
)

// The lensir package must not depend on CUE, directly or transitively: the IR
// is consumed where CUE is not linked (encoding/capnp's engine), and the
// program it carries is output, never consulted to decide what CUE means.
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
		t.Errorf("package lensir must not depend on CUE, but depends on:\n\t%s", strings.Join(cueDeps, "\n\t"))
	}
}

func TestPathString(t *testing.T) {
	for _, c := range []struct {
		path lensir.Path
		want string
	}{
		{lensir.Path{lensir.Field("a"), lensir.Field("b2")}, "a.b2"},
		{lensir.Path{lensir.Field("a"), lensir.Index(0), lensir.Any()}, "a[0][_]"},
		{lensir.Path{lensir.Field("a-b"), lensir.Field("2a")}, `"a-b"."2a"`},
		// A data field whose name starts with _ is quoted, as CUE needs it to
		// be; unquoted, _id would be a hidden field.
		{lensir.Path{lensir.Field("_id"), lensir.Field("x_")}, `"_id".x_`},
	} {
		if got := c.path.String(); got != c.want {
			t.Errorf("%#v: got %s, want %s", c.path, got, c.want)
		}
	}
}
