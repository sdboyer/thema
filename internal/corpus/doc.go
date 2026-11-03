// Package corpus runs a corpus of cases through suites, and checks each
// result against the verdict the spec gives.
//
// A case is a txtar archive holding a lineage's text and the value that uses
// it. A suite is one way of building, from those files, the value passed to
// BindLineage: written together in one package, compiled apart and filled
// together with FillPath, unified in Go, and so on. A cell is one case run
// through one suite. Every suite should give the same verdict, so a cell
// that disagrees with the spec is either a bug that an issue tracks or a
// regression.
//
// # Cases
//
// The archive's comment holds directives, one per line, as "#key: value":
//
//	#description: what the case is about, in one to three plain sentences.
//	#expect: ok, or the set of codes bind reports (E0141, or E0140 E0141).
//	#spec: where the spec decides #expect (docs/invariants.md:116-118).
//	#known/<suite>: #<issue> why the suite disagrees with #expect today.
//
// A corpus may require more directives (the refs corpus requires #at).
// #expect is written by hand from the spec and never by a golden update:
// changing it is a change to what the spec says. Other comment lines are
// prose.
//
// The archive's other files are its input, which the suites read, and one
// golden section, out/<suite>/bind, for each suite that applies to the case.
// The section's first line is "verdict: " followed by ok or the sorted set of
// codes bind reported; each line after it is one diagnostic, sorted, as
// "<code> <headline> @ <position>". Running with THEMA_UPDATE_GOLDEN=1
// rewrites the out/ sections and nothing else.
//
// # Statuses
//
// A cell's status is read from the archive alone, by [CellStatus]:
//
//   - not-applicable: the case has no section for the suite.
//   - pass: the section's verdict is #expect.
//   - known: the verdict is not #expect, and a #known tag says so.
//   - regressed: the verdict is not #expect, and no tag says so.
//   - xpass: the verdict is #expect, but a #known tag says it is not.
//
// [Corpus.Run] computes the same status from what each suite reports now,
// and fails on regressed and xpass, so a fix is as loud as a regression: it
// must remove the tag. A pass or known cell whose section no longer matches
// what bind reports fails too, as a change in rendering. Each failure prints
// the case, the suite, #expect with its citation, what bind reported, and
// what to do about it.
//
// The suites a corpus registers are recorded in suites.json beside its
// cases, for tools that read the corpus without running it.
package corpus
