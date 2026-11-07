// Package corpus runs a corpus of cases through suites, and checks each
// result against the verdict the spec gives.
//
// A corpus is a directory of txtar archives. An archive is one case, or, when
// the corpus's [Layout] names inputs, one case for each input it holds. A
// suite is one way of running the corpus's operation on a case: for the refs
// corpus, of building, from the archive's files, the value passed to
// BindLineage (written together in one package, composed with thema.Compose,
// and so on); for a corpus of inputs, of taking one input to a Go value. A
// cell is one case run through one suite. The spec is the document a case's
// #spec directive cites (docs/invariants.md for the refs corpus), and every
// suite must give the verdict it decides. A cell that disagrees with it is a
// bug, either tracked by an issue (a #known tag) or untracked (a regression,
// or a new case that still needs its tag).
//
// # Archives
//
// The archive's comment holds directives, one per line, as "#key: value":
//
//	#description: what the case is about, in one to three plain sentences.
//	#expect: ok, or the set of codes the operation reports (E0141, or E0140 E0141).
//	#spec: where the spec decides #expect, as <path>:<line>[-<line>] citations
//	       separated by commas (docs/invariants.md:116-118, docs/invariants.md:220).
//	#known/<suite>: #<issue> why the suite disagrees with #expect today.
//
// A corpus may require more directives (the refs corpus requires #at). Each
// directive is given once. #expect is written by hand from the spec and never
// by a golden update: changing it is a change to what the spec says. Other
// comment lines are prose.
//
// The archive's other files are its input, which the suites read, and its
// golden sections: one for each case and each suite that applies to it. The
// section's first line is "verdict: " followed by ok or the sorted set of
// codes the operation reported. When the operation succeeded, the lines after
// it are what it produced, if anything (bind produces nothing); otherwise
// each is one diagnostic, sorted, as "<code> <headline> @ <position>". A suite
// whose operation succeeded but whose result is wrong, such as a value that
// lost what its input holds, returns [Wrong]: its verdict is wrong, which
// #expect never is, and its section records how the result is wrong. A cell
// with that verdict is tagged #known as any other that disagrees.
// Running with THEMA_UPDATE_GOLDEN=1 rewrites the out/ sections and nothing
// else, with two exceptions: it keeps a section that records a suite agreeing
// with #expect when the suite no longer does, and a section of a suite that no
// longer applies to the case. Each of those still fails, and only a person can
// resolve it.
//
// # Inputs
//
// When the layout names inputs, as a pattern such as data/*.json, each file of
// an archive that the pattern matches is a case of its own, named
// <archive>/<input>, where input is the file's name without its directory or
// extension: data/qty-zero.json in order.txtar is the case order/qty-zero.
// The cases share the archive's other files, such as the lineage.
//
// A directive written #key/<input> gives key for that input alone, and
// overrides #key, which gives it for every input of the archive: an archive
// writes #expect/qty-zero: E0504 for each input, and one #spec or
// #description for them all unless an input needs its own. Every input must
// have every required directive, from either form, and every value is read,
// even one every input overrides. A #known tag tracks one cell, so it is
// always written for one input, as #known/<suite>/<input>. A case's section
// for a suite is out/<suite>/<input>; an archive that is one case has the one
// section out/<suite>/bind.
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
// matches what the suite reports fails as well: as a change of verdict when
// the section's verdict differs, and as a change in rendering when only a
// code, headline, position or what the operation produced moved. Each failure
// prints the case, the suite, #expect with its citation, what the suite
// reported, and what to do about it. A suite that applies to no case fails
// the run.
//
// The suites a corpus registers, and its layout, are recorded in suites.json
// beside its archives, for tools that read the corpus without running it.
//
// The refs corpus checks reference placement for lineages written into one
// package, so it cannot express a lineage declared in another package and
// imported or re-exported. A change to placement.go must also pass TestCases
// in the repository's root package and TestLoadPaths in load.
package corpus
