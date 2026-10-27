# Agent guidance for thema

## Inspecting CUE values

There is no single, reliable way to serialize a `cue.Value` that shows everything
you might need when working out what the evaluator is doing internally. `Syntax()`,
`fmt`/`%v`, `Expr()`, `Source()` and `Validate()` errors each hide something, such
as references, closedness, defaults, constraint vs. regular fields, or which
conjuncts a value came from. Expect to build small, purpose-specific inspection
code, and confirm hypotheses with a standalone reproduction before changing
thema's CUE or Go.

`internal/cueview` names the ways thema turns a value into syntax or text (the
"views"), each documented with its quirks. Its goldens
(`internal/cueview/testdata`) record what each view produces for definitions,
references and lets, aliases, hidden fields, comprehensions and output shape. Pick
a view by name rather than assembling `Syntax` options at a call site, and add a
golden case for any new quirk you rely on. Never decide anything about a value
from `Source()` (the `Written` view): it depends on how the value was
constructed, not on what it is. To follow references within a value,
`cueview.Tree` resolves its exported syntax.

**Concrete is not constant.** `Validate(cue.Concrete(true))` passes for a value
with a default (`*5 | int`) and for an open list or struct (`[...int]`,
`{a: 1, ...}`), because each has a concrete instance to offer, yet data can
still vary all of them. Any analysis that asks "can an instance change this?"
(reference classification, default detection, deciding whether a field is a
constant) must also check `Default()` and openness, never concreteness alone.
`internal/compat.constantTarget` is the reference implementation, and
`internal/compat/testdata/crossfield-defaulted-target.txtar` the case that
exposed it.

