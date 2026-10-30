package capnp

import (
	"encoding/json"
	"flag"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/encoding/capnp/compiled"
	"github.com/sdboyer/thema/internal/lensir"
	"github.com/sdboyer/thema/lacuna"
)

// describeFate renders what a compilation carries for the fields no program
// writes: each version's structs, with the labels it declares there and
// whether each is open, then each edge's fate as read from its lens.
func describeFate(c *Compiled) string {
	var b strings.Builder
	for _, v := range c.Versions {
		fmt.Fprintf(&b, "version %d.%d\n", v.Major, v.Minor)
		for _, s := range v.Structs {
			open := "closed"
			if s.Open {
				open = "open"
			}
			fmt.Fprintf(&b, "  struct %s %s %s: %s\n", pathText(s.Path), c.Node(s.Node).Name, open, strings.Join(s.Labels, ", "))
		}
	}
	for _, e := range c.Edges {
		if e.Fate == nil {
			continue
		}
		fmt.Fprintf(&b, "edge %d.%d -> %d.%d\n", e.From[0], e.From[1], e.To[0], e.To[1])
		for _, r := range e.Fate.Reads {
			fmt.Fprintf(&b, "  read %s\n", pathText(r))
		}
		for _, r := range e.Fate.Readers {
			fmt.Fprintf(&b, "  reader %s <- %s\n", pathText(r.Result), pathText(r.Input))
		}
		for _, r := range e.Fate.Copies {
			fmt.Fprintf(&b, "  copy %s <- %s\n", pathText(r.Result), pathText(r.Input))
		}
		for _, n := range e.Fate.Named {
			fmt.Fprintf(&b, "  named %s (lacuna %d, field %d)\n", pathText(n.Path), n.Lacuna, n.Field)
		}
	}
	return b.String()
}

func pathText(p lensir.Path) string {
	if len(p) == 0 {
		return "(root)"
	}
	return p.String()
}

// describeTranslations renders Go's translation of each example of each
// version one step to each adjacent version: the instance, the result and
// the step's lacunas. Go's Translate defines what an engine running the
// artifact must produce (docs/translation.md, G4), so these are the cases an
// engine is held to. Each lacuna translation raised by itself is checked
// against the artifact's message templates, and the templates it matches
// are recorded in matched.
func describeTranslations(t testing.TB, lin thema.Lineage, matched map[string]bool) string {
	t.Helper()
	var b strings.Builder
	for _, sch := range lin.All() {
		ex := sch.Examples()
		names := make([]string, 0, len(ex))
		for name := range ex {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			inst := ex[name]
			for _, to := range []thema.Schema{sch.Predecessor(), sch.Successor()} {
				if to == nil {
					continue
				}
				fmt.Fprintf(&b, "%s %s -> %s\n", sch.Version(), name, to.Version())
				fmt.Fprintf(&b, "  in  %s\n", mustJSON(t, inst.Underlying()))
				out, lacs, err := inst.Translate(to.Version())
				if err != nil {
					fmt.Fprintf(&b, "  error %v\n", err)
					continue
				}
				fmt.Fprintf(&b, "  out %s\n", mustJSON(t, out.Underlying()))
				for _, step := range lacs {
					for _, l := range step.Lacunas {
						fmt.Fprintf(&b, "  lacuna %s\n", mustJSON(t, l))
						checkAside(t, step, l, matched)
					}
				}
			}
		}
	}
	return b.String()
}

func mustJSON(t testing.TB, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// asidePattern recognizes the messages of the lacunas translation raises by
// itself, as opposed to those a lens lists.
var asidePattern = regexp.MustCompile(` (set aside|not carried): `)

// checkAside expects a lacuna translation raised by itself to be one of the
// artifact's message templates, filled in with the step's versions and the
// lacuna's fields; matched records the template.
func checkAside(t testing.TB, step lacuna.TranslationStep, l lacuna.Lacuna, matched map[string]bool) {
	t.Helper()
	if !asidePattern.MatchString(l.Message) {
		return
	}
	paths := make([]string, len(l.SourceFields))
	for i, f := range l.SourceFields {
		paths[i] = f.Path
	}
	for _, m := range asideMessages {
		if m.Type != l.Type || (m.Form == compiled.SeveralFields) != (len(paths) > 1) {
			continue
		}
		re := regexp.QuoteMeta(m.Text)
		for ph, val := range map[string]string{
			"{fields}": regexp.QuoteMeta(strings.Join(paths, ", ")),
			"{field}":  regexp.QuoteMeta(paths[0]),
			"{from}":   regexp.QuoteMeta(fmt.Sprintf("%d.%d", step.From[0], step.From[1])),
			"{to}":     regexp.QuoteMeta(fmt.Sprintf("%d.%d", step.To[0], step.To[1])),
			"{struct}": ".+",
			"{target}": ".+",
		} {
			re = strings.ReplaceAll(re, regexp.QuoteMeta(ph), val)
		}
		if regexp.MustCompile("^" + re + "$").MatchString(l.Message) {
			matched[m.Cause.String()+" "+m.Form.String()] = true
			return
		}
	}
	t.Errorf("lacuna %q matches no message template of the artifact", l.Message)
}

// checkAsideCoverage expects every message template to have matched a lacuna
// Go's translation raised in the goldens, so that each is held to Go's text.
// A run of some of the goldens (-run) proves nothing either way.
func checkAsideCoverage(t *testing.T, matched map[string]bool) {
	if f := flag.Lookup("test.run"); f != nil && f.Value.String() != "" {
		return
	}
	for _, m := range asideMessages {
		if m.Cause == compiled.AsideLossy && m.Form == compiled.MovedField {
			// Unreachable through a lens that binds: an extra reaches a field
			// at another path only beneath a struct the lens copies whole from
			// an open one, and lenscheck refuses that copy as not total where
			// the target declares a label the source does not, unless the
			// label accepts every value (_), which nothing fails.
			continue
		}
		if !matched[m.Cause.String()+" "+m.Form.String()] {
			t.Errorf("message template %s %s matches no lacuna in the goldens: %s", m.Cause, m.Form, m.Text)
		}
	}
}
