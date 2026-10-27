package thresh

// Thresh annotations for the thema pilot. Data only — the #Seam schema lives
// in the thresh binary and is unified against this at load (no import).

// --- published-language: consumers we cannot enumerate ---------------------------

// Every lineage is a versioned contract. thema makes authors declare the version
// tuple and enforces that a major bump means breaking, so the compat verdict is
// read straight from the declaration.
seams: lineages: {
	kind:   "published-language"
	paths:  ["exemplars/*.cue", "docs/*.cue"]
	format: "thema"
}

// Every compatibility verdict is part of the versioning contract: a lineage is
// valid only if its minor steps are compatible and its major steps are not, so
// a verdict that changes can invalidate lineages already published. The corpus
// records one verdict per rule; a diff there is a contract change, even when it
// fixes a bug. (Before v1 verdicts are declared unstable; the diff is still loud.)
// This is a published language whose consumers are every bound lineage, but
// thresh's published-language kind only reads version tuples, which the corpus
// has none of (sdboyer/thresh#6), so it is declared as a kernel to fire on touch.
seams: "compat-verdicts": {
	kind:  "shared-kernel"
	paths: ["internal/compat/testdata/*.txtar"]
	consult: [
		"every lineage already bound: a verdict that moves can make a published minor step invalid or a major step unnecessary",
		"the rule tables in docs/invariants.md, which must describe the recorded verdicts",
		"the stability policy in docs/invariants.md: after v1 a recorded verdict may not change",
	]
}

// Every lens-totality verdict is part of what binding guarantees: a lens that
// binds is total, so a verdict that moves can make a published lens fail to
// bind, or let through one that fails at translate (#57 rests on this). The
// corpus records one verdict per rule; a diff there is a contract change, even
// when it fixes a bug. Declared as a kernel to fire on touch, as compat-verdicts
// is (sdboyer/thresh#6).
seams: "lenscheck-verdicts": {
	kind:  "shared-kernel"
	paths: ["internal/lenscheck/testdata/*.txtar"]
	consult: [
		"every lineage already bound: a verdict that moves can make a published lens fail to bind, or admit one that fails at translate",
		"the rule tables in docs/invariants.md (Lens totality), which must describe the recorded verdicts",
		"the stability policy in docs/invariants.md: after v1 a recorded verdict may not change",
		"internal/compat, whose verdicts the copy rule reuses: a compat verdict that moves moves copies with it",
	]
}

// The compiler and the decompiler are inverses on the canonical subset of the
// normal form, and the round-trip fixtures prove it in both directions. A
// change to what either emits or recognizes owes the other the same change.
seams: "compile-roundtrip": {
	kind:  "shared-kernel"
	paths: ["internal/compile/compile.go", "internal/compile/lens.go", "internal/compile/decompile.go", "internal/compile/testdata/roundtrip/*.txtar"]
	consult: [
		"the other direction: what Compile emits, Decompile must recognize, and what Decompile omits, Compile must regenerate",
		"the round-trip fixtures in internal/compile/testdata/roundtrip, which hold both directions' goldens, and the compiler's diagnostics in the authoring vocabulary",
		"the lens language design (#62) and the authoring file format (#61), which the vocabulary and layout follow",
	]
}

// Diagnostic codes are the stable identity of thema's errors: tools, docs links
// and users' scripts match on them, and the playground renders by them. A code's
// meaning never changes; one that stops applying is retired, never reused. Each
// code has a reproducer, whose rendered text is its documentation.
seams: "diag-codes": {
	kind:  "shared-kernel"
	paths: ["diag/codes.go", "diag/testdata/codes/*.txtar", "docs/errors/*.md"]
	consult: [
		"every consumer that matches on a code: the CLI's --diag-format json readers, docs/errors links, the playground; retire and allocate rather than change a code's meaning",
		"the lockstep tests in diag/codes_test.go: one reproducer per code, docs/errors generated from them (THEMA_UPDATE_GOLDEN=1 go test ./diag)",
		"the authoring principles in AGENTS.md (Diagnostics), enforced by internal/diagtest.Lint over every corpus",
		"internal/compile/vocab.go and diagnose.go, which render the same codes in the authoring and normal-form vocabularies",
	]
}

