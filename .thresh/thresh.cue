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
		"users reading error positions (the path is user-facing)",
		"golden error output in testdata",
	]
}

// Go's set of lacuna types mirrors LacunaTypes in thema's CUE; Go lenses' lacunas
// are checked against it.
seams: "lacuna-types": {
	kind:  "shared-kernel"
	paths: ["lacuna.go"]
	consult: [
		"LacunaTypes in lacuna.cue (lockstep test TestLacunaTypesMatchCUE)",
		"Go lenses, whose lacunas are validated against these types at translation",
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
		"Go code that parses CUE error shapes (validate.go mungeValidateErr matches on Msg() argument counts and types)",
		"Go code that works around evaluator behavior (assignable.go detaches values before Subsume)",
		"golden error output in testdata, which records CUE's error wording and positions",
		"internal/cueview goldens, which record how CUE presents values as syntax; every check built on a view (lens checks in internal/cuetil) depends on them",
		"version IDs (internal/contenthash goldens): the canonical form is built on how CUE exports values, so an upgrade that changes the export changes stored IDs",
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
// rule, enforced at bind (CUE lenses, from syntax) and at translate (CUE and Go
// lenses), from shared helpers. The enforcement sites must agree; when they
// didn't, hidden fields were rejected at bind that translate accepted. Field
// origins (copied/constant/computed) will also decide what lenses can compile to.
seams: "lens-result-fields": {
	kind:  "shared-kernel"
	paths: ["internal/cuetil/declared.go", "internal/cuetil/syntax.go", "internal/cueview/tree.go"]
	consult: [
		"bind.go checkGoLensCompleteness and checkLacunas, and instance.go Translate (Go lenses) and checkLensResultFields (CUE lenses): all must apply the same rule",
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
		checkGoValidity:        "compat vs declared version delta — CUE can't compute version counts natively (lineage.cue:118)"
		checkSchemasOrder:      "schema version ordering against the sorted version list"
		checkExists:            "presence of required lineage parts before deeper checks"
		checkLineageShape:      "structural shape checks on the raw lineage value"
		checkNativeValidity:    "runs the CUE-native validity pass and surfaces its errors"
		checkLensesOrder:       "lens from/to ordering constraints"
		checkLensCompleteness:   "exactly the required lenses exist (Go or CUE): needs the bound version list, and Translate selects CUE lenses by position, so a missing or extra lens silently shifts the rest"
		checkSchemaShapes:       "schemas and joinSchema must be structs, and examples valid instances: CUE can constrain kinds, but reports a schema like _ only once data arrives"
		checkLensResults:        "static lens checks with input standing for any source instance: needs each lens paired with its source and target schemas, closedness applied at use, and the declared-fields walker"
		checkLacunas:            "lacuna lists must be enumerable without input, and lacuna field paths declared by the lens's schemas: needs the lineage's schemas, and CUE would only report missing required lacuna fields once data arrives"
		checkUnknownFields:      "unknown lineage/schema/lens fields; CUE rejects them too, but positions the error at its guard in thema's CUE, while Go can point at the author's line. Known fields are read from thema's CUE, not duplicated"
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
