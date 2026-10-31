package capnp

import (
	"cuelang.org/go/cue"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/diag"
)

// Codes is the diagnostic code of each rule. Each rule has one code and no two
// rules share one; TestRuleCodes reads the rules from this package's source
// and keeps the two in step. Rejections are E0710 up, Unknowns E0730 up.
var Codes = map[Rule]diag.Code{
	RuleIntUnbounded:               diag.E0710,
	RuleFloat:                      diag.E0711,
	RuleTop:                        diag.E0712,
	RuleDisjunctionUndiscriminated: diag.E0713,
	RuleNameCollision:              diag.E0714,

	RuleUnknownPatternLabels:     diag.E0730,
	RuleUnknownListHeterogeneous: diag.E0731,
}

// ruleText is what each rule's diagnostic says beyond the finding: the
// headline, given the construct's name in the author's terms, and what the
// author can do (docs/capnp.md, the rule tables).
var ruleText = map[Rule]struct {
	headline string
	help     string
}{
	RuleIntUnbounded:               {"%s has no bounds that fit a Cap'n Proto integer", "bound it within 64 bits, such as int32, uint8 or int & >=0 & <=100"},
	RuleFloat:                      {"%s may be a float, which compiling to Cap'n Proto does not carry", "use an integer or a string: floats are outside the compiled target"},
	RuleTop:                        {"%s accepts any value, which has no Cap'n Proto type", "declare the type"},
	RuleDisjunctionUndiscriminated: {"%s has no discriminant for a Cap'n Proto union", "give struct alternatives a tag field with a distinct constant in each, make the alternatives of distinct kinds, or make them all constants"},
	RuleNameCollision:              {"%s has the same Cap'n Proto name as another name in its struct", "rename one of them, so that the names differ once underscores are dropped"},
	RuleUnknownPatternLabels:       {"%s has pattern constraints the Cap'n Proto compiler does not carry", "declare the fields, or move the map to a field of its own"},
	RuleUnknownListHeterogeneous:   {"%s is a closed list whose elements have no one Cap'n Proto type", "use a struct: Cap'n Proto has no tuple"},
}

// Vocabulary sets the vocabulary diagnostics name the lineage's constructs
// in: that of the form the author wrote. A bound lineage does not report how
// it was written, so a caller that compiled it from version files passes
// their vocabulary; the default is the normal form's.
func Vocabulary(v diag.Vocabulary) Option {
	return func(c *config) { c.vocab = v }
}

// Unwrap returns the findings as diagnostics, one [diag.Error] each, in a
// cue/errors list, so that [diag.Diagnostics] and [diag.From] read a Result
// as they read any error thema reports.
func (r Result) Unwrap() error {
	errs := make([]error, len(r.Findings))
	for i, f := range r.Findings {
		errs[i] = f.diagnostic(r.vocab)
	}
	return diag.Join(errs...)
}

// diagnostic describes f: the headline states the rule about the construct
// in the author's terms, the note is the finding's own sentence, and the help
// says what the author can do.
func (f Finding) diagnostic(vocab diag.Vocabulary) *diag.Error {
	if vocab == nil {
		vocab = diag.NormalForm
	}
	addr := diag.Address{Kind: diag.AddrSchema, Version: diag.Version(f.Version)}
	if len(f.Path.Selectors()) > 0 {
		addr = diag.Address{Kind: diag.AddrSchemaField, Version: diag.Version(f.Version), Field: pathSegments(f.Path)}
	}
	term := vocab.Term(addr)
	text := ruleText[f.Rule]
	b := diag.New(Codes[f.Rule]).Headlinef(text.headline, term).Addr(addr)
	// As diag.B.AtValue: a position in thema's own CUE is a note, never the
	// primary.
	if p := diag.PosOf(f.Pos); p != nil && p.Origin == diag.OriginThema {
		b.Note(f.Pos, "in thema's own CUE")
	} else {
		b.At(f.Pos)
	}
	help := text.help
	if f.Help != "" {
		help = f.Help
	}
	return b.Notef(token.NoPos, "%s %s (Cap'n Proto rule %s)", term, diag.RewriteFor(vocab, addr, f.Detail), f.Rule).
		Help(help).
		Err()
}

// pathSegments is p's selectors as an Address's field: labels unquoted.
func pathSegments(p cue.Path) []string {
	sels := p.Selectors()
	out := make([]string, len(sels))
	for i, s := range sels {
		if s.IsString() {
			out[i] = s.Unquoted()
		} else {
			out[i] = s.String()
		}
	}
	return out
}