// thema depends on the exact messages the CUE evaluator and loader produce:
// diag/shapes.go recognizes them by format string, and the menagerie records
// every shape with a digest that fails on change. A CUE upgrade that moves the
// digest means re-checking every shape match and every reproducer's rendering.
seams: "cue-error-shapes": {
	kind:  "shared-kernel"
	paths: ["diag/shapes.go", "diag/shapes_*.go", "diag/testdata/menagerie.txtar", "docs/cue-errors.md"]
	consult: [
		"menagerieDigest in diag/menagerie_test.go, updated by hand after reading the regenerated menagerie and docs/cue-errors.md",
		"TestShapesCovered and the unmatchedYet backlog: every recorded format is matched by a shape or listed, so nothing is reported as E0902 unknowingly",
		"the reproducers in diag/testdata/codes, whose details quote CUE's text",
	]
}

// --- shared-kernel: co-owners we can name ---------------------------------------

// thema's own CUE definitions are the kernel both halves stand on: every lineage
// anyone writes unifies with #Lineage, and the Go library binds to these by path.
seams: "lineage-kernel": {
	kind:  "shared-kernel"
	paths: ["lineage.cue", "translate.cue", "lacuna.cue", "traverse.cue"]
	consult: [
		"every lineage that unifies with thema.Lineage",
		"the Go facade (bind.go, lineage.go, surface.go)",
		"Go code that calls thema's CUE pseudofunctions and decodes their output shape (instance.go Translate decodes Translate's out)",
		"bind.go parseVersionKey, parseLensEdge and lensID.String, which mirror the schemas and lenses key formats (versionKey, edgeKey in lineage.cue)",
	]
}

// Go constants that mirror thema's CUE module identity: its path, embedded version
// and language version, and the package identity that scopes thema's hidden fields
// (cue.Hid). They must stay in lockstep with cue.mod/module.cue and the release tag.
seams: "thema-module-identity": {
	kind:  "shared-kernel"
	paths: ["internal/util/modfile.go"]
	consult: [
		"cue.mod/module.cue (lockstep test in internal/util)",
		"the release process, which tags the Go and CUE modules with ThemaModuleVersion",
		"every module.cue thema synthesizes (util.ModuleFile) and every cue.Hid lookup",
	]
}

// The virtual path at which the embedded thema module is presented to the CUE
// loader appears in every error position that points into thema's CUE.
seams: "embedded-module-root": {
	kind:  "shared-kernel"
	paths: ["internal/embedreg/embedreg.go"]
	consult: [
		"users reading error positions (the path is user-facing; diag renders a file under it as thema/<file>)",
		"diag/pos.go, which classifies every position's origin by IsKernelFile, and the Positionless rule in diag: a kernel position is never a primary",
		"golden error output in testdata, diag/testdata and docs/errors",
	]
}

// Go's set of lacuna types mirrors LacunaTypes in thema's CUE; every lacuna a
// translation emits is decoded into it.
seams: "lacuna-types": {
	kind:  "shared-kernel"
	paths: ["lacuna/lacuna.go"]
	consult: [
		"LacunaTypes in lacuna.cue (lockstep test TestLacunaTypesMatchCUE)",
		"instance.go Translate, which decodes the lacunas of every step into these types",
	]
}

// The CUE module config is pure substrate.
seams: "cue-module": {
	kind:    "shared-kernel"
	paths:   ["cue.mod/module.cue"]
	consult: ["everything that evaluates thema's CUE"]
}

