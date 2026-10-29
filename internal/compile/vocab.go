package compile

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/rulecodes"
)

// Vocabulary is the authoring form's: diagnostics about the compiled lineage
// name version files, the ForwardLens or BackwardLens in them, and the lens
// parameters (v0_0) the author wrote, rather than the normal form's schemas,
// lenses, input and result. Pass it to thema.BindLineage as
// thema.WithVocabulary.
func Vocabulary(c *Compiled) diag.Vocabulary {
	return vocab{paths: c.paths}
}

type vocab struct {
	paths map[thema.SyntacticVersion]string
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
		return []string{file, block, paramName(thema.SyntacticVersion(a.From))}
	case diag.AddrLensResult:
		file, block := v.lens(a)
		return []string{file, block, paramName(thema.SyntacticVersion(a.To))}
	case diag.AddrLensResultField:
		file, block := v.lens(a)
		return append([]string{file, block, paramName(thema.SyntacticVersion(a.To))}, field...)
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
		return fmt.Sprintf("%s, the input of the %s in %s", paramName(thema.SyntacticVersion(a.From)), block, file)
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
// A closing remedy written in the normal form's syntax, such as a presence
// test to write a field under, is dropped: the authoring form has no way to
// write one, and tests presence itself wherever a mapping or a when reads a
// field the source may omit.
func (v vocab) Rewrite(a diag.Address, sentence string) string {
	switch a.Kind {
	case diag.AddrLens, diag.AddrLensInput, diag.AddrLensResult, diag.AddrLensResultField, diag.AddrLacuna, diag.AddrLacunaField, diag.AddrStep:
	default:
		return sentence
	}
	if i := strings.LastIndex(sentence, "; "); i >= 0 && strings.Contains(sentence[i:], "_|_") {
		sentence = sentence[:i]
	}
	param := paramName(thema.SyntacticVersion(a.From))
	r := strings.NewReplacer(
		"input.", param+".",
		"an input field", "a field of "+param,
		"the input", param,
		"the source schema", v.file(a.From),
		"the target schema", v.file(a.To),
	)
	return r.Replace(sentence)
}

// compatDiag describes one finding of the compatibility checker between the
// version files prev and curr, where curr was named as a minor version and is
// not backward compatible.
func compatDiag(f compat.Finding, prev, curr *version) *diag.Error {
	code := rulecodes.CompatCode(f.Rule)
	subject := curr.path
	if sels := f.Path.Selectors(); len(sels) > 0 {
		subject = f.Path.String() + " in " + curr.path
	}
	b := diag.New(code)
	sentence := subject + " " + f.Detail
	short := subject + " is not backward compatible with " + prev.v.String()
	if len(sentence) <= diag.MaxHeadline {
		b.Headline(sentence)
	} else {
		b.Headline(short).Note(token.NoPos, sentence)
	}
	if f.Pos.IsValid() {
		b.At(f.Pos)
	} else {
		b.At(curr.header())
	}
	major := thema.SV(prev.v[0]+1, 0)
	b.Notef(token.NoPos, "%s is named as minor version %s of %s, so every instance of %s must be an instance of it (compatibility rule %s)", curr.path, curr.v, prev.v, prev.v, f.Rule)
	if f.ViaResidual {
		b.Notef(token.NoPos, "%s does not declare %s by name, but accepts it through ... or a pattern, so an instance may already hold any value there", prev.path, f.Path)
		b.Helpf("declare it as %s?: _ to accept what %s accepted, or rename the file %s%s", f.Path, prev.v, major, Ext)
	} else if f.Verdict == compat.Unknown {
		b.Helpf("change the schema so the checker can decide, or rename the file %s%s", major, Ext)
	} else {
		b.Helpf("make the change compatible, or rename the file %s%s", major, Ext)
	}
	return b.Err()
}
