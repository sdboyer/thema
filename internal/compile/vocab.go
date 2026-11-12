package compile

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compat"
)

// Vocabulary is the authoring form's: diagnostics about the compiled lineage
// name version files, the ForwardLens or BackwardLens in them, and the lens
// parameters (v0_0) the author wrote, rather than the normal form's schemas,
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

// param is the name the author gives an instance of ver in a lens: its
// version's, or TipParam for the tip's.
func (v vocab) param(ver diag.Version) string {
	if v.tip != nil && *v.tip == thema.SyntacticVersion(ver) {
		return TipParam
	}
	return paramName(thema.SyntacticVersion(ver))
}

func (v vocab) file(ver diag.Version) string {
	if p, ok := v.paths[thema.SyntacticVersion(ver)]; ok {
		return p
	}
	return ver.String() + Ext
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
		return []string{file, block, v.param(a.From)}
	case diag.AddrLensResult:
		file, block := v.lens(a)
		return []string{file, block, v.param(a.To)}
	case diag.AddrLensResultField:
		file, block := v.lens(a)
		return append([]string{file, block, v.param(a.To)}, field...)
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
		return "the schema in " + v.file(a.Version)
	case diag.AddrSchemaField:
		return field + " in " + v.file(a.Version)
	case diag.AddrExample:
		return "example " + a.Name + " in " + v.file(a.Version)
	case diag.AddrLens, diag.AddrStep:
		file, block := v.lens(a)
		return fmt.Sprintf("the %s in %s", block, file)
	case diag.AddrLensInput:
		file, block := v.lens(a)
		return fmt.Sprintf("%s, the input of the %s in %s", v.param(a.From), block, file)
	case diag.AddrLensResult:
		file, block := v.lens(a)
		return fmt.Sprintf("the body of the %s in %s", block, file)
	case diag.AddrLensResultField:
		file, block := v.lens(a)
		return fmt.Sprintf("%s in the %s in %s", field, block, file)
	case diag.AddrLacuna:
		file, block := v.lens(a)
		return fmt.Sprintf("a lacuna of the %s in %s", block, file)
	case diag.AddrLacunaField:
		file, block := v.lens(a)
		return fmt.Sprintf("%s of a lacuna of the %s in %s", a.Name, block, file)
	}
	return diag.NormalForm.Term(a)
}

// Rewrite renders a checker's sentence about a lens in the author's terms:
// the normal form's input is the parameter the author named after the
// source version, v0_0, and "the target schema" is the target version's file.
func (v vocab) Rewrite(a diag.Address, sentence string) string {
	if !lensAddr(a) {
		return sentence
	}
	param := v.param(a.From)
	r := strings.NewReplacer(
		"input.", param+".",
		"an input field", "a field of "+param,
		"the input", param,
		"the source schema", v.file(a.From),
		"the target schema", v.file(a.To),
	)
	return r.Replace(sentence)
}

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
// run of the check can tell which. The diagnostic names both files, with each
// finding where it is.
func historyMismatch(res compat.Result, prev, curr *version) *diag.Error {
	b := diag.New(diag.E0207).At(curr.header())
	if curr.v[1] == 0 {
		b.Headlinef("immutability violation: %s is backward compatible with %s", curr.path, prev.path).
			Notef(token.NoPos, "a version that accepts all data the one before it accepts is a minor version, and %s is a major version", curr.v)
	} else {
		verb := "is not backward compatible with"
		if res.Verdict() == compat.Unknown {
			verb = "cannot be shown backward compatible with"
		}
		b.Headlinef("immutability violation: %s %s %s", curr.path, verb, prev.path).
			Notef(token.NoPos, "%s is a minor version, so it must accept all data %s accepts", curr.v, prev.v)
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
				b.Notef(token.NoPos, "%s does not declare %s by name, but accepts it through ... or a pattern, so an instance may already hold any value there", prev.path, f.Path)
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
