# The reference-placement corpus

Each case here is one lineage, used in one way. Each suite builds the value
passed to `BindLineage` from the case's files in its own way, so one case is
bound several times. The spec, `docs/invariants.md` (References), gives one
verdict for the lineage, and every suite must give it. A suite that gives
another verdict is either a bug an issue tracks (a `#known` tag) or a
regression.

`TestRefs` in `internal/corpus/refs_test.go` runs the corpus. The framework is
`internal/corpus`; its package documentation describes the format.

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
- **`#known/<suite>: #<issue> <reason>`**, one per suite that disagrees with
  `#expect` today: the issue that tracks it, and one sentence naming the
  mechanism.
- **Other `.cue` files** are other packages of the module: `common/common.cue`
  is package `example.com/m/common`.
- **`out/<suite>/bind`** is what that suite's bind reported: the verdict line,
  then one line per diagnostic. Never write these by hand.

Some suites compile `text.cue` on its own, so they apply only to a text that
compiles on its own: one that refers to nothing outside itself except CUE's
builtin packages. A case whose text refers to `in.cue`, or imports a package
of the module, runs only through `together`; the others are skipped, with the
reason.

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
   in `#spec`. If the spec does not decide the case, don't add it; raise it
   instead.
4. Run `THEMA_UPDATE_GOLDEN=1 go test ./internal/corpus -run TestRefs/<name>`
   to write the `out/` sections, then run it again without the variable.
5. For each suite that fails as a regression, the code disagrees with the
   spec. Find the issue that covers the mechanism and add
   `#known/<suite>: #<issue> <one sentence naming the mechanism>`. If no issue
   covers it, don't make one up; ask for one.
6. Read every `out/` section before committing it.

## What a failure means

Every failing cell prints the case, the suite, `#expect` and its citation,
what bind reported, any `#known` tag, and one of these.

- **"This is a regression"**: the suite gave a verdict other than `#expect`,
  and no `#known` tag says it does. Your change broke a case that was right,
  or the case is new and the suite is wrong. Don't update goldens or add a tag
  to make it pass. Updating goldens doesn't change the status, which compares
  against `#expect`. If you believe `#expect` is wrong, it comes from the spec
  cited, so raise it with the owner. Changing `#expect` is a change to the
  spec.
- **"This suite now gives the verdict the spec expects"** (an xpass): a cell
  tagged `#known` now agrees with `#expect`. Your change fixed it. Remove the
  tag, mention the issue in your commit, and check the issue's other cells.
- **"The verdict is unchanged, but what bind reports changed"**: the verdict is
  the same, but a code, headline or position moved. Review the diff. If the
  change is intended, run with `THEMA_UPDATE_GOLDEN=1` and commit the new
  sections.
- **A directive is missing, or a tag or section is stale**: the case itself is
  malformed. A `#known` tag must start with `#<issue>`, name a registered
  suite, and name one that applies to the case. An `out/` section whose suite
  is gone or no longer applies is removed by `THEMA_UPDATE_GOLDEN=1`.

A cell tagged `#known` that still disagrees doesn't fail. It logs its issue.

## Limits

- `#expect` is the set of codes, so it can't tell one finding from two with
  the same code. A suite that adds a false finding with a code the case
  already expects passes. Its `out/` section shows the extra finding
  (`text-ref-name-narrowed` is one).
- Positions differ between suites, because each compiles the text under a
  different file name: `/m/text.cue` for `together`, one line below the
  line in `text.cue`; `text.cue` for `fill-text-named`; no name for the rest.
