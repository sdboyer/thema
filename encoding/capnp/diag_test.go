package capnp

import (
	"os"
	"regexp"
	"testing"

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
