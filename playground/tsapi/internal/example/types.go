// Package example is a downstream session API whose wire types embed and
// refer to thema's, as tsapi's tests see one.
package example

import (
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/lacuna"
	"github.com/sdboyer/thema/playground"
)

// CheckRequest checks one file.
type CheckRequest struct {
	File playground.File `json:"file"`
}

// CheckResponse is a check's result, with thema's diagnostics.
type CheckResponse struct {
	Diagnostics []diag.Diagnostic          `json:"diagnostics"`
	At          *diag.Pos                  `json:"at,omitempty"`
	Lacunas     *lacuna.TranslationLacunas `json:"lacunas,omitempty"`
	Severity    diag.Severity              `json:"severity"`
}

// Annotated is a diagnostic with the program's own note on it. JSON inlines
// an embedded struct's fields; tygo's extends option says so.
type Annotated struct {
	diag.Diagnostic `tstype:",extends"`
	Remark          string `json:"remark"`
}
