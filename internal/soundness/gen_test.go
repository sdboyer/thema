package soundness

import (
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema/internal/cuetil"
)

// TestGenerateCovers checks that the generator produces each form of instance
// the oracles rely on, and that what it produces for a schema without
// cross-field constraints validates.
func TestGenerateCovers(t *testing.T) {
	ctx := cuecontext.New()
	v := ctx.CompileString(`
a!: int & >=0 & <10
b?: string
c: *"x" | "y"
d: [...int]
m: [string]: int
o?: {p: int, q: *1 | int, ...}
dj: {k: "a", n: int} | {k: "b", s: string}
n: null | bool
f: float & >1.5
`)
	lim := DefaultLimits
	lim.Instances = 200
	cands, st := NewGenerator(ctx, lim).Generate(v)
	closed := cuetil.CloseRecursive(ctx, v)
	var texts []string
	for _, c := range cands {
		if err := closed.Unify(ctx.BuildExpr(c.Expr)).Validate(); err != nil {
			t.Errorf("invalid candidate %s: %v", compact(c.Expr), err)
		}
		texts = append(texts, compact(c.Expr))
	}
	if len(st.Unproducible) > 0 {
		t.Errorf("unproducible: %v", st.Unproducible)
	}
	want := map[string]func(string) bool{
		"b omitted":               func(s string) bool { return !strings.Contains(s, `"b":`) },
		"c omitted":               func(s string) bool { return !strings.Contains(s, `"c":`) },
		"c the other alternative": func(s string) bool { return strings.Contains(s, `"c": "y"`) },
		"a at its upper bound":    func(s string) bool { return strings.Contains(s, `"a": 9`) },
		"f just past its bound":   func(s string) bool { return strings.Contains(s, `"f": 2.0`) },
		"d empty":                 func(s string) bool { return strings.Contains(s, `"d": []`) },
		"d of one":                func(s string) bool { return strings.Contains(s, `"d": [0]`) },
		"d of three":              func(s string) bool { return strings.Contains(s, `"d": [0, 0, 0]`) },
		"a key under m": func(s string) bool {
			return strings.Contains(s, `"m": "zz_extra": 0`) || strings.Contains(s, `"m": { "zz_extra": 0`)
		},
		"o present without q":      func(s string) bool { return strings.Contains(s, `"o": "p": 0`) },
		"an undeclared field in o": func(s string) bool { return strings.Contains(s, `"zz_extra": "extra"`) },
		"dj's second alternative":  func(s string) bool { return strings.Contains(s, `"k": "b"`) },
	}
	for name, ok := range want {
		found := false
		for _, s := range texts {
			if ok(s) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no candidate with %s among:\n%s", name, strings.Join(texts, "\n"))
		}
	}
}
