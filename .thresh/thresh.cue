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

// --- shared-kernel: co-owners we can name ---------------------------------------

// thema's own CUE definitions are the kernel both halves stand on: every lineage
// anyone writes unifies with #Lineage, and the Go library binds to these by path.
seams: "lineage-kernel": {
	kind:  "shared-kernel"
	paths: ["lineage.cue", "translate.cue", "lacuna.cue", "traverse.cue"]
	consult: [
		"every lineage that unifies with thema.#Lineage",
		"the Go facade (bind.go, lineage.go, surface.go)",
		"Go code that calls thema's CUE pseudofunctions and decodes their output shape (instance.go Translate decodes #Translate's out)",
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
		checkGoLensCompleteness: "every required lens exists for the version set"
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
