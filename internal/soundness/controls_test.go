package soundness

import (
	"os"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/compile"
)

// controls are witnesses of filed issues, each a lineage that binds and that
// one of the oracles should flag. They check that the oracles and the
// generator can see a hole of each kind; they are not part of the corpora.
// A control that stops failing means its issue was fixed, or the spike lost
// the ability to see it.
var controls = []struct {
	name, issue, oracle, src string
	authoring                []compile.File
}{
	{name: "optional-ancestor-copy", issue: "#316", oracle: S1, src: `
import "github.com/sdboyer/thema"
lin: thema.Lineage & {
	name: "o"
	schemas: "0": "0": schema: {o?: {x: *1 | int}}
	schemas: "1": "0": schema: {o?: {x: *2 | int}}
	lenses: "0.0->1.0": {v0_0: _, v1_0: {if v0_0.o != _|_ {o: {x: v0_0.o.x}}}}
	lenses: "1.0->0.0": {v1_0: _, v0_0: {if v1_0.o != _|_ {o: {x: v1_0.o.x}}}}
}`},
	{name: "struct-default-changed-minor", issue: "#315", oracle: S2c, src: `
import "github.com/sdboyer/thema"
lin: thema.Lineage & {
	name: "c"
	schemas: "0": "0": schema: {a: *{x: 1} | {x: int}}
	schemas: "0": "1": schema: {a: *{x: 2} | {x: int}}
	lenses: "0.1->0.0": {v0_1: _, v0_0: v0_1}
}`},
	{name: "match-defaulted-enum", issue: "#318", oracle: S4, authoring: []compile.File{
		{Path: "history/0.0.thema", Text: []byte("Schema: {\n\tc: *\"a\" | \"b\" | \"q\"\n}\n")},
		{Path: "history/1.0.thema", Text: []byte(`Schema: {
	c: *"a" | "b" | "x"
}
ForwardLens: {
	v0_0: _
	v1_0: {
		c: _match(v0_0.c, {"q": "x"})
	}
}
BackwardLens: {
	v1_0: _
	v0_0: {
		c: _match(v1_0.c, {"x": "q"})
	}
}
`)}}},
}

func loadControls(t *testing.T) []entry {
	var out []entry
	for _, c := range controls {
		name := c.name + " (" + c.issue + ", expect " + c.oracle + ")"
		ctx := cuecontext.New()
		if c.authoring != nil {
			f, err := compileAuthoring(ctx, c.authoring, "", "")
			if err != nil {
				out = append(out, entry{name: name, skip: "does not compile: " + compileCodes(err)})
				continue
			}
			out = append(out, bindFile(name, ctx, f, ""))
			continue
		}
		v, err := loadPackage(ctx, map[string]string{"in.cue": c.src})
		if err != nil {
			out = append(out, entry{name: name, skip: "does not load: " + firstLine(err.Error())})
			continue
		}
		out = append(out, bind(name, v, cue.ParsePath("lin"), thema.NewRuntime(ctx), ""))
	}
	return out
}

// TestControls checks that each control binds and that its oracle flags it.
func TestControls(t *testing.T) {
	if os.Getenv("THEMA_SOUNDNESS") == "" {
		t.Skip("set THEMA_SOUNDNESS=1 to run the soundness spike")
	}
	for i, e := range loadControls(t) {
		c := controls[i]
		t.Run(c.name, func(t *testing.T) {
			if e.lin == nil {
				t.Fatalf("%s: %s", c.issue, e.skip)
			}
			_, fails := Run("controls", e.name, e.lin, DefaultLimits)
			for _, f := range fails {
				if f.Oracle == c.oracle {
					t.Logf("%s flagged by %s: %s->%s %s\n  instance: %s", c.issue, f.Oracle, f.From, f.To, f.Detail, f.Instance)
					return
				}
			}
			t.Errorf("%s: %s did not flag it; failures: %v", c.issue, c.oracle, fails)
		})
	}
}