// The upstream CUE implementation is the language every lineage is written in.
// Only the CUE dependency line matters, not unrelated go.mod churn.
seams: "cue-dependency": {
	kind:    "shared-kernel"
	paths:   ["go.mod"]
	watch:   ["cuelang.org/go"]
	consult: [
		"every lineage's validity; the Go facade's cue.Value handling",
		"diag/shapes.go, which recognizes CUE's errors by the format string of Msg(); the menagerie (diag/testdata/menagerie.txtar, menagerieDigest in diag/menagerie_test.go) and docs/cue-errors.md record every shape thema depends on, and the digest fails on a change",
		"cmd/thema load.go loadDiag, which recognizes two of the loader's failures by their text, since the loader formats them before thema sees them",
		"Go code that works around evaluator behavior (assignable.go detaches values before Subsume)",
		"golden error output in testdata, and the reproducers in diag/testdata/codes with the docs/errors pages generated from them, which record CUE's wording (as details) and positions",
		"internal/cueview goldens, which record how CUE presents values as syntax; every check built on a view (lens checks in internal/cuetil) depends on them",
		"version IDs (internal/contenthash goldens): the canonical form is built on how CUE exports values, so an upgrade that changes the export changes stored IDs",
		"internal/compile tests, which pin what the evaluator does with positions in AST built for authoring files: that a node's position carries its language version and experiments, and where errors' positions land in thema's pipeline",
	]
}

// Error positions are chosen by telling thema's own CUE apart from the author's
// by path. thema's CUE reaches the evaluator at two paths: embedreg.Root(), the
// embedded module's root, for lineages that import thema, and
// embedreg.RuntimeRoot(), the Runtime's load path, for everything BindLineage
// unifies with thema.Lineage itself and for every pseudofunction call such as
// Translate. Every diag.Pos carries an origin decided by those paths, and
// diagnostics show a file under either as thema/<file>.
seams: "kernel-paths": {
	kind:  "shared-kernel"
	paths: ["runtime.go"]
	consult: [
		"internal/embedreg IsKernel, IsKernelFile, KernelPath and AuthorPos, which recognize thema's CUE only at embedreg.Root() and embedreg.RuntimeRoot(); diag/pos.go, which classifies every position's origin through them, and diag.AuthorPos; every error position bind.go takes through them",
		"internal/compile classify, and the kernel positions in its pipeline goldens",
		"diag/render.go and cmd/thema readSource, which display and read thema's files by those roots",
	]
}

// How CUE presents a value as syntax shifts between versions and surprises often
// (_#def wrappers, generated lets, comprehensions kept or dropped). The goldens
// record each view's output; a diff there is a change every caller of that view
// must be checked against.
seams: "cueview-goldens": {
	kind:  "shared-kernel"
	paths: ["internal/cueview/testdata/*.txtar"]
	consult: [
		"every caller of the view whose output changed (internal/cuetil syntax.go reads Evaluated through cueview.Tree; internal/contenthash builds its canonical form on SelfContained)",
		"the view's doc comment in internal/cueview/view.go, which lists its quirks",
	]
}

// The lens result-field rule: a lens may write (constant or computed) only fields
// its target declares, while data it copies from its input is not checked. One
// rule, enforced at bind (from the lens's syntax) and at translate, from shared
// helpers. The enforcement sites must agree; when they
// didn't, hidden fields were rejected at bind that translate accepted. Field
// origins (copied/constant/computed) will also decide what lenses can compile to.
seams: "lens-result-fields": {
	kind:  "shared-kernel"
	paths: ["internal/cuetil/declared.go", "internal/cuetil/syntax.go", "internal/cueview/tree.go"]
	consult: [
		"bind.go checkLensResults and checkLacunas, and instance.go checkLensResultFields: all must apply the same rule, and report it as E0305",
		"AGENTS.md, Lens results",
		"testdata/cases/lens-* and the construction-independence tests in internal/cuetil",
	]
}

