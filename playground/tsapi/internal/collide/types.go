// Package collide declares a type of the same name as one of thema's that it
// also refers to.
package collide

import "github.com/sdboyer/thema/diag"

// Diagnostic is the program's own diagnostic.
type Diagnostic struct {
	Inner diag.Diagnostic `json:"inner"`
}
