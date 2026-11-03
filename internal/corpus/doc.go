// Package corpus runs a corpus of cases through suites, and checks each
// result against the verdict the spec gives.
//
// A case is a txtar archive holding a lineage's text and the value that uses
// it. A suite is one way of building, from those files, the value passed to
// BindLineage: written together in one package, compiled apart and filled
// together with FillPath, unified in Go, and so on. A cell is one case run
// through one suite. The spec is the document a case's #spec directive cites
// (docs/invariants.md for the refs corpus), and every suite must give the
// verdict it decides. A cell that disagrees with it is a bug, either tracked
// by an issue (a #known tag) or untracked (a regression, or a new case that
// still needs its tag).
//
// # Cases
//
// The archive's comment holds directives, one per line, as "#key: value":
//
//	#description: what the case is about, in one to three plain sentences.
//	#expect: ok, or the set of codes bind reports (E0141, or E0140 E0141).
//	#spec: where the spec decides #expect, as <path>:<line>[-<line>] citations
//	       separated by commas (docs/invariants.md:116-118, docs/invariants.md:220).
//	#known/<suite>: #<issue> why the suite disagrees with #expect today.
//
// A corpus may require more directives (the refs corpus requires #at). Each
// directive is given once. #expect is written by hand from the spec and never
// by a golden update: changing it is a change to what the spec says. Other
// comment lines are prose.
//
// The archive's other files are its input, which the suites read, and one
// golden section, out/<suite>/bind, for each suite that applies to the case.
// The section's first line is "verdict: " followed by ok or the sorted set of
// codes bind reported; each line after it is one diagnostic, sorted, as
// "<code> <headline> @ <position>". Running with THEMA_UPDATE_GOLDEN=1
// rewrites the out/ sections and nothing else, with two exceptions: it keeps
// a section that records a suite agreeing with #expect when the suite no
// longer does, and a section of a suite that no longer applies to the case.
// Each of those still fails, and only a person can resolve it.
//
// # Statuses
//
// A cell's status is read from the archive alone, by [CellStatus], which the
// report generator (internal/corpus/report) uses too:
//
//   - not-applicable: the case has no section for the suite.
//   - pass: the section's verdict is #expect.
//   - known: the verdict is not #expect, and a #known tag says so.
//   - regressed: the verdict is not #expect, and no tag says so.
//   - xpass: the verdict is #expect, but a #known tag says it is not.
//
// [Corpus.Run] computes the status from what each suite reports now, which
// differs from [CellStatus] wherever a section is out of date. It fails on
// regressed and xpass, so a fix fails the test too, until whoever made it
// removes the #known tag. A pass or known cell whose section no longer
// matches what bind reports fails as well: as a change of verdict when the
// section's verdict differs, and as a change in rendering when only a code,
// headline or position moved. Each failure prints the case, the suite,
// #expect with its citation, what bind reported, and what to do about it.
// A suite that applies to no case fails the run.
//
// The suites a corpus registers are recorded in suites.json beside its
// cases, for tools that read the corpus without running it.
//
// The refs corpus checks reference placement for lineages written into one
// package, so it cannot express a lineage declared in another package and
// imported or re-exported. A change to placement.go must also pass TestCases
// in the repository's root package and TestLoadPaths in load.
package corpus
