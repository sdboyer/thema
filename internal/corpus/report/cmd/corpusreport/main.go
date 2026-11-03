// Command corpusreport compares a corpus of cases run by many suites (such as
// testdata/refs) at two revisions, and reports every cell that changed: as one
// self-contained HTML page with the whole matrix, and as a Markdown summary for
// a pull request's description.
//
//	go run ./internal/corpus/report/cmd/corpusreport --dir testdata/refs --range origin/main..HEAD --html out.html --md
//
// --range is "A..B", "A...B" (from the merge base of A and B to B), or "A"
// (from A to the working tree). The corpus is read through git, so nothing is
// checked out. It exits 1 if a cell of the after revision is regressed or
// xpass, or its corpus breaks the format, and 2 if it can't make the report.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/sdboyer/thema/internal/corpus/report"
)

const (
	// exitFailing: the report was made, and the after revision may not be
	// committed.
	exitFailing = 1
	// exitError: the report could not be made.
	exitError = 2
	// filePerm is for the HTML report, which only its reader reads.
	filePerm = 0o600
)

func main() {
	dir := flag.String("dir", "testdata/refs", "corpus directory, relative to the current directory")
	rng := flag.String("range", "origin/main...HEAD", `revisions to compare: "A..B", "A...B", or "A" for A against the working tree`)
	htmlOut := flag.String("html", "", "write the HTML report to this file")
	md := flag.Bool("md", false, "print the Markdown summary to stdout")
	flag.Parse()

	r, err := build(*dir, *rng)
	if err != nil {
		fmt.Fprintln(os.Stderr, "corpusreport:", err)
		os.Exit(exitError)
	}
	if err := write(r, *htmlOut, *md); err != nil {
		fmt.Fprintln(os.Stderr, "corpusreport:", err)
		os.Exit(exitError)
	}
	if r.Failing() {
		fmt.Fprintf(os.Stderr, "corpusreport: the corpus in %s has regressed or xpass cells, or breaks its format\n", r.Range.AfterLabel())
		os.Exit(exitFailing)
	}
}

func build(dir, rng string) (*report.Report, error) {
	repo := report.Repo{Dir: "."}
	rg, err := repo.ParseRange(rng)
	if err != nil {
		return nil, err
	}
	before, err := repo.Load(rg.Before, dir)
	if err != nil {
		return nil, err
	}
	after, err := repo.Load(rg.After, dir)
	if err != nil {
		return nil, err
	}
	return report.Build(dir, rg, before, after), nil
}

func write(r *report.Report, htmlOut string, md bool) error {
	if htmlOut != "" {
		var b bytes.Buffer
		if err := report.HTML(&b, r); err != nil {
			return err
		}
		if err := os.WriteFile(htmlOut, b.Bytes(), filePerm); err != nil {
			return err
		}
	}
	if md {
		return report.Markdown(os.Stdout, r)
	}
	return nil
}
