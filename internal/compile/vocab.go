package compile

import (
	"fmt"
	"strings"

	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compat"
)

// Vocabulary is the authoring form's: diagnostics about the compiled lineage
// name version files, the ForwardLens or BackwardLens in them, and the lens
// parameters (v0_0) the author wrote, rather than the normal form's schemas,
// lenses, input and result. Pass it to thema.BindLineage as
// thema.WithVocabulary. It is a [diag.Immutability]: binding reports an error
// in a file under history/ as a violation of that file's immutability.
func Vocabulary(c *Compiled) diag.Vocabulary {
	return vocab{paths: c.paths, tip: c.provisional, frozen: c.frozen}
}

type vocab struct {
	paths map[thema.SyntacticVersion]string
	// tip is the version the tip was compiled as, on the development path.
	tip    *thema.SyntacticVersion
	frozen frozen
}

// Immutable implements [diag.Immutability].
func (v vocab) Immutable(err error) error { return v.frozen.immutable(err) }

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

// name is version ver as messages name it: v0.1, or the tip.
func (v vocab) name(ver diag.Version) string {
	if v.tip != nil && *v.tip == thema.SyntacticVersion(ver) {
		return TipParam
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
		return "the schema of " + v.name(a.Version)
	case diag.AddrSchemaField:
		return field + " in " + v.name(a.Version)
	case diag.AddrExample:
		return "example " + a.Name + " of " + v.name(a.Version)
	case diag.AddrLens, diag.AddrStep:
		return v.lensName(a)
	case diag.AddrLensInput:
		return fmt.Sprintf("%s, the input of %s", v.param(a.From), v.lensName(a))
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
// the normal form's input is the parameter the author named after the
// source version, v0_0, and "the target schema" is the target version.
func (v vocab) Rewrite(a diag.Address, sentence string) string {
	if !lensAddr(a) {
		return sentence
	}
	param := v.param(a.From)
	r := strings.NewReplacer(
		"input.", param+".",
		"an input field", "a field of "+param,
		"the input", param,
		"the source schema", v.name(a.From),
		"the target schema", v.name(a.To),
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
// agrees with what was cut: find which file changed, and restore it. It names
// no command: thema lineage check --published compiles the lineage first, and
// so reports the same error rather than the file.
func restoreHelp(b *diag.B) {
	b.Helpf("find which file under %s/ changed since it was cut, restore it, and make the change in %s", HistoryDir, TipFile)
}

// frozen is the version each file under history/ keeps, keyed by the name the
// file's positions carry.
type frozen map[string]thema.SyntacticVersion

// frozenFiles is the version each of the history's files keeps.
func frozenFiles(history []File, o options) frozen {
	fz := make(frozen, len(history))
	for _, f := range history {
		if v, ok := frozenVersion(f.Path); ok {
			fz[o.name(f)] = v
		}
	}
	return fz
}

// frozenOf is the version each of vs's files under history/ keeps.
func frozenOf(vs []*version) frozen {
	fz := make(frozen, len(vs))
	for _, v := range vs {
		if !v.tip {
			fz[v.tf.Name()] = v.v
		}
	}
	return fz
}

// immutable reports each error in err positioned in a file under history/ as
// a violation of that file's immutability: the file's first error is led by
// one E0209, naming its version, and every error in it is given without its
// help, which would edit the file. A file with an error already worded as a
// violation, E0207 or E0355, gets no E0209, and that error keeps its help.
func (fz frozen) immutable(err error) error {
	if err == nil || len(fz) == 0 {
		return err
	}
	errs := cerrors.Errors(err)
	// An error about the lineage's name is not about the file it is positioned
	// in: the name is the directory's, positioned at a version file for want of
	// a place of its own.
	in := func(e cerrors.Error) (*diag.Error, string, bool) {
		de, ok := e.(*diag.Error)
		if !ok || de.Diagnostic().Severity != diag.SeverityError {
			return nil, "", false
		}
		if a := de.Diagnostic().Address; a != nil && a.Kind == diag.AddrName {
			return nil, "", false
		}
		name := de.Position().Filename()
		_, ok = fz[name]
		return de, name, ok
	}
	// E0207 names both versions; E0355, in a file under history/, the version
	// before it too.
	violates := func(de *diag.Error) bool {
		return de.Diagnostic().Code == diag.E0207 || de.Diagnostic().Code == diag.E0355
	}
	count := make(map[string]int)
	led := make(map[string]bool)
	for _, e := range errs {
		if de, name, ok := in(e); ok {
			count[name]++
			led[name] = led[name] || violates(de)
		}
	}
	out := make([]error, 0, len(errs)+len(count))
	for _, e := range errs {
		de, name, ok := in(e)
		switch {
		case !ok:
			out = append(out, e)
		case violates(de):
			out = append(out, de)
		default:
			if !led[name] {
				led[name] = true
				out = append(out, violation(fz[name], count[name], de.Position().File().Pos(0, token.NoRelPos)))
			}
			out = append(out, de.WithoutHelp())
		}
	}
	return diag.Join(out...)
}

// violation is the E0209 that leads the n errors in the file under history/
// that keeps v, positioned at the start of the file.
func violation(v thema.SyntacticVersion, n int, at token.Pos) error {
	errs := "an error"
	if n > 1 {
		errs = fmt.Sprintf("%d errors", n)
	}
	b := diag.New(diag.E0209).At(at).
		Headlinef("immutability violation: %s is under %s/, and has %s", vname(v), HistoryDir, errs).
		Notef(token.NoPos, "a version under %s/ never changes once it is cut, and had no error when it was cut, so since then its file, another file under %s/, or thema has changed, or the file was never cut", HistoryDir, HistoryDir)
	restoreHelp(b)
	return b.Err()
}
