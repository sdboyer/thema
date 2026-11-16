// Package omitted declares a Go interface another embeds, for tsapi's tests:
// a wire format that omits the first must omit the second too.
package omitted

// Behavior is behavior, which a wire format omits.
type Behavior interface {
	Do()
}

// More is Behavior, and more.
type More interface {
	Behavior
	Again()
}