// A version's ID is two digests of canonical forms of its content. IDs are
// stored (envelopes, a registry of record), so the canonical form is co-owned
// with every ID ever computed: a change to it, or to where a version's schema and
// lenses are found, changes IDs and orphans what was recorded against them.
seams: "content-hash": {
	kind:  "shared-kernel"
	paths: ["internal/contenthash/*.go", "internal/contenthash/testdata/*.txtar"]
	consult: [
		"every stored version ID; a change to the golden digests in internal/contenthash/testdata is a change to them",
		"internal/cueview SelfContained and its goldens, which the canonical form is built on",
		"the tip and cut workflow (#65) and lens fixes (#67), which compare schema and lens digests",
	]
}

// The playground's wire format is a contract with a frontend in another
// repository (sdboyer/thema-playground), which reads it through types.ts and
// calls it through thema.wasm's bridge. thresh's published-language kind reads
// only version tuples, so it is declared as a kernel to fire on touch, as
// compat-verdicts is (sdboyer/thresh#6).
seams: "playground-api": {
	kind:  "shared-kernel"
	paths: ["playground/types.go", "playground/types.ts", "playground/session.go", "cmd/thema-wasm/main.go", "cmd/thema-wasm/thema-fs.js"]
	consult: [
		"sdboyer/thema-playground at the release cmd/thema/playground/VERSION pins: a renamed field, operation or bridge function breaks it, and the pin and the frontend move together",
		"playground/types.ts, regenerated from the Go types (THEMA_UPDATE_GOLDEN=1 go test ./playground), and the response goldens in playground/testdata",
		"the release assets the static site fetches by tag: thema.wasm, wasm_exec.js, thema-fs.js, types.ts",
	]
}

// The frontend release the CLI embeds. Bumping it changes what thema serve
// playground serves, and must match the wire format this thema speaks.
seams: "playground-ui-pin": {
	kind:  "shared-kernel"
	paths: ["cmd/thema/playground/VERSION"]
	consult: [
		"the playground-api seam: the pinned frontend must speak this thema's wire format",
		"the release workflow, which runs go generate ./cmd/thema/playground and attaches the bundle's wasm assets",
	]
}

// --- choke-point ------------------------------------------------------------------

// BindLineage is the sole verification choke point (see docs/go-mapping.md).
// Constructing the guarded impl type anywhere else skips its invariants.
seams: bindlineage: {
	kind:   "choke-point"
	paths:  ["*.go"]
	type:   "baseLineage"
	onlyIn: ["BindLineage"]
}

// Configure is the only way thema's CUE module gets served to the loader. Building
// the registry elsewhere could skip the module-file check or the overlay that makes
// its virtual root readable.
seams: "embedded-registry": {
	kind:   "choke-point"
	paths:  ["internal/embedreg/*.go"]
	type:   "registry"
	onlyIn: ["Configure"]
}

// --- declared-set: the Go/CUE partition of responsibility ------------------------

