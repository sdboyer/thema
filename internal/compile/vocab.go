package compile

import (
	"fmt"
	"regexp"
	"strings"

	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compat"
)

// Vocabulary is the authoring form's: diagnostics about the compiled lineage
// name version files, the ForwardLens or BackwardLens in them, and the lens
// parameters (prev, self) the author wrote, rather than the normal form's schemas,
// lenses, input and result. Pass it to thema.BindLineage as
// thema.WithVocabulary.
func Vocabulary(c *Compiled) diag.Vocabulary {
	return vocab{paths: c.paths, tip: c.provisional}
}

type vocab struct {
	paths map[thema.SyntacticVersion]string
	// tip is the version the tip was compiled as, on the development path.
	tip *thema.SyntacticVersion
}

// param is the name the author gives an instance of ver, one end of a's
// lens, in it: self for the newer version, whose file holds the lens, and
// prev for the older.
func (v vocab) param(a diag.Address, ver diag.Version) string {
	newer := a.To
	if thema.SyntacticVersion(a.To).Less(thema.SyntacticVersion(a.From)) {
		newer = a.From
	}
	if ver == newer {
		return paramSelf
	}
	return paramPrev
}

func (v vocab) file(ver diag.Version) string {
	if p, ok := v.paths[thema.SyntacticVersion(ver)]; ok {
		return p
	}
	return ver.String() + Ext
}

// name is version ver as messages name it: v0.1, or the tip.
func (v vocab) name(ver diag.Version) string {
	if v.tip != nil && *v.tip == thema.SyntacticVersion(ver) {
		return TipName
	}
	return vname(thema.SyntacticVersion(ver))
}

// lensName names a's lens by the version that declares it, as lens says:
// the ForwardLens of v1.0.
func (v vocab) lensName(a diag.Address) string {
	if thema.SyntacticVersion(a.From).Less(thema.SyntacticVersion(a.To)) {
		return fmt.Sprintf("the %s of %s", fieldForward, v.name(a.To))
	}
	return fmt.Sprintf("the %s of %s", fieldBackward, v.name(a.From))
}

// lens names the lens block and the file it is in: a lens lives in the newer
// version's file, as its ForwardLens (from the predecessor) or BackwardLens (to
// it).
func (v vocab) lens(a diag.Address) (file, block string) {
	if thema.SyntacticVersion(a.From).Less(thema.SyntacticVersion(a.To)) {
		return v.file(a.To), fieldForward
	}
	return v.file(a.From), fieldBackward
}

func (v vocab) Path(a diag.Address) []string {
	field := a.Field
	switch a.Kind {
	case diag.AddrLineage:
		return nil
	case diag.AddrName, diag.AddrJoinSchema:
		return diag.NormalForm.Path(a)
	case diag.AddrSchema:
		return []string{v.file(a.Version), fieldSchema}
	case diag.AddrSchemaField:
		return append([]string{v.file(a.Version), fieldSchema}, field...)
	case diag.AddrExample:
		return []string{v.file(a.Version), "examples", a.Name}
	case diag.AddrLens, diag.AddrStep:
		file, block := v.lens(a)
		return []string{file, block}
	case diag.AddrLensInput:
		file, block := v.lens(a)
		return []string{file, block, v.param(a, a.From)}
	case diag.AddrLensResult:
		file, block := v.lens(a)
		return []string{file, block, v.param(a, a.To)}
	case diag.AddrLensResultField:
		file, block := v.lens(a)
		return append([]string{file, block, v.param(a, a.To)}, field...)
	case diag.AddrLacuna:
		file, block := v.lens(a)
		return []string{file, block, fieldLacunas}
	case diag.AddrLacunaField:
		file, block := v.lens(a)
		return []string{file, block, fieldLacunas, a.Name}
	case diag.AddrData:
		return field
	}
	return nil
}

func (v vocab) Term(a diag.Address) string {
	field := strings.Join(a.Field, ".")
	switch a.Kind {
	case diag.AddrSchema:
		return "the schema of " + v.name(a.Version)
	case diag.AddrSchemaField:
		return field + " in " + v.name(a.Version)
	case diag.AddrExample:
		return "example " + a.Name + " of " + v.name(a.Version)
	case diag.AddrLens, diag.AddrStep:
		return v.lensName(a)
	case diag.AddrLensInput:
		return fmt.Sprintf("%s, the input of %s", v.param(a, a.From), v.lensName(a))
	case diag.AddrLensResult:
		return "the body of " + v.lensName(a)
	case diag.AddrLensResultField:
		return field + " in " + v.lensName(a)
	case diag.AddrLacuna:
		return "a lacuna of " + v.lensName(a)
	case diag.AddrLacunaField:
		return a.Name + " of a lacuna of " + v.lensName(a)
	}
	return diag.NormalForm.Term(a)
}

