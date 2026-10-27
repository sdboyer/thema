package thema

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/load"
	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/util"
)

// TestKnownViolations runs the cases in testdata/known: lineages that thema
// currently mishandles, each paired with the behavior thema should exhibit.
//
// Each .txtar file has a comment header containing `#issue: N`, an in.cue file
// declaring the lineage (at path `lin` if present, else the root), and an expect
// file stating the correct behavior, one assertion per line:
//
//	bind ok|fail [text the error must contain]
//	validate <version> ok|fail <cue data>
//	translate <from> <to> ok|fail <cue data>
//	succ <version> ok|fail <cue data>   (ok: AsSuccessor succeeds, and the result validates)
//	examples <version> valid|invalid
//
// A case whose expectations aren't all met is skipped as TODO(#N). A case whose
// expectations are all met fails, so that a fix is noticed and the case moved
// into testdata/cases.
func TestKnownViolations(t *testing.T) {
	files, err := filepath.Glob("testdata/known/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no known violations")
	}
	for _, f := range files {
		t.Run(strings.TrimSuffix(filepath.Base(f), ".txtar"), func(t *testing.T) {
			ar, err := txtar.ParseFile(f)
			if err != nil {
				t.Fatal(err)
			}
			issue := regexp.MustCompile(`#issue:\s*(\d+)`).FindStringSubmatch(string(ar.Comment))
			if issue == nil {
				t.Fatalf("%s has no #issue line", f)
			}

			var src, expect string
			for _, af := range ar.Files {
				switch af.Name {
				case "in.cue":
					src = string(af.Data)
				case "expect":
					expect = string(af.Data)
				}
			}
			if src == "" || expect == "" {
				t.Fatalf("%s must have in.cue and expect files", f)
			}

			unmet := checkExpectations(t, src, expect)
			if len(unmet) > 0 {
				t.Skipf("TODO(#%s): %d expectation(s) not met:\n%s", issue[1], len(unmet), strings.Join(unmet, "\n"))
			}
			t.Errorf("all expectations met: #%s may be fixed for this case; move it into testdata/cases", issue[1])
		})
	}
}

// TestCases runs the cases in testdata/cases, in the same format as
// testdata/known (an #issue line is optional), requiring every expectation to be
// met. Fixed known violations move here.
func TestCases(t *testing.T) {
	files, err := filepath.Glob("testdata/cases/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		t.Run(strings.TrimSuffix(filepath.Base(f), ".txtar"), func(t *testing.T) {
			ar, err := txtar.ParseFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var src, expect string
			for _, af := range ar.Files {
				switch af.Name {
				case "in.cue":
					src = string(af.Data)
				case "expect":
					expect = string(af.Data)
				}
			}
			if src == "" || expect == "" {
				t.Fatalf("%s must have in.cue and expect files", f)
			}
			if unmet := checkExpectations(t, src, expect); len(unmet) > 0 {
				t.Errorf("%d expectation(s) not met:\n%s", len(unmet), strings.Join(unmet, "\n"))
			}
		})
	}
}