// Go enforces ONLY what CUE cannot yet express. Every Go-side validity check is
// declared here with the reason it is Go-side; an undeclared one is a partition
// violation. As CUE gains expressiveness, rules move into lineage.cue and come
// off this list. (Reasons are first-pass; refine as you reconsider each.)
seams: "go-residual-checks": {
	kind:  "declared-set"
	paths: ["bind.go"]
	funcs: "check*"
	declared: {
		checkGoValidity:       "compat vs declared version delta: needs the compat checker, which closes both schemas and subsumes them in Go; also rejects a major version key with no minors, which struct.MinFields only reports under concrete validation, which abstract schemas never pass"
		checkSchemaVersions:   "versions must be contiguous from 0.0: in CUE a skipped version is a missing key, which is only an incomplete reference (unification could still supply it), so validation never reports it"
		checkExists:           "presence of required lineage parts before deeper checks"
		checkLineageShape:     "structural shape checks on the raw lineage value"
		checkNativeValidity:   "runs the CUE-native validity pass and surfaces its errors"
		checkLensEdges:        "exactly the required lenses exist: needs the bound version list; a missing lens would otherwise surface only when a translation crosses its edge, and an extra one would be silently unused"
		checkSchemaShapes:     "schemas and joinSchema must be structs, and examples valid instances: CUE can constrain kinds, but reports a schema like _ only once data arrives"
		checkLensResults:      "static lens checks with input standing for any source instance: needs each lens paired with its source and target schemas, closedness applied at use, and the declared-fields walker"
		checkLacunas:          "lacuna lists must be enumerable without input, and lacuna field paths declared by the lens's schemas: needs the lineage's schemas, and CUE would only report missing required lacuna fields once data arrives"
		checkUnknownFields:    "unknown lineage/schema/lens fields, and schemas/lenses keys that aren't versions/edges; CUE rejects them too, but positions the error at its guard in thema's CUE, while Go can point at the author's line. Known fields are read from thema's CUE, not duplicated"
		checkLensTotality:     "every CUE lens must map every instance of its source schema to an instance of its target: a quantified claim over instances, which CUE can only evaluate one instance at a time. The walk (internal/lenscheck) reads each lens's syntax, judges copies with the compat checker and bounds through Expr(), and treats what it cannot decide as an error"
		checkSchemaConstructs: "no comprehensions or reference cycles in a schema: CUE evaluates both happily, but no later check (compat, lens results, lacunas) can describe a schema whose fields depend on evaluation or whose paths are unbounded; refused before any of them runs, and reported at the construct"
		checkReferences:       "a schema refers only to its own body and CUE's builtin packages, a lens only to the instance it takes, its own body and those packages: CUE resolves a reference to anything in lexical scope, so a sibling schema, the lineage's name, a helper beside the lineage or another package all evaluate without complaint, and each can change a published schema's meaning without its text changing. Read from each schema's and lens's syntax, because lets, pending comprehensions and index expressions hide references from the value API"
	}
}

// thema's CUE API is built from regular fields, not definitions: definitions close
// recursively, and authors' schemas flow through these structures, so a definition
// would impose closedness on every schema. Each definition is an exception that
// must say why it can't be a regular field.
seams: "kernel-definitions": {
	kind:  "declared-set"
	paths: ["lineage.cue", "translate.cue", "lacuna.cue", "traverse.cue"]
	defs:  "*#*"
	declared: {
		"#LatestVersion": "lets a user's package reference a computed member without it appearing in exported data; holds a version, never a schema"
	}
}

// Each view of a cue.Value as syntax is a deliberate, documented choice of
// options with known quirks. Variants made ad hoc at call sites are how those
// quirks get rediscovered; a new view must say why none of these will do.
seams: "cue-views": {
	kind:  "declared-set"
	paths: ["internal/cueview/view.go"]
	funcs: "[A-Z]*"
	declared: {
		Written:       "the author's text with positions, per conjunct; for pointing errors at it, never for deciding anything"
		Evaluated:     "the value as evaluated, not final; depends only on the value; the basis for structural checks"
		Raw:           "the names the value's syntax refers to, left dangling"
		Final:         "defaults taken, closed; what a user sees by default, without requiring concreteness"
		Data:          "concrete data, failing on anything else, which Syntax alone would hide as _|_ comments"
		Documented:    "Evaluated with doc comments, for output people read"
		Text:          "formats syntax from any view as CUE source"
		SelfContained: "Evaluated with imported packages inlined, for questions about a value's content that must not stop at an import path, such as hashing it"
	}
}

// --- references-resolve: the CUE<->Go binding -------------------------------------

// Go reaches into CUE by literal path (cue.Str / cue.Def / cue.Hid). A rename in
// the CUE breaks those bindings in Go files the change never touched.
seams: "cue-go-binding": {
	kind: "references-resolve"
	go:   ["*.go"]
	cue:  ["*.cue"]
}
