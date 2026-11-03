# The reference-placement corpus

Each case here is one lineage, used in one way. Each suite builds, from the
case's files and in its own way, the package the lineage is bound from, so
one case is bound several times. The spec, `docs/invariants.md` (References), gives one
verdict for the lineage, and every suite must give it. A suite that gives
another verdict is a bug: either an issue tracks it (a `#known` tag), or
nothing does yet (a regression, or a new case that still needs its tag).

`TestRefs` in `internal/corpus/refs_test.go` runs the corpus. The framework is
`internal/corpus`; its package documentation describes the format.

Every case writes the lineage into package `m`, so the corpus can't express a
lineage declared in another package and imported or re-exported. A change to
`placement.go` must also pass `TestCases` (`go test .`) and `TestLoadPaths`
(`go test ./load`).

## A case

```
#description: A schema in the text refers to the lineage's name. A schema may refer only to its own body, so this is ref-lineage-meta.
#expect: E0141
#spec: docs/invariants.md:116-118, docs/invariants.md:220
#at: lin.h
-- in.cue --
import "github.com/sdboyer/thema"

lin: h: thema.Lineage & {name: "h"}
-- text.cue --
name: "h"
schemas: "0": "0": schema: {a?: string, n?: name}
-- out/together/bind --
verdict: E0141
E0141 n in schema 0.0 refers to name, which is declared in the lineage outside the schema @ /m/text.cue:3:45
```

- **`text.cue`** is the lineage's text, as an author writes it: the struct
  declaring its schemas (and lenses, name or joinSchema, if the case needs
  them), with no package clause. It may import packages before its fields.
- **`in.cue`** is package `m` of module `example.com/m`, the value that uses the
  lineage. `package m` is added when it is missing. It usually holds
  `thema.Lineage` at the field where the lineage goes, and may narrow the
  lineage's joinSchema there.
- **`#at`** is the path in package `m` where the lineage goes, such as `lin.h`.
  `in.cue` must declare it: the suites that fill the text into the use fill it
  there.
- **`#known/<suite>: #<issue> <reason>`**, one per suite that disagrees with
  `#expect` today: the issue that tracks it, and one sentence naming the
  mechanism. Reasons use two terms from CUE's evaluator: a *root* is the
  value one loaded package or one compiled text evaluates to, and a value's
  *first conjunct* is the first of the values unified to make it.
- Each directive is given once.
- **Other `.cue` files** are other packages of the module: `common/common.cue`
  is package `example.com/m/common`.
- **`out/<suite>/bind`** is what that suite's bind reported: the verdict line,
  then one line per diagnostic. Never write these by hand.

`compose` writes `text.cue` into package `m` at `#at` with `thema.Compose`,
so it applies only to a text that compiles on its own: one that refers to
nothing outside itself except CUE's builtin packages. A case whose text
refers to `in.cue`, or imports a package of the module, runs only through
`together`; `compose` is skipped, with the reason.

A lineage assembled in Go, by filling or unifying separately built values, is
refused (`E0152`) rather than given a verdict; `TestRefsGoAssembly` checks
that every case whose text compiles on its own is refused that way along each
Go construction.

The suites, and how each builds the value, are in `suites.json`.

## Adding a case

1. Write the archive, `testdata/refs/<name>.txtar`, with a kebab-case name and
   no `out/` sections.
2. Write `#description` for someone new to the code: the scenario, and what
   the rule says about it.
3. Decide `#expect` from the spec, not from what the code does. Read
   `docs/invariants.md` (References): which rule the reference meets or breaks
   gives the verdict. `ok` if the lineage binds; otherwise the code of each
   rule that refuses something (the rule tables name the rules;
   `diag/codes.go` maps each rule to its code). Cite the lines that decide it
   in `#spec`; the test checks that each cited line exists. If the spec does
   not decide the case, don't add it; raise it instead.
