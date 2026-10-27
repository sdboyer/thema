package playground

import (
	"encoding/json"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/lacuna"
)

// The request and response types of every operation. They are the
// playground's wire format: the wasm bridge passes them as JSON, and
// types.ts, generated from this file, is how the frontend sees them. Versions
// are strings, "0.0"; only lacunas keep thema's [major, minor] pairs.

// File is one named text: a version file of the authoring form, relative to
// the lineage's directory (1.0.thema, history/0.0.thema), or a normal-form
// .cue file.
type File struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

// LineageInput is a lineage as the author wrote it, in exactly one of the two
// forms: the authoring form (Name and Files) or one normal-form file (Normal,
// with Path naming the lineage within it when it is not the file's root).
type LineageInput struct {
	// Name is the lineage's name in the authoring form, which the authoring
	// form takes from its directory.
	Name  string `json:"name,omitempty"`
	Files []File `json:"files,omitempty"`
	// Normal is a normal-form file. It may import github.com/sdboyer/thema,
	// and nothing else.
	Normal *File `json:"normal,omitempty"`
	// Path is the CUE path of the lineage within Normal, such as "lin" or
	// "rename.l". Empty means the file's root.
	Path string `json:"path,omitempty"`
}

// DataInput is one data document.
type DataInput struct {
	// Filename names the document in diagnostics. It must not end in .cue.
	Filename string `json:"filename"`
	Text     string `json:"text"`
	// Format is "json" or "yaml". Empty detects it: JSON first, then YAML.
	Format string `json:"format,omitempty"`
}

// Result is what every response carries: whether the operation succeeded,
// and everything thema had to say about it. OK is false exactly when some
// diagnostic is an error.
type Result struct {
	OK          bool              `json:"ok"`
	Diagnostics []diag.Diagnostic `json:"diagnostics"`
}

// InitResponse describes the playground: the versions it was built from and
// the operations it answers.
type InitResponse struct {
	Result       `tstype:",extends"`
	ThemaVersion string   `json:"themaVersion"`
	CUEVersion   string   `json:"cueVersion"`
	Ops          []string `json:"ops"`
}

// BindRequest binds a lineage.
type BindRequest struct {
	Lineage LineageInput `json:"lineage"`
}

// BindResponse is a bound lineage: its name, its versions in order, and its
// normal form as text (for the authoring form, what the files compile to).
type BindResponse struct {
	Result   `tstype:",extends"`
	Name     string   `json:"name,omitempty"`
	Versions []string `json:"versions,omitempty"`
	Normal   string   `json:"normal,omitempty"`
}

// ValidateRequest validates a data document against one schema. An empty
// Version means the latest.
type ValidateRequest struct {
	Lineage LineageInput `json:"lineage"`
	Data    DataInput    `json:"data"`
	Version string       `json:"version,omitempty"`
}

// ValidateResponse names the version the data was validated against.
type ValidateResponse struct {
	Result  `tstype:",extends"`
	Version string `json:"version,omitempty"`
}

// ValidateAnyRequest searches the lineage for a schema the data is an
// instance of.
type ValidateAnyRequest struct {
	Lineage LineageInput `json:"lineage"`
	Data    DataInput    `json:"data"`
}

// VersionDiagnostics is what one schema said about the data.
type VersionDiagnostics struct {
	Version     string            `json:"version"`
	Diagnostics []diag.Diagnostic `json:"diagnostics"`
}

// ValidateAnyResponse names the version that accepted the data, if one did,
// and what every schema said about it, so that a failure shows why each
// version refused it.
type ValidateAnyResponse struct {
	Result     `tstype:",extends"`
	Version    string               `json:"version,omitempty"`
	PerVersion []VersionDiagnostics `json:"perVersion"`
}

// TranslateRequest translates a data document from one version to another.
// An empty From searches the lineage for the data's schema.
type TranslateRequest struct {
	Lineage LineageInput `json:"lineage"`
	Data    DataInput    `json:"data"`
	From    string       `json:"from,omitempty"`
	To      string       `json:"to"`
}

// TranslateResponse is the translated instance, as JSON, and the lacunas each
// step emitted.
type TranslateResponse struct {
	Result  `tstype:",extends"`
	From    string                    `json:"from,omitempty"`
	To      string                    `json:"to,omitempty"`
	Output  json.RawMessage           `json:"output,omitempty"`
	Lacunas lacuna.TranslationLacunas `json:"lacunas,omitempty"`
}

// HydrateRequest fills a data document with its schema's defaults
// (Hydrate), or removes the values its defaults imply (Dehydrate). An empty
// Version searches the lineage for the data's schema.
type HydrateRequest struct {
	Lineage LineageInput `json:"lineage"`
	Data    DataInput    `json:"data"`
	Version string       `json:"version,omitempty"`
}

// HydrateResponse is the document after the operation, and whether it
// changed.
type HydrateResponse struct {
	Result  `tstype:",extends"`
	Version string          `json:"version,omitempty"`
	Output  json.RawMessage `json:"output,omitempty"`
	Changed bool            `json:"changed"`
}

// CodegenRequest generates code from one schema. An empty Version means the
// latest. Format is "json" or "yaml" for OpenAPI and JSON Schema, and is
// ignored for TypeScript.
type CodegenRequest struct {
	Lineage LineageInput `json:"lineage"`
	Version string       `json:"version,omitempty"`
	Format  string       `json:"format,omitempty"`
}

// CodegenResponse is the generated text.
type CodegenResponse struct {
	Result  `tstype:",extends"`
	Version string `json:"version,omitempty"`
	Output  string `json:"output,omitempty"`
}

// CompileRequest compiles authoring files to the normal form.
type CompileRequest struct {
	Name  string `json:"name"`
	Files []File `json:"files"`
}

// CompileResponse is the normal form as text.
type CompileResponse struct {
	Result `tstype:",extends"`
	Normal string `json:"normal,omitempty"`
}

// DecompileRequest recovers the authoring files of a normal-form lineage.
type DecompileRequest struct {
	Normal File   `json:"normal"`
	Path   string `json:"path,omitempty"`
}

// DecompileResponse is the lineage's name and its version files.
type DecompileResponse struct {
	Result `tstype:",extends"`
	Name   string `json:"name,omitempty"`
	Files  []File `json:"files,omitempty"`
}

// FormatRequest lays out one version file in the authoring form's layout.
type FormatRequest struct {
	File File `json:"file"`
}

// FormatResponse is the formatted text.
type FormatResponse struct {
	Result `tstype:",extends"`
	Text   string `json:"text,omitempty"`
}

// ExampleInfo describes one example the playground offers.
type ExampleInfo struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Summary  string `json:"summary"`
	// Kind is the form the example is written in: "authoring" or "normal".
	Kind string `json:"kind"`
	// Binds says whether the lineage binds; an example of an error does not.
	Binds bool `json:"binds"`
}

// ListExamplesResponse lists the examples.
type ListExamplesResponse struct {
	Result   `tstype:",extends"`
	Examples []ExampleInfo `json:"examples"`
}

// GetExampleRequest asks for one example by ID.
type GetExampleRequest struct {
	ID string `json:"id"`
}

// GetExampleResponse is an example's lineage and data documents.
type GetExampleResponse struct {
	Result  `tstype:",extends"`
	Example ExampleInfo  `json:"example"`
	Lineage LineageInput `json:"lineage"`
	Data    []DataInput  `json:"data"`
}
