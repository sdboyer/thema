// Package compile is where thema's authoring form will be compiled to the
// normal form, the thema.Lineage struct. The compiler builds CUE AST, not text,
// and positions each node in the authoring source it came from.
//
// The compiler doesn't exist yet. For now the package holds only tests that
// pin what the CUE evaluator and thema's own checks do with such positions. The
// compiler's design depends on that behavior, so these tests should fail loudly
// if a CUE upgrade or a change to thema's checks moves it.
package compile
