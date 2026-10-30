package capnp

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
)

// Every rule has a code and the text of its diagnostic, and no two rules share
// a code. The rules are read from compile.go, so a rule added without a code
// fails here.
func TestRuleCodes(t *testing.T) {
	src, err := os.ReadFile("compile.go")
	if err != nil {
		t.Fatal(err)
	}
	rules := regexp.MustCompile(`(?m)^\s*Rule[A-Za-z]+\s+Rule = "([a-z-]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(rules) == 0 {
		t.Fatal("no Rule constants found in compile.go")
	}
	seen := map[diag.Code]Rule{}
	for _, m := range rules {
		r := Rule(m[1])
		code, ok := Codes[r]
		if !ok {
			t.Errorf("rule %s has no code in Codes", r)
			continue
		}
		if _, ok := diag.Lookup(code); !ok {
			t.Errorf("rule %s maps to %s, which diag.Codes does not declare", r, code)
		}
		if prev, dup := seen[code]; dup {
			t.Errorf("rules %s and %s share code %s", prev, r, code)
		}
		seen[code] = r
		if _, ok := ruleText[r]; !ok {
			t.Errorf("rule %s has no headline or help in ruleText", r)
		}
	}
	if len(rules) != len(Codes) {
		t.Errorf("Codes has %d entries for %d rules", len(Codes), len(rules))
	}
}

// A label named as the field every struct reserves for undeclared data is a
// name-collision at the author's field, never renamed, and its help says
// why the name is taken.
func TestReservedExtras(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	lin := bindString(t, rt, `
name: "reserved"
schemas: "0": "0": schema: {a: string, extras: string}
lenses: {}
`)
	_, err := Compile(lin, WithoutLayout())
	if err == nil {
		t.Fatal("compiled a lineage with a label named extras")
	}
	ds := diag.Diagnostics(err)
	if len(ds) != 1 {
		t.Fatalf("%d diagnostics, want 1: %v", len(ds), err)
	}
	d := ds[0]
	if d.Code != diag.E0714 {
		t.Errorf("code %s, want E0714", d.Code)
	}
	if d.Primary == nil {
		t.Error("no primary position")
	}
	if len(d.Help) != 1 || !strings.Contains(d.Help[0], "reserved for undeclared data in the Cap'n Proto encoding") {
		t.Errorf("help %q does not say extras is reserved", d.Help)
	}
}