[github.com/sdboyer/cuerious](https://github.com/sdboyer/cuerious) helps with
this: `cuerious.ExprTree(v)` builds a walkable tree of a value's expression
structure, linking each node to its `cue.Value`. It is far from perfect, and it
was written against CUE v0.5, so it may need updating before use with thema's
current CUE version.

## Closedness in thema's CUE

thema's CUE API (`Lineage`, `SchemaDef`, `Lens`, `Lacuna`, …) is deliberately
built from regular fields, not definitions. Definitions close recursively, and the
schemas lineage authors write flow through these structures, so a definition
would impose closedness on every schema regardless of what its author wrote.
thema's CUE takes no position on schema openness. Operations that need closed
semantics apply them where they are used:

- **Compatibility checks**: `internal/compat` closes both versions
  (`cuetil.CloseRecursive`) before walking them. An author's `...` survives
  closing, and is the only openness the walk sees. The rules, their stable IDs
  and the Unknown cases are in `docs/invariants.md`; every verdict is recorded
  in the `internal/compat/testdata` corpus (the `compat-verdicts` seam).
- **`Schema.Validate()`**: closed by default.
- **Lens results**: closed against the target schema inside `Translate`. In Go,
  the fields each lens *writes* are also checked against what the target schema
  declares (`cuetil.UndeclaredLensFields`), even where the author wrote `...`.
  Lenses are strictly bound to the schema as defined. Each result field's origin
  is *copied* (input data, reached however indirectly: lets, aliases, `self`,
  other fields), *constant* (a literal) or *computed* (any other expression, or
  a mix); a reference to anything but input, such as a hidden helper field, has
  the origin of what it refers to. Copied data is not checked, and neither are
  steps with no lens (identity, minor versions). Origins are read from the
  lens's syntax (`cuetil.LensSyntax`), so they depend only on what the lens is;
  they also matter for compiling lenses to targets without an expression
  language, which can only copy. Hidden fields follow closedness: one the target
  doesn't declare is the lens's scratch space and isn't checked; beneath one it
  declares, the lens may write only what it declares.

Typos at thema-owned levels are caught twice:

- In CUE, a pattern constraint over unknown labels evaluates to `error()`.
- In Go, `checkUnknownFields` points at the author's line.

The few intentional definitions, and why each is one, are declared in the
`kernel-definitions` seam.

Never weaken a constraint, validator or closedness guarantee (e.g. by adding
`...`) to make a failure go away. Find the root cause, and if a constraint
itself seems wrong, raise it explicitly instead.

## Diagnostics

Every error thema reports is a `diag.Diagnostic` with a stable code (`E0xxx`,
allocated by hundreds: E01 lineage shape, E02 versions and compatibility, E03
lenses, E04 lacunas, E05 data, E06 loading and the CLI, E09 internal). Sites
build them with `diag.New(code)`; errors from CUE go through `diag.Convert` or
`diag.From`, which recognize CUE's messages by shape (`diag/shapes.go`) and
report what they don't recognize as E0902 with CUE's text verbatim. The CLI
renders them in the rustc layout, or as JSON with `--diag-format json`.

When writing or changing a diagnostic:

- **Author's vocabulary only.** Headlines, notes and help use the terms of the
  form the author wrote: version file, `schema`, `ForwardLens`, `v0_0` for the
  authoring form; `schemas`, `lenses`, `input`, `result` for the normal form.
  Never kernel or pipeline terms (`_accum`, `steps`, `Lineage.`, `#closed`,
  embedreg paths). `internal/diagtest.Lint` fails a headline that leaks.
- **Never embed CUE's text.** CUE's own message is the `Detail` or a note, never
  interpolated into a headline. No `cerrors.Details` in message construction.
- **Nothing dropped.** Every entry of `cerrors.Errors(err)` becomes a headline
  or a note of some diagnostic.
- **The primary position is the author's.** A thema-origin position is never a
  primary; it becomes a note. Only codes declared `Positionless` may have none.
- **Headline = rule + violator**, in one line under `diag.MaxHeadline`: the
  invariant in the author's terms and the construct that breaks it. A note says
  why; help says what to do.
- **One code, one reproducer.** `diag/testdata/codes/<code>.txtar` runs the
  operation (`#op`) and records the rendering; `docs/errors/<code>.md` is
  generated from it (`THEMA_UPDATE_GOLDEN=1 go test ./diag`). Read the rendered
  text before considering the diagnostic done. A code's meaning never changes:
  retire and allocate rather than reuse.
- **Compat and totality findings are not re-derived.** `compat.Finding` and the
  lenscheck findings are the source; `internal/rulecodes` maps each rule to its
  code and the finding's detail is the note.

<!-- BEGIN thresh explain: generated by `make agents-md` from .thresh/thresh.cue; do not edit this section by hand. -->
## Thresh seams

Before finalizing an approach that touches any of these, run `thresh check` and
read the taps. They are facts about your diff, not opinions, and they never block.

- **bindlineage** (`choke-point`): in *.go, only BindLineage may construct baseLineage. Constructing it elsewhere skips what the choke point enforces.
- **compat-verdicts** (`shared-kernel`): internal/compat/testdata/*.txtar is co-owned kernel. A change there owes consultation with: every lineage already bound: a verdict that moves can make a published minor step invalid or a major step unnecessary; the rule tables in docs/invariants.md, which must describe the recorded verdicts; the stability policy in docs/invariants.md: after v1 a recorded verdict may not change.
- **compile-roundtrip** (`shared-kernel`): internal/compile/compile.go, internal/compile/lens.go, internal/compile/decompile.go, internal/compile/testdata/roundtrip/*.txtar is co-owned kernel. A change there owes consultation with: the other direction: what Compile emits, Decompile must recognize, and what Decompile omits, Compile must regenerate; the round-trip fixtures in internal/compile/testdata/roundtrip, which hold both directions' goldens, and the compiler's diagnostics in the authoring vocabulary; the lens language design (#62) and the authoring file format (#61), which the vocabulary and layout follow.
- **content-hash** (`shared-kernel`): internal/contenthash/*.go, internal/contenthash/testdata/*.txtar is co-owned kernel. A change there owes consultation with: every stored version ID; a change to the golden digests in internal/contenthash/testdata is a change to them; internal/cueview SelfContained and its goldens, which the canonical form is built on; the tip and cut workflow (#65) and lens fixes (#67), which compare schema and lens digests.
- **cue-dependency** (`shared-kernel`): in go.mod, the lines mentioning cuelang.org/go are co-owned substrate. A change there owes consultation with: every lineage's validity; the Go facade's cue.Value handling; diag/shapes.go, which recognizes CUE's errors by the format string of Msg(); the menagerie (diag/testdata/menagerie.txtar, menagerieDigest in diag/menagerie_test.go) and docs/cue-errors.md record every shape thema depends on, and the digest fails on a change; cmd/thema load.go loadDiag, which recognizes two of the loader's failures by their text, since the loader formats them before thema sees them; Go code that works around evaluator behavior (assignable.go detaches values before Subsume); golden error output in testdata, and the reproducers in diag/testdata/codes with the docs/errors pages generated from them, which record CUE's wording (as details) and positions; internal/cueview goldens, which record how CUE presents values as syntax; every check built on a view (lens checks in internal/cuetil) depends on them; version IDs (internal/contenthash goldens): the canonical form is built on how CUE exports values, so an upgrade that changes the export changes stored IDs; internal/compile tests, which pin what the evaluator does with positions in AST built for authoring files: that a node's position carries its language version and experiments, and where errors' positions land in thema's pipeline.
- **cue-error-shapes** (`shared-kernel`): diag/shapes.go, diag/shapes_*.go, diag/testdata/menagerie.txtar, docs/cue-errors.md is co-owned kernel. A change there owes consultation with: menagerieDigest in diag/menagerie_test.go, updated by hand after reading the regenerated menagerie and docs/cue-errors.md; TestShapesCovered and the unmatchedYet backlog: every recorded format is matched by a shape or listed, so nothing is reported as E0902 unknowingly; the reproducers in diag/testdata/codes, whose details quote CUE's text.
- **cue-go-binding** (`references-resolve`): Go files (*.go) reach into CUE files (*.cue) by literal path; every such reference must resolve.
- **cue-module** (`shared-kernel`): cue.mod/module.cue is co-owned kernel. A change there owes consultation with: everything that evaluates thema's CUE.
- **cue-views** (`declared-set`): the [A-Z]* functions in internal/cueview/view.go are an intentional, enumerated set (8 declared). Adding one requires declaring why it belongs.
- **cueview-goldens** (`shared-kernel`): internal/cueview/testdata/*.txtar is co-owned kernel. A change there owes consultation with: every caller of the view whose output changed (internal/cuetil syntax.go reads Evaluated through cueview.Tree; internal/contenthash builds its canonical form on SelfContained); the view's doc comment in internal/cueview/view.go, which lists its quirks.
- **diag-codes** (`shared-kernel`): diag/codes.go, diag/testdata/codes/*.txtar, docs/errors/*.md is co-owned kernel. A change there owes consultation with: every consumer that matches on a code: the CLI's --diag-format json readers, docs/errors links, the playground; retire and allocate rather than change a code's meaning; the lockstep tests in diag/codes_test.go: one reproducer per code, docs/errors generated from them (THEMA_UPDATE_GOLDEN=1 go test ./diag); the authoring principles in AGENTS.md (Diagnostics), enforced by internal/diagtest.Lint over every corpus; internal/compile/vocab.go and diagnose.go, which render the same codes in the authoring and normal-form vocabularies.
- **embedded-module-root** (`shared-kernel`): internal/embedreg/embedreg.go is co-owned kernel. A change there owes consultation with: users reading error positions (the path is user-facing; diag renders a file under it as thema/<file>); diag/pos.go, which classifies every position's origin by IsKernelFile, and the Positionless rule in diag: a kernel position is never a primary; golden error output in testdata, diag/testdata and docs/errors.
- **embedded-registry** (`choke-point`): in internal/embedreg/*.go, only Configure may construct registry. Constructing it elsewhere skips what the choke point enforces.
- **go-residual-checks** (`declared-set`): the check* functions in bind.go are an intentional, enumerated set (13 declared). Adding one requires declaring why it belongs.
- **kernel-definitions** (`declared-set`): the *#* definitions in lineage.cue, translate.cue, lacuna.cue, traverse.cue are an intentional, enumerated set (1 declared). Adding one requires declaring why it belongs.
- **kernel-paths** (`shared-kernel`): runtime.go is co-owned kernel. A change there owes consultation with: internal/embedreg IsKernel, IsKernelFile, KernelPath and AuthorPos, which recognize thema's CUE only at embedreg.Root() and embedreg.RuntimeRoot(); diag/pos.go, which classifies every position's origin through them, and diag.AuthorPos; every error position bind.go takes through them; internal/compile classify, and the kernel positions in its pipeline goldens; diag/render.go and cmd/thema readSource, which display and read thema's files by those roots.
- **lacuna-types** (`shared-kernel`): lacuna/lacuna.go is co-owned kernel. A change there owes consultation with: LacunaTypes in lacuna.cue (lockstep test TestLacunaTypesMatchCUE); instance.go Translate, which decodes the lacunas of every step into these types.
- **lens-result-fields** (`shared-kernel`): internal/cuetil/declared.go, internal/cuetil/syntax.go, internal/cueview/tree.go is co-owned kernel. A change there owes consultation with: bind.go checkLensResults and checkLacunas, and instance.go checkLensResultFields: all must apply the same rule, and report it as E0305; AGENTS.md, Lens results; testdata/cases/lens-* and the construction-independence tests in internal/cuetil.
- **lenscheck-verdicts** (`shared-kernel`): internal/lenscheck/testdata/*.txtar is co-owned kernel. A change there owes consultation with: every lineage already bound: a verdict that moves can make a published lens fail to bind, or admit one that fails at translate; the rule tables in docs/invariants.md (Lens totality), which must describe the recorded verdicts; the stability policy in docs/invariants.md: after v1 a recorded verdict may not change; internal/compat, whose verdicts the copy rule reuses: a compat verdict that moves moves copies with it.
- **lineage-kernel** (`shared-kernel`): lineage.cue, translate.cue, lacuna.cue, traverse.cue is co-owned kernel. A change there owes consultation with: every lineage that unifies with thema.Lineage; the Go facade (bind.go, lineage.go, surface.go); Go code that calls thema's CUE pseudofunctions and decodes their output shape (instance.go Translate decodes Translate's out); bind.go parseVersionKey, parseLensEdge and lensID.String, which mirror the schemas and lenses key formats (versionKey, edgeKey in lineage.cue).
- **lineages** (`published-language`): exemplars/*.cue, docs/*.cue is a versioned contract for consumers you cannot enumerate. A change owes them compatibility; a breaking change is loud even when correct.
- **playground-api** (`shared-kernel`): playground/types.go, playground/ui/src/api/types.ts, playground/session.go, cmd/thema-wasm/main.go, cmd/thema-wasm/thema-fs.js is co-owned kernel. A change there owes consultation with: playground/ui, in the same change: a renamed field, operation or bridge function breaks it; its tests run against this tree's thema.wasm (go generate ./cmd/thema/playground, then npm test in playground/ui); playground/ui/src/api/types.ts, regenerated from the Go types (THEMA_UPDATE_GOLDEN=1 go test ./playground), and the response goldens in playground/testdata; the release assets a statically hosted playground fetches by tag: thema.wasm, wasm_exec.js, thema-fs.js, types.ts.
- **thema-module-identity** (`shared-kernel`): internal/util/modfile.go is co-owned kernel. A change there owes consultation with: cue.mod/module.cue (lockstep test in internal/util); the release process, which tags the Go and CUE modules with ThemaModuleVersion; every module.cue thema synthesizes (util.ModuleFile) and every cue.Hid lookup.

### Recording whether a tap helped

Each tap carries an ID (`#<id>`). Once it is clear whether a tap was useful (not
before), record a verdict:

    thresh verdict <id> --by origin --useful yes|no|unclear --evidence "what happened"

Use `--by origin` if the tap was delivered to you; `--by sweep` if you are reviewing
the work afterwards.

- **yes**: the tap caused or confirmed something observable: the diff changed, a
  co-owner was consulted, a version or compatibility decision was revisited, or you
  explicitly checked that the crossing was intended.
- **no**: the tap was true, but nothing followed and nothing needed to.
- **unclear**: it can't be determined (e.g. the work was abandoned).
- If the tap misstated the diff, pass `--inaccurate` instead of `--useful`: that is a
  thresh bug, not a judgment.

A verdict is a record, never a request: it does not change what thresh reports.
<!-- END thresh explain -->
