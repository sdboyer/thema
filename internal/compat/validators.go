package compat

import (
	"strings"

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
	// A multiple of the prior n is a multiple of the new n when the prior n
	// is itself a multiple of the new n.
	"math.MultipleOf": {implied: intArgMultiple},
	// Affixes and substrings: the prior string must carry the new one.
	"strings.HasPrefix": {implied: strArg(strings.HasPrefix)},
	"strings.HasSuffix": {implied: strArg(strings.HasSuffix)},
	"strings.Contains":  {implied: strArg(strings.Contains)},
	// Character sets: the new set must have every rune of the prior one.
	"strings.ContainsAny": {implied: strArg(runesSubset)},
	// Argument-free validators: implied only by themselves.
	"list.UniqueItems":     {implied: never},
	"list.IsSortedStrings": {implied: never},
	"math.Signbit":         {implied: never},
	"strconv.IsPrint":      {implied: never},
	"strconv.IsGraphic":    {implied: never},
	"regexp.Valid":         {implied: never},
	"encoding/json.Valid":  {implied: never},
	// Validators whose argument is the requirement: a different element or
	// comparator is a different requirement in both directions.
	"list.Contains": {implied: never},
	"list.IsSorted": {implied: never},
	// Format validators: a different argument is a different format.
	"time.Time":                     {implied: never},
	"time.Format":                   {implied: never},
	"time.Duration":                 {implied: never},
	"net.IP":                        {implied: never},
	"net.IPv4":                      {implied: never},
	"net.IPv6":                      {implied: never},
	"net.IPCIDR":                    {implied: never},
	"net.URL":                       {implied: never},
	"net.AbsURL":                    {implied: never},
	"net.FQDN":                      {implied: never},
	"net.GlobalUnicastIP":           {implied: never},
	"net.InterfaceLocalMulticastIP": {implied: never},
	"net.LinkLocalMulticastIP":      {implied: never},
	"net.LinkLocalUnicastIP":        {implied: never},
	"net.LoopbackIP":                {implied: never},
	"net.MulticastIP":               {implied: never},
	"net.UnspecifiedIP":             {implied: never},
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

// intArgMultiple reports whether old's n is a multiple of new's: every
// multiple of old's n is then a multiple of new's.
func intArgMultiple(newArgs, oldArgs []cue.Value) bool {
	n, ok1 := intArg(newArgs)
	o, ok2 := intArg(oldArgs)
	return ok1 && ok2 && n != 0 && o%n == 0
}

func str(args []cue.Value) (string, bool) {
	if len(args) != 1 {
		return "", false
	}
	s, err := args[0].String()
	return s, err == nil
}

// strArg lifts a relation on strings to a rule: the new call is implied when
// carries(prior, next) holds for the two string arguments.
func strArg(carries func(prior, next string) bool) func(newArgs, oldArgs []cue.Value) bool {
	return func(newArgs, oldArgs []cue.Value) bool {
		n, ok1 := str(newArgs)
		o, ok2 := str(oldArgs)
		return ok1 && ok2 && carries(o, n)
	}
}

// runesSubset reports whether every rune of prior is in next.
func runesSubset(prior, next string) bool {
	for _, r := range prior {
		if !strings.ContainsRune(next, r) {
			return false
		}
	}
	return true
}
