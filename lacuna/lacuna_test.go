package lacuna_test

import (
	"bufio"
	"bytes"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/lacuna"
)

// The Go lacuna type constants must match the types defined in thema's CUE.
//
// CUE is imported here, in the external test package, so that the lacuna package
// itself stays free of it; TestNoCUEDependency checks that it does.
func TestLacunaTypesMatchCUE(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	iter, err := rt.Underlying().LookupPath(cue.ParsePath("LacunaTypes")).Fields()
	if err != nil {
		t.Fatal(err)
	}
	var cueTypes []string
	for iter.Next() {
		s, err := iter.Value().String()
		if err != nil || s != iter.Selector().String() {
			t.Errorf("LacunaTypes.%s must be the string %q, got %v", iter.Selector(), iter.Selector(), iter.Value())
		}
		cueTypes = append(cueTypes, s)
	}
	var goTypes []string
	for lt := range lacuna.Types {
		goTypes = append(goTypes, string(lt))
		if !lt.Known() {
			t.Errorf("%s is listed as a lacuna type but Known() reports it is not", lt)
		}
	}
	sort.Strings(cueTypes)
	sort.Strings(goTypes)
	if !reflect.DeepEqual(cueTypes, goTypes) {
		t.Errorf("CUE lacuna types %v differ from Go constants %v", cueTypes, goTypes)
	}
	if lacuna.Type("Bogus").Known() {
		t.Error("Known() accepts an undefined lacuna type")
	}
}

// SyntacticVersion converts to and from thema's, and formats the same way.
func TestSyntacticVersion(t *testing.T) {
	sv := lacuna.SyntacticVersion(thema.SV(3, 12))
	if got, want := sv.String(), thema.SV(3, 12).String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if thema.SyntacticVersion(sv) != thema.SV(3, 12) {
		t.Errorf("round trip through thema.SyntacticVersion changed %v", sv)
	}
}

// The lacuna package must not depend on CUE, directly or transitively. Engines
// that translate instances without CUE emit its types.
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
		t.Errorf("package lacuna must not depend on CUE, but depends on:\n\t%s", strings.Join(cueDeps, "\n\t"))
	}
}
