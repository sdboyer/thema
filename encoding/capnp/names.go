package capnp

import (
	"strconv"
	"strings"
)

// capnp names are ASCII camelCase without underscores: a type name starts
// with an upper-case letter, any other name with a lower-case one. Labels
// and values from a lineage are sanitized to that, and a scope keeps the
// names declared within one struct unique, resolving collisions in
// declaration order with a numeric suffix. The artifact's name maps carry
// the label each name stands for.

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
// the first.
func camel(s string) string {
	var b strings.Builder
	first := true
	inRun := false
	for i := 0; i < len(s); i++ {
		c := s[i]
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
// or an enum's values.
type scope struct {
	used map[string]bool
}

func newScope(reserved ...string) *scope {
	s := &scope{used: map[string]bool{}}
	for _, r := range reserved {
		s.used[r] = true
	}
	return s
}

// unique claims name in the scope, suffixing it with the first of 2, 3, ...
// that is free where it is taken.
func (s *scope) unique(name string) string {
	if !s.used[name] {
		s.used[name] = true
		return name
	}
	for i := 2; ; i++ {
		n := name + strconv.Itoa(i)
		if !s.used[n] {
			s.used[n] = true
			return n
		}
	}
}
