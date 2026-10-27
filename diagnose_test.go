package thema

import (
	"os"
	"regexp"
	"testing"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/lenscheck"
	"github.com/sdboyer/thema/internal/rulecodes"
)

// Every rule of the compatibility and totality checkers has a code, and no
// two rules share one. The rules are read from the checkers' sources, so a
// rule added to either without a code fails here.

func TestCompatCodes(t *testing.T) {
	rules := ruleConstants(t, "internal/compat/compat.go")
	seen := map[diag.Code]compat.Rule{}
	for _, r := range rules {
		code, ok := rulecodes.Compat[compat.Rule(r)]
		if !ok {
			t.Errorf("compat rule %s has no code in rulecodes.Compat", r)
			continue
		}
		if _, ok := diag.Lookup(code); !ok {
			t.Errorf("compat rule %s maps to %s, which diag.Codes does not declare", r, code)
		}
		if prev, dup := seen[code]; dup {
			t.Errorf("compat rules %s and %s share code %s", prev, r, code)
		}
		seen[code] = compat.Rule(r)
	}
	if len(rules) != len(rulecodes.Compat) {
		t.Errorf("rulecodes.Compat has %d entries for %d rules", len(rulecodes.Compat), len(rules))
	}
}

func TestLenscheckCodes(t *testing.T) {
	rules := ruleConstants(t, "internal/lenscheck/lenscheck.go")
	seen := map[diag.Code]lenscheck.Rule{}
	for _, r := range rules {
		code, ok := rulecodes.Lenscheck[lenscheck.Rule(r)]
		if !ok {
			t.Errorf("lenscheck rule %s has no code in rulecodes.Lenscheck", r)
			continue
		}
		if _, ok := diag.Lookup(code); !ok {
			t.Errorf("lenscheck rule %s maps to %s, which diag.Codes does not declare", r, code)
		}
		// target-undeclared shares E0305 with checkLensResults on purpose: one
		// rule, two enforcement sites (the lens-result-fields seam).
		if prev, dup := seen[code]; dup && code != diag.E0305 {
			t.Errorf("lenscheck rules %s and %s share code %s", prev, r, code)
		}
		seen[code] = lenscheck.Rule(r)
	}
	if len(rules) != len(rulecodes.Lenscheck) {
		t.Errorf("rulecodes.Lenscheck has %d entries for %d rules", len(rulecodes.Lenscheck), len(rules))
	}
}

// TestRefCodes: every reference rule (references.go) has its own code.
func TestRefCodes(t *testing.T) {
	src, err := os.ReadFile("references.go")
	if err != nil {
		t.Fatal(err)
	}
	rules := regexp.MustCompile(`(?m)^\s*ref[A-Za-z]+\s+refRule = "([a-z-]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(rules) == 0 {
		t.Fatal("no refRule constants found in references.go")
	}
	seen := map[diag.Code]refRule{}
	for _, m := range rules {
		r := refRule(m[1])
		code, ok := refCodes[r]
		if !ok {
			t.Errorf("reference rule %s has no code in refCodes", r)
			continue
		}
		if _, ok := diag.Lookup(code); !ok {
			t.Errorf("reference rule %s maps to %s, which diag.Codes does not declare", r, code)
		}
		if prev, dup := seen[code]; dup {
			t.Errorf("reference rules %s and %s share code %s", prev, r, code)
		}
		seen[code] = r
	}
	if len(rules) != len(refCodes) {
		t.Errorf("refCodes has %d entries for %d rules", len(refCodes), len(rules))
	}
}

var ruleConst = regexp.MustCompile(`(?m)^\s*Rule[A-Za-z]+ Rule = "([a-z-]+)"`)

// ruleConstants reads the string values of the Rule constants declared in a
// checker's source.
func ruleConstants(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range ruleConst.FindAllStringSubmatch(string(src), -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatalf("no Rule constants found in %s", path)
	}
	return out
}
