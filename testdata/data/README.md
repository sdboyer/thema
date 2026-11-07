# The data corpus

Each archive here is one lineage, in the authoring form, with inputs: JSON
documents, each a case of its own. Each suite takes every input from JSON to
the Go type generated for the schema at the archive's `#version`, in its own
way. The spec, `docs/translation.md` ("What an instance may hold"), says
whether each input is an instance of the schema, and what a program reading
it sees. Every suite must give that verdict, and a Go value that holds what
the instance holds, defaults included, wherever the Go type has a place for
it. A suite that gives another verdict is
a bug: either an issue tracks it (a `#known` tag), or nothing does yet (a
regression, or a new case that still needs its tag).

`TestData` in `internal/corpus/data_test.go` runs the corpus. The framework is
`internal/corpus`; its package documentation describes the format, inputs
included.

## An archive

```
#description: An order for a shop's checkout.
#version: 0.0
#spec: docs/translation.md:48
#expect/full: ok
#expect/quantity-below-1: E0504
#spec/no-id: docs/translation.md:49
#expect/no-id: E0501
-- order/history/0.0.thema --
Schema: {
	id:       string
	quantity: int & >=1
}
-- data/full.json --
{"id":"ord_1","quantity":2}
-- data/quantity-below-1.json --
{"id":"ord_1","quantity":0}
-- data/no-id.json --
{"quantity":2}
-- out/cue/full --
verdict: ok
value: {"id":"ord_1","quantity":2}
```

- **`<name>/history/<version>.thema`** is the lineage, as an author writes
  it; `<name>` is the lineage's name. The archive holds exactly one lineage.
- **`#version`** is the version of the schema the inputs are data of.
- **`data/<input>.json`** is one input, and one case, `<archive>/<input>`.
  Input names are kebab-case.
- **`#expect/<input>`**, **`#spec/<input>`** and **`#description/<input>`**
  give the input's verdict, citation and description. Written without
  `/<input>`, a directive gives it for every input that doesn't have its own.
  `#expect` is `ok`, or the code of the rule the input breaks.
- **`#known/<suite>/<input>: #<issue> <reason>`**, one for each cell that
  disagrees with `#expect` today. A tag is always for one input.
- **`out/<suite>/<input>`** is what that suite reported for the input: the
  verdict line, then the Go value as JSON (`value: ...`) when the input is
  accepted, one line per diagnostic when it is not, or how the Go value is
  wrong (below). Never write these by hand.

A suite checks the Go value it produces against the reference value: the
instance (CUE's value of the input unified with the schema, closed as
validation closes it, every default filled), decoded onto the same Go type
with go-json-experiment, which matches each field by exactly the name its tag
gives it. Where the type holds a number as `any`, the reference holds an
`int64`, a `*big.Int` for an integer outside `int64`'s range, or a `float64`
for a number with a fraction or an exponent (A7), as `datatypes.Number`
says. A Go value that differs has lost or changed what the instance holds,
which is a bug in thema, and the cell's verdict is `wrong`, with both values
in its section. `#expect` is never `wrong`, so a missed default fails its cell
like any other wrong verdict, and is tagged `#known` like one.

The check sees only what the Go type has a place for. Data the schema does
not declare, under `...`, has none unless it is inside a field the type holds
as `any`, so the check can't tell whether a suite kept it; it can tell when
such data changed a declared field.

## The Go types

Each archive's types are generated, by `SchemaTypes`, into
`internal/corpus/datatypes/<archive>/types.go` (the archive's name without
its dashes), and `internal/corpus/datatypes/registry.go` binds each to its
schema. `TestDataTypes` fails when they are not what `SchemaTypes` generates
now; `THEMA_UPDATE_GOLDEN=1 go test ./internal/corpus -run TestDataTypes`
rewrites them.

## Adding a case

1. To add inputs to an archive, write each as `data/<input>.json`. For a new
   lineage, write a new archive with a name that is a Go identifier once its
   dashes are gone (it names the Go package of its types), holding the
   lineage and its inputs.
2. Write `#description` for someone new to the code: the scenario, and what
   the rule says about it. An input that needs more than the archive's says
   so in `#description/<input>`.
3. Decide each input's `#expect` from the spec, not from what the code does.
   Read "What an instance may hold" in `docs/translation.md` and the field
   forms in `docs/invariants.md`: which rule the input meets or breaks gives
   the verdict. `ok` if it is an instance; otherwise the code of the rule it
   breaks (`diag/codes.go`: E0501 a required field is absent, E0502 a field
   the schema does not declare, E0503 a value of the wrong kind, E0504 a value
   outside a bound, pattern or validator, E0505 a value matching no
   alternative, E0507 data that does not parse, including a string escape
   that is not a character, A8). Cite the lines that decide
   it in `#spec`. Make each invalid input break one rule. If the spec does
   not decide the case, don't add it; raise it instead.
4. For a new archive, or a changed schema, run
   `THEMA_UPDATE_GOLDEN=1 go test ./internal/corpus -run TestDataTypes` to
   generate its types; it refuses an archive that breaks the format, so this
   comes once every directive is written. Then run
   `THEMA_UPDATE_GOLDEN=1 go test ./internal/corpus -run 'TestData$/<archive>'`
   to write the `out/` sections, then run it again without the variable.
5. For each cell that fails with "This suite disagrees with #expect", the
   code disagrees with the spec. Find the issue that covers the mechanism and
   add `#known/<suite>/<input>: #<issue> <one sentence naming the mechanism>`.
   If no issue covers it, don't make one up; open one that cites the `#spec`
   lines.
6. Read every `out/` section before committing it: the value, as much as the
   verdict.

## What a failure means

Failures read as in the refs corpus (`testdata/refs/README.md`, "What a
failure means"), with one more source of change: a suite's section records
the Go value an accepted input decodes to, so a change in the value is a
change in rendering, and a Go value that loses what the instance holds is a
change of verdict, to `wrong`.

## Limits

- Each suite stops at the rule it reports first, or reports every rule the
  input breaks, so an input that breaks two rules can give suites different
  verdicts. Make each invalid input break one.
- Positions and headlines differ between suites; only the verdict is judged.
