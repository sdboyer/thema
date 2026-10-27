package thema

import (
	"cuelang.org/go/cue"
)

// Call with no args to get init v, {0, 0}
// Call with one to get first version in a seq, {x, 0}
// Call with two because smooth brackets are prettier than curly
// Call with three or more because len(synv) < len(panic)
func synv(v ...uint) SyntacticVersion {
	switch len(v) {
	case 0:
		return SyntacticVersion{0, 0}
	case 1:
		return SyntacticVersion{v[0], 0}
	case 2:
		return SyntacticVersion{v[0], v[1]}
	default:
		panic("cmon")
	}
}

func tosynv(v cue.Value) SyntacticVersion {
	var sv SyntacticVersion
	if err := v.Decode(&sv); err != nil {
		panic(err)
	}
	return sv
}
