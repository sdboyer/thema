// Package report compares a corpus of cases run by many suites at two
// revisions, and renders the result as a matrix (HTML) or a summary for a pull
// request (Markdown).
//
// A corpus is a directory of txtar archives, one case per file, and a
// suites.json naming the suites that run them; testdata/refs is the first.
// A cell is one case run by one suite. Its status is computed from the archive
// alone, from the case's #expect, its #known/<suite> tags and the suite's
// golden verdict in out/<suite>/bind:
//
//	not-applicable  the archive has no out/<suite>/ section
//	pass            the golden verdict is #expect, and there is no #known/<suite>
//	known           the golden verdict is not #expect, and #known/<suite> says why
//	regressed       the golden verdict is not #expect, and there is no #known/<suite>
//	xpass           the golden verdict is #expect, but #known/<suite> is still there
//
// A committed corpus holds only pass, known and not-applicable cells.
package report

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema/internal/diagtest"
)

// Status is the status of a cell.
type Status string

// The statuses a cell can have in a corpus, and Absent, which only a report
// uses: the case or the suite does not exist in that revision.
const (
	NotApplicable Status = "not-applicable"
	Pass          Status = "pass"
	Known         Status = "known"
	Regressed     Status = "regressed"
	XPass         Status = "xpass"
	Absent        Status = "absent"
)

// Statuses lists every status a corpus can give a cell, in the order a report
// shows them.
var Statuses = []Status{Regressed, XPass, Known, Pass, NotApplicable}

// SuitesFile is the name of a corpus's suite registry, beside its archives.
const SuitesFile = "suites.json"

// Suite is one way the corpus's cases are run, as suites.json declares it.
type Suite struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// KnownTag is a #known/<suite> directive: the suite currently disagrees with
// #expect, for the reason given, tracked by the GitHub issue.
type KnownTag struct {
	Issue  int    `json:"issue"`
	Reason string `json:"reason"`
}

func (k KnownTag) String() string {
	return fmt.Sprintf("#%d %s", k.Issue, k.Reason)
}

// Case is one archive of a corpus.
type Case struct {
	Name        string
	Description string
	// Expect is #expect as written, and expect its normal form: "ok", or the
	// sorted, de-duplicated codes joined by single spaces.
	Expect string
	expect string
	Spec   string
	At     string
	Known  map[string]KnownTag
	// Goldens holds out/<suite>/bind for each suite that ran the case, and
	// verdicts the normal form of its first line.
	Goldens  map[string]string
	verdicts map[string]string
}

// Status computes the status of the cell for suite.
func (c *Case) Status(suite string) Status {
	verdict, ran := c.verdicts[suite]
	if !ran {
		return NotApplicable
	}
	_, known := c.Known[suite]
	switch agrees := verdict == c.expect; {
	case agrees && !known:
		return Pass
	case !agrees && known:
		return Known
	case !agrees:
		return Regressed
	default:
		return XPass
	}
}

// Corpus is a corpus as it is in one revision.
type Corpus struct {
	Suites []Suite
	// Cases are sorted by name.
	Cases []*Case
	// Problems are the ways the corpus breaks its format, each naming the file
	// it is in. A cell's status is still computed where it can be.
	Problems []string
}

// Suite returns the suite named name, and whether the corpus has it.
func (c *Corpus) Suite(name string) (Suite, bool) {
	for _, s := range c.Suites {
		if s.Name == name {
			return s, true
		}
	}
	return Suite{}, false
}

// Case returns the case named name, or nil.
func (c *Corpus) Case(name string) *Case {
	for _, k := range c.Cases {
		if k.Name == name {
			return k
		}
	}
	return nil
}

var (
	codePattern = regexp.MustCompile(`^E\d{4}$`)
	specPattern = regexp.MustCompile(`^[^\s:,]+:\d+(-\d+)?$`)
	knownValue  = regexp.MustCompile(`^#(\d+)\s+(\S.*)$`)
)

// Parse builds a corpus from the contents of its files, keyed by base name:
// suites.json and every .txtar. Other files are ignored.
func Parse(files map[string][]byte) *Corpus {
	c := &Corpus{}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)

	data, hasSuites := files[SuitesFile]
	if hasSuites {
		c.parseSuites(data)
	}
	for _, name := range names {
		if path.Ext(name) != ".txtar" {
			continue
		}
		if !hasSuites {
			c.problemf(SuitesFile, "missing, but the corpus has archives")
			hasSuites = true
		}
		c.Cases = append(c.Cases, c.parseCase(name, files[name]))
	}
	slices.Sort(c.Problems)
	return c
}

func (c *Corpus) problemf(file, format string, args ...any) {
	c.Problems = append(c.Problems, file+": "+fmt.Sprintf(format, args...))
}

