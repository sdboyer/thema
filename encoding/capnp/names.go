package capnp

import (
	"strings"
)

// capnp names are ASCII camelCase without underscores: a type name starts
// with an upper-case letter, any other name with a lower-case one. Labels
// and values from a lineage are sanitized to that: underscores are dropped
// (ship_v2 is shipv2, #89), other characters outside [A-Za-z0-9] separate
// words, which are joined in camel case. Two labels can then map to one
// name; a scope detects it, and the compiler reports it (name-collision)
// rather than emitting a duplicate. The artifact's name maps carry the label
// each name stands for.

// fieldName makes a non-type name from s.
func fieldName(s string) string {
	n := camel(s)
	if n == "" {
		n = "empty"
	}
	if n[0] >= '0' && n[0] <= '9' {
		n = "v" + n
	}
	return lowerFirst(n)
}

// typeName makes a type name from s.
func typeName(s string) string {
	n := camel(s)
	if n == "" {
		n = "Empty"
	}
	if n[0] >= '0' && n[0] <= '9' {
		n = "V" + n
	}
	return upperFirst(n)
}

// camel joins the alphanumeric ASCII runs of s, capitalizing each run after
// the first. An underscore is dropped without ending a run.
func camel(s string) string {
	var b strings.Builder
	first := true
	inRun := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' {
			continue
		}
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !alnum {
			inRun = false
			continue
		}
		if !inRun && !first && c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		b.WriteByte(c)
		inRun, first = true, false
	}
	return b.String()
}

func lowerFirst(s string) string {
	if s == "" || s[0] < 'A' || s[0] > 'Z' {
		return s
	}
	return string(s[0]+('a'-'A')) + s[1:]
}

func upperFirst(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-('a'-'A')) + s[1:]
}

// A scope is the names declared in one struct: its fields, its nested types,
// or an enum's values. Each name records what it stands for (a label, a
// value), so that one thing claiming its name again is not a collision.
type scope struct {
	used map[string]string
}

func newScope() *scope {
	return &scope{used: map[string]string{}}
}

// claim claims name for what in the scope. Where another thing holds the name
// already, it returns that thing and false; the caller reports the collision.
func (s *scope) claim(name, what string) (string, bool) {
	if have, ok := s.used[name]; ok && have != what {
		return have, false
	}
	s.used[name] = what
	return "", true
}

// reserved names a field the compiler adds to every schema struct, for
// undeclared data. A label whose name is reserved is a name-collision.
const reserved = "extras"