4. Run `THEMA_UPDATE_GOLDEN=1 go test ./internal/corpus -run TestRefs/<name>`
   to write the `out/` sections, then run it again without the variable.
5. For each suite that fails with "This suite disagrees with #expect", the
   code disagrees with the spec. Find the issue that covers the mechanism and
   add `#known/<suite>: #<issue> <one sentence naming the mechanism>`. If no
   issue covers it, don't make one up; open one that cites the `#spec` lines.
6. Read every `out/` section before committing it.

## What a failure means

Every failing cell prints the case, the suite, `#expect` and its citation,
what bind reported, any `#known` tag, and one of these. A case or suite is
described in full only in the first failure that names it.

- **"This is a regression"**: the golden section records the suite giving
  `#expect`, and it no longer does. Your change broke a case that was right.
  Don't add a tag to make it pass. `THEMA_UPDATE_GOLDEN=1` records what the
  suite gives now, and the cell keeps failing until the suite agrees again; the
  report shows it as a change from pass to regressed. If you believe
  `#expect` is wrong, it comes from the spec cited, so open an issue citing
  those lines. Changing `#expect` is a change to the spec.
- **"This suite disagrees with #expect"**: no golden section records the
  suite agreeing, and no tag tracks the disagreement. For a new case, this is
  step 5 above. If git shows the section agreeing before your change, it is a
  regression whose section was rewritten: fix the code.
- **"This suite now gives the verdict the spec expects"** (an xpass): a cell
  tagged `#known` now agrees with `#expect`. Your change fixed it. Remove the
  tag, rewrite the section with `THEMA_UPDATE_GOLDEN=1`, and mention the issue
  in your commit. The failure lists the other cells that carry the same issue,
  so you know whether it can be closed.
- **"This suite's verdict changed"**: a cell tagged `#known` still disagrees
  with `#expect`, but with another verdict than its golden section records.
  The tag may now name the wrong mechanism. Check it against the new verdict,
  or whether this is a different bug, before rewriting the section. A golden
  update rewrites the section but fails this once more, since the tag isn't
  rewritten.
- **"This suite now gives #expect"**: an untagged cell agrees, but its golden
  section records another verdict, usually because you just removed its tag.
  Rewrite the section with `THEMA_UPDATE_GOLDEN=1`.
- **"The verdict is unchanged, but what bind reports changed"**: the verdict
  is the same, but a code, headline or position moved. Review the diff. If
  the change is intended, run with `THEMA_UPDATE_GOLDEN=1` and commit the new
  sections.
- **"There is no golden section"**: the suite runs the case, but the case has
  no section for it. Run with `THEMA_UPDATE_GOLDEN=1` to write it.

Other failures are about the case's text, not a cell's verdict:

- **A directive is missing, repeated or malformed**, a `#spec` line doesn't
  exist, or the file name isn't kebab-case. A `#known` tag must start with
  `#<issue>`, name a registered suite, and name one that applies to the case.
- **"no longer applies"**: a suite that has a section for the case no longer
  applies to it. `THEMA_UPDATE_GOLDEN=1` keeps the section. Fix the suite, or,
  if it should not run the case, delete the section (and its tag) by hand.
- **"applies to no case"**: a registered suite runs nothing, so it checks
  nothing.
- An `out/` section whose suite is no longer registered, or that is repeated,
  is removed by `THEMA_UPDATE_GOLDEN=1`.

A cell tagged `#known` that still disagrees, with the verdict its section
records, doesn't fail. It logs its issue.

## Limits

- `#expect` is the set of codes, so it can't tell one finding from two with
  the same code. A suite that adds a false finding with a code the case
  already expects passes, though its `out/` section shows the extra finding.
  Write a case so that each finding a suite could add falsely has a code the
  case doesn't expect.
- Positions differ between suites, because each reads the text under a
  different file name: `/m/text.cue` for `together`, one line below the
  line in `text.cue`; `text.cue` for `compose`, at its line.