// Rewrite renders a checker's sentence about a lens in the author's terms:
// the normal form's input is the instance the author's lens takes, prev or
// self, "the target schema" is the target version, and an expression quoted
// from the normal form names the instances by their versions, v1_0.a, where
// the author wrote self.a.
func (v vocab) Rewrite(a diag.Address, sentence string) string {
	if !lensAddr(a) {
		return sentence
	}
	param := v.param(a, a.From)
	r := strings.NewReplacer(
		"input.", param+".",
		"an input field", "a field of "+param,
		"the input", param,
		"the source schema", v.name(a.From),
		"the target schema", v.name(a.To),
	)
	names := map[string]string{
		paramName(thema.SyntacticVersion(a.From)): param,
		paramName(thema.SyntacticVersion(a.To)):   v.param(a, a.To),
	}
	return renameRoots(r.Replace(sentence), names)
}

// renameRoots renames, in an expression quoted in s, each path rooted at an
// instance of a lens by its name in to: v0_0.a and v0_0[0] are paths into
// the instance, while x.v0_0 selects a field of that name, and v0_0 alone,
// in a sentence, names the field the message is about.
func renameRoots(s string, to map[string]string) string {
	var b strings.Builder
	last := 0
	for _, m := range normalParam.FindAllStringIndex(s, -1) {
		name, ok := to[s[m[0]:m[1]]]
		if !ok || (m[0] > 0 && s[m[0]-1] == '.') || m[1] == len(s) || (s[m[1]] != '.' && s[m[1]] != '[') {
			continue
		}
		b.WriteString(s[last:m[0]])
		b.WriteString(name)
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// normalParam matches a name the normal form gives a lens's instance, v1_0.
var normalParam = regexp.MustCompile(`\bv(0|[1-9][0-9]*)_(0|[1-9][0-9]*)\b`)

// RewriteHelp is Rewrite for a line of help about a lens. Help written in the
// normal form's syntax, such as a presence test to write a field under, is not
// given: the authoring form has no way to write one, and tests presence
// itself wherever a mapping or a when reads a field the source may omit.
func (v vocab) RewriteHelp(a diag.Address, help string) (string, bool) {
	if lensAddr(a) && strings.Contains(help, "_|_") {
		return "", false
	}
	return v.Rewrite(a, help), true
}

// lensAddr reports whether a is in a lens, where a checker's sentences use the
// normal form's terms.
func lensAddr(a diag.Address) bool {
	switch a.Kind {
	case diag.AddrLens, diag.AddrLensInput, diag.AddrLensResult, diag.AddrLensResultField, diag.AddrLacuna, diag.AddrLacunaField, diag.AddrStep:
		return true
	}
	return false
}

// historyMismatch reports curr, a version under history/, whose number its
// compatibility with prev, the version before it, contradicts: numbered minor
// and not backward compatible, or numbered major and backward compatible. cut
// numbers a version by this same check, so since curr was cut one of the two
// files has changed, or the checker has, or they were not cut at all, and no
// run of the check can tell which. The diagnostic names both versions, with
// each finding where it is.
func historyMismatch(res compat.Result, prev, curr *version) *diag.Error {
	b := diag.New(diag.E0207).At(curr.header())
	if curr.v[1] == 0 {
		b.Headlinef("immutability violation: %s is backward compatible with %s", curr.name(), prev.name()).
			Notef(token.NoPos, "a version that accepts all data the one before it accepts is a minor version, and %s is a major version", curr.name())
	} else {
		verb := "is not backward compatible with"
		if res.Verdict() == compat.Unknown {
			verb = "cannot be proven backward compatible with"
		}
		b.Headlinef("immutability violation: %s %s %s", curr.name(), verb, prev.name()).
			Notef(token.NoPos, "%s is a minor version, so it must accept all data %s accepts", curr.name(), prev.name())
		for _, f := range res.Findings {
			if f.Verdict == compat.Compatible {
				continue
			}
			subject := "the schema"
			if len(f.Path.Selectors()) > 0 {
				subject = f.Path.String()
			}
			b.Note(f.Pos, subject+" "+f.Detail)
			if f.ViaResidual {
				b.Notef(token.NoPos, "%s does not declare %s by name, but accepts it through ... or a pattern, so an instance may already hold any value there", prev.name(), f.Claimed)
			}
		}
	}
	b.Note(prev.header(), "the version before it")
	restoreHelp(b)
	return b.Err()
}

// restoreHelp says what to do about a version under history/ that no longer
// agrees with what was cut: find which file changed, and restore it.
func restoreHelp(b *diag.B) {
	b.Helpf("run thema lineage check --published to find which file changed since it was cut, restore it, and make the change in %s", TipFile)
}