func (c *Corpus) parseSuites(data []byte) {
	var reg struct {
		Suites []Suite `json:"suites"`
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		c.problemf(SuitesFile, "%v", err)
		return
	}
	seen := map[string]bool{}
	for _, s := range reg.Suites {
		switch {
		case s.Name == "":
			c.problemf(SuitesFile, "a suite has no name")
			continue
		case seen[s.Name]:
			c.problemf(SuitesFile, "suite %s is declared twice", s.Name)
			continue
		case strings.TrimSpace(s.Description) == "":
			c.problemf(SuitesFile, "suite %s has no description", s.Name)
		}
		seen[s.Name] = true
		c.Suites = append(c.Suites, s)
	}
}

func (c *Corpus) parseCase(file string, data []byte) *Case {
	k := &Case{
		Name:     strings.TrimSuffix(file, ".txtar"),
		Known:    map[string]KnownTag{},
		Goldens:  map[string]string{},
		verdicts: map[string]string{},
	}
	ar := &diagtest.Archive{Path: file, Ar: txtar.Parse(data)}
	dirs, _ := ar.Directives()
	for _, key := range []string{"description", "expect", "spec", "at"} {
		if dirs[key] == "" {
			c.problemf(file, "no #%s", key)
		}
	}
	k.Description, k.Expect, k.Spec, k.At = dirs["description"], dirs["expect"], dirs["spec"], dirs["at"]

	if k.Expect != "" {
		norm, err := normalVerdict(k.Expect)
		if err != nil {
			c.problemf(file, "#expect: %v", err)
		}
		k.expect = norm
	}
	for _, s := range strings.Split(k.Spec, ",") {
		if s = strings.TrimSpace(s); k.Spec != "" && !specPattern.MatchString(s) {
			c.problemf(file, "#spec: %q is not <path>:<line>[-<line>]", s)
		}
	}

	for key, v := range dirs {
		suite, ok := strings.CutPrefix(key, "known/")
		if !ok {
			continue
		}
		if _, ok := c.Suite(suite); !ok {
			c.problemf(file, "#known/%s names a suite that %s does not declare", suite, SuitesFile)
		}
		m := knownValue.FindStringSubmatch(v)
		if m == nil {
			c.problemf(file, "#known/%s: %q is not #<issue> <reason>", suite, v)
			k.Known[suite] = KnownTag{Reason: v}
			continue
		}
		issue, err := strconv.Atoi(m[1])
		if err != nil {
			c.problemf(file, "#known/%s: %v", suite, err)
		}
		k.Known[suite] = KnownTag{Issue: issue, Reason: m[2]}
	}

	for _, f := range ar.Ar.Files {
		rest, isGolden := strings.CutPrefix(f.Name, "out/")
		if !isGolden {
			continue
		}
		suite, section, _ := strings.Cut(rest, "/")
		if _, ok := c.Suite(suite); !ok {
			c.problemf(file, "%s names a suite that %s does not declare", f.Name, SuitesFile)
			continue
		}
		if _, seen := k.verdicts[suite]; !seen {
			// The suite ran the case. Until its bind section is read, its
			// verdict is one that equals no #expect.
			k.verdicts[suite] = "(no bind section)"
		}
		if section != "bind" {
			continue
		}
		golden := string(f.Data)
		k.Goldens[suite] = golden
		first, _, _ := strings.Cut(golden, "\n")
		v, ok := strings.CutPrefix(first, "verdict: ")
		if !ok {
			c.problemf(file, "%s: the first line is not \"verdict: ...\"", f.Name)
			continue
		}
		norm, err := normalVerdict(v)
		switch {
		case err != nil:
			c.problemf(file, "%s: %v", f.Name, err)
		case norm != v:
			c.problemf(file, "%s: verdict %q is not sorted and de-duplicated (%q)", f.Name, v, norm)
		}
		k.verdicts[suite] = norm
	}
	for suite := range k.verdicts {
		if _, ok := k.Goldens[suite]; !ok {
			c.problemf(file, "out/%s/ has no bind section", suite)
		}
	}
	for suite := range k.Known {
		if _, ran := k.verdicts[suite]; !ran {
			c.problemf(file, "#known/%s tags a suite that does not run this case", suite)
		}
	}
	return k
}

// normalVerdict returns a verdict in its normal form: "ok", or the codes
// sorted, de-duplicated and joined by single spaces. A verdict it can't read
// is returned as written, which equals no well-formed verdict.
func normalVerdict(v string) (string, error) {
	fields := strings.Fields(v)
	switch {
	case len(fields) == 0:
		return v, fmt.Errorf("empty verdict")
	case len(fields) == 1 && fields[0] == "ok":
		return "ok", nil
	}
	for _, f := range fields {
		if !codePattern.MatchString(f) {
			return v, fmt.Errorf("%q is neither ok nor a set of codes", v)
		}
	}
	slices.Sort(fields)
	return strings.Join(slices.Compact(fields), " "), nil
}