// checkExpectations runs each expectation in expect against the lineage in src,
// returning a description of each one that isn't met.
func checkExpectations(t *testing.T, src, expect string) (unmet []string) {
	t.Helper()
	if !strings.Contains(src, "package ") {
		src = "package m\n" + src
	}
	ctx := cuecontext.New()
	rt := NewRuntime(ctx)
	cfg := &load.Config{
		Dir: "/m",
		Overlay: map[string]load.Source{
			"/m/cue.mod/module.cue": load.FromString(util.ModuleFile("example.com/m")),
			"/m/in.cue":             load.FromString(src),
		},
	}
	if err := embedreg.Configure(cfg, CueJointFS); err != nil {
		t.Fatal(err)
	}
	bi := load.Instances([]string{"."}, cfg)[0]
	if bi.Err != nil {
		t.Fatalf("loading case failed: %v", bi.Err)
	}
	root := ctx.BuildInstance(bi)
	v := root
	if lv := root.LookupPath(cue.ParsePath("lin")); lv.Exists() {
		v = lv
	}

	var lin Lineage
	var binderr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				binderr = panicked{r}
			}
		}()
		lin, binderr = BindLineage(v, rt)
	}()

	miss := func(line, format string, args ...any) {
		unmet = append(unmet, fmt.Sprintf("  %s: %s", line, fmt.Sprintf(format, args...)))
	}
	outcome := func(err error) string {
		if p, ok := err.(panicked); ok {
			// Never satisfies an expectation: the correct behavior is never a panic.
			return fmt.Sprintf("panic (%v)", p.v)
		}
		if err != nil {
			// An expectation's text is matched against everything the
			// diagnostics say: headlines, notes, help and detail.
			if ds := diag.Diagnostics(err); len(ds) > 0 {
				var b strings.Builder
				diag.Render(&b, ds, diag.RenderOptions{})
				return "fail (" + strings.Join(strings.Fields(b.String()), " ") + ")"
			}
			return "fail (" + strings.ReplaceAll(cerrors.Details(err, nil), "\n", " ") + ")"
		}
		return "ok"
	}

	for _, line := range strings.Split(expect, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if fields[0] == "bind" {
			got := outcome(binderr)
			if want := strings.Join(fields[2:], " "); !strings.HasPrefix(got, fields[1]) || !strings.Contains(got, want) {
				miss(line, "bind was %s", got)
			}
			continue
		}
		if binderr != nil {
			miss(line, "not checked: bind failed (%s)", outcome(binderr))
			continue
		}

		err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = panicked{r}
				}
			}()
			switch fields[0] {
			case "validate":
				want, data := fields[2], strings.Join(fields[3:], " ")
				_, err := mustSchema(lin, fields[1]).Validate(ctx.CompileString(data))
				if got := outcome(err); !strings.HasPrefix(got, want) {
					miss(line, "validation was %s", got)
				}
			case "translate":
				want, data := fields[3], strings.Join(fields[4:], " ")
				inst, err := mustSchema(lin, fields[1]).Validate(ctx.CompileString(data))
				if err == nil {
					_, _, err = inst.Translate(parseSV(fields[2]))
				}
				if got := outcome(err); !strings.HasPrefix(got, want) {
					miss(line, "translation was %s", got)
				}
			case "succ":
				want, data := fields[2], strings.Join(fields[3:], " ")
				inst, err := mustSchema(lin, fields[1]).Validate(ctx.CompileString(data))
				if err == nil {
					var out *Instance
					if out, _, err = inst.AsSuccessor(); err == nil {
						_, err = out.Schema().Validate(out.Underlying())
					}
				}
				if got := outcome(err); !strings.HasPrefix(got, want) {
					miss(line, "successor was %s", got)
				}
			case "examples":
				sch := mustSchema(lin, fields[1])
				var bad []string
				for name, ex := range sch.Examples() {
					if _, err := sch.Validate(ex.Underlying()); err != nil {
						bad = append(bad, name)
					}
				}
				if got := map[bool]string{true: "valid", false: "invalid"}[len(bad) == 0]; got != fields[2] {
					miss(line, "examples were %s (invalid: %v)", got, bad)
				}
			default:
				return fmt.Errorf("unknown expectation")
			}
			return nil
		}()
		if err != nil {
			miss(line, "%v", err)
		}
	}
	return unmet
}

// panicked records a recovered panic as an outcome distinct from an error.
type panicked struct{ v any }

func (p panicked) Error() string { return fmt.Sprintf("panic: %v", p.v) }

func mustSchema(lin Lineage, v string) Schema {
	sch, err := lin.Schema(parseSV(v))
	if err != nil {
		panic(fmt.Sprintf("no schema %s: %v", v, err))
	}
	return sch
}

func parseSV(s string) SyntacticVersion {
	maj, min, ok := strings.Cut(s, ".")
	a, err1 := strconv.ParseUint(maj, 10, 64)
	b, err2 := strconv.ParseUint(min, 10, 64)
	if !ok || err1 != nil || err2 != nil {
		panic(fmt.Sprintf("invalid version %q", s))
	}
	return SV(uint(a), uint(b))
}
