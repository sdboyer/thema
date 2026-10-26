package compat

import (
	"cuelang.org/go/cue"
)

// validatorRule says when a validator call on new is implied by a call to the
// same validator on old, with the given arguments. A validator in this table
// that new adds without any counterpart on old is a narrowing; one that is not
// in the table is Unknown unless the calls are identical.
type validatorRule struct {
	implied func(newArgs, oldArgs []cue.Value) bool
}

// validatorTable holds the builtins the checker has rules for. Every entry
// restricts the values it is applied to, so adding one narrows.
var validatorTable = map[string]validatorRule{
	// Lower bounds: new's bound must not exceed old's.
	"strings.MinRunes": {implied: intArgLE},
	"list.MinItems":    {implied: intArgLE},
	"struct.MinFields": {implied: intArgLE},
	// Upper bounds: new's bound must not fall below old's.
	"strings.MaxRunes": {implied: intArgGE},
	"list.MaxItems":    {implied: intArgGE},
	"struct.MaxFields": {implied: intArgGE},
	// Argument-free validators: implied only by themselves.
	"list.UniqueItems": {implied: never},
	// Format validators: a different argument is a different format.
	"time.Time":     {implied: never},
	"time.Format":   {implied: never},
	"time.Duration": {implied: never},
	"net.IP":        {implied: never},
	"net.IPv4":      {implied: never},
	"net.IPv6":      {implied: never},
}

func never([]cue.Value, []cue.Value) bool { return false }

func intArg(args []cue.Value) (int64, bool) {
	if len(args) != 1 {
		return 0, false
	}
	n, err := args[0].Int64()
	return n, err == nil
}

func intArgLE(newArgs, oldArgs []cue.Value) bool {
	n, ok1 := intArg(newArgs)
	o, ok2 := intArg(oldArgs)
	return ok1 && ok2 && n <= o
}

func intArgGE(newArgs, oldArgs []cue.Value) bool {
	n, ok1 := intArg(newArgs)
	o, ok2 := intArg(oldArgs)
	return ok1 && ok2 && n >= o
}
