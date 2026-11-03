// Package report compares a corpus of cases run by many suites at two
// revisions, and renders the result as a matrix (HTML) or a summary for a pull
// request (Markdown).
//
// A corpus is a directory of txtar archives, one case per file, and a
// suites.json naming the suites that run them; testdata/refs is the first.
// A cell is one case run by one suite. The package corpus defines the format,
// reads each case and computes each cell's status from the archive alone
// ([corpus.CellStatus]), as its test framework does, so the report and the
// tests never disagree about a cell. A committed corpus holds only pass,
// known and not-applicable cells.
package report

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/sdboyer/thema/internal/corpus"
)

// Status is the status of a cell, as corpus.Status names it.
type Status string

// The statuses a cell can have in a corpus, and Absent, which only a report
// uses: the case or the suite does not exist in that revision.
var (
	NotApplicable = statusOf(corpus.StatusNotApplicable)
	Pass          = statusOf(corpus.StatusPass)
	Known         = statusOf(corpus.StatusKnown)
	Regressed     = statusOf(corpus.StatusRegressed)
	XPass         = statusOf(corpus.StatusXPass)
)

// Absent is the status of a cell whose case or suite does not exist in that
// revision.
const Absent Status = "absent"

func statusOf(s corpus.Status) Status { return Status(s.String()) }

// Statuses lists every status a corpus can give a cell, in the order a report
// shows them.
var Statuses = []Status{Regressed, XPass, Known, Pass, NotApplicable}

// SuitesFile is the name of a corpus's suite registry, beside its archives.
const SuitesFile = "suites.json"

// require names the directives a case must have beyond description, expect
// and spec. It is what TestRefs requires of testdata/refs, the only corpus.
var require = []string{"at"}

// Suite is one way the corpus's cases are run, as suites.json declares it.
type Suite struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// EveryCase says the suite applies to every case, so every case has a
	// section for it.
	EveryCase bool `json:"everyCase,omitempty"`
}

// Corpus is a corpus as it is in one revision.
type Corpus struct {
	Suites []Suite
	// Cases are sorted by name.
	Cases []*corpus.Case
	// Problems are the ways the corpus breaks its format, each naming the file
	// it is in. A cell's status is still computed where it can be.
	Problems []string
	// Missing says the corpus directory does not exist in the revision.
	Missing bool
	// Unreadable says suites.json could not be read, so the cases were not
	// read either: without the suites, every section would read as an orphan.
	// Problems says why.
	Unreadable bool
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
func (c *Corpus) Case(name string) *corpus.Case {
	for _, k := range c.Cases {
		if k.Name == name {
			return k
		}
	}
	return nil
}

// CellStatus is the status of case k's cell for suite.
func CellStatus(k *corpus.Case, suite string) Status {
	return statusOf(corpus.CellStatus(k, suite))
}

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
		if err := c.parseSuites(data); err != nil {
			c.Problems = []string{SuitesFile + ": " + err.Error()}
			c.Unreadable = true
			return c
		}
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

func (c *Corpus) parseSuites(data []byte) error {
	var reg struct {
		Suites []Suite `json:"suites"`
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		return err
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
	return nil
}

func (c *Corpus) parseCase(file string, data []byte) *corpus.Case {
	k := corpus.ParseCase(file, data)
	registered := make([]string, 0, len(c.Suites))
	for _, s := range c.Suites {
		registered = append(registered, s.Name)
	}
	directives, sections := k.Problems(registered, require)
	for _, p := range append(directives, sections...) {
		c.problemf(file, "%s", p)
	}
	// The test framework finds a tag on a suite that does not run the case
	// when it runs the cell; read from the archive alone, it is a tag with no
	// section.
	for suite := range k.Known {
		if _, ran := k.File(corpus.SectionFile(suite)); !ran {
			c.problemf(file, "#known/%s tags a suite that does not run this case", suite)
		}
	}
	for _, s := range c.Suites {
		if _, ran := k.File(corpus.SectionFile(s.Name)); s.EveryCase && !ran {
			c.problemf(file, "%s is missing, but suite %s applies to every case", corpus.SectionFile(s.Name), s.Name)
		}
	}
	return k
}
