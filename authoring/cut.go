package authoring

import (
	"fmt"

	"cuelang.org/go/cue"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compile"
)

// Cut is what cutting a lineage's tip did: the version it minted, and the
// lineage's files after the cut.
type Cut struct {
	// Version is the version the tip was cut as. When the tip was clean there
	// was nothing to cut, and Version is the newest version under history/,
	// which the tip equals.
	Version thema.SyntacticVersion
	// Clean reports that the tip was clean: the cut changed nothing, Written
	// is empty and Files are the files it was given.
	Clean bool
	// Written are the files the cut writes: the frozen version,
	// history/<Version>.thema, and the tip as it is after the cut, clean.
	Written []File
	// Files are all the lineage's files after the cut: the history with the
	// new version, and the clean tip.
	Files []File
}

// CutTip cuts the tip of the lineage name, in files, to history/ at the
// version it cuts as. It writes nothing: the files after the cut are the
// result, for the caller to write.
//
// The tip is bound on the development path first, and is cut only when
// nothing about it is provisional: a lens its edge needs and does not write
// (E1001) is an error here (E1004), and every error binding reports is
// returned as it is. The frozen version is the whole tip file, its notes
// included, with its lenses naming the tip's instance by the new version,
// v<major>_<minor>, in place of tip, laid out as [Format] lays it out. The tip
// after the cut is clean: its Schema, with its doc comment and the comments
// within it, and the imports the Schema uses. The lenses and the notes stay in
// the frozen file alone.
//
// Cutting a clean tip changes nothing and is not an error. A tip whose
// parent moved, as when another branch cut first, is reported by binding
// (E0150), not by the cut.
func CutTip(rt *thema.Runtime, name string, files []File, opts ...Option) (*Cut, error) {
	ctx := rt.Context()
	c, err := compile.Development(ctx, name, files, opts...)
	if err != nil {
		return nil, err
	}
	if _, err = BindDevelopment(rt, c); err != nil {
		return nil, err
	}
	if c.Tip.Clean {
		return &Cut{Version: c.Tip.Version, Clean: true, Files: files}, nil
	}
	if c.Pending != nil {
		return nil, pendingLenses(c)
	}

	frozen, tip, err := compile.CutFiles(files, c.Tip.Version)
	if err != nil {
		return nil, err
	}
	after := []File{frozen}
	for _, f := range files {
		if f.Path == TipFile {
			f = tip
		}
		after = append(after, f)
	}

	// What the cut wrote must be the lineage it was given, with the tip
	// frozen: the history binds, it appends to the history before, and the
	// tip after is clean. Anything else is a fault in the cut.
	cut, err := bindHistory(rt, name, after, opts)
	if err != nil {
		return nil, diag.InternalErr(fmt.Errorf("the history after cutting the tip as %s does not bind: %w", vname(c.Tip.Version), err))
	}
	if c.Tip.Base != nil {
		before, err := bindHistory(rt, name, files, opts)
		if err != nil {
			return nil, err
		}
		if err := thema.CheckAppendOnly(before, cut); err != nil {
			return nil, diag.InternalErr(fmt.Errorf("the history after cutting the tip as %s does not append to the history before: %w", vname(c.Tip.Version), err))
		}
	}
	if err := CheckClean(ctx, after, opts...); err != nil {
		return nil, diag.InternalErr(fmt.Errorf("the tip after cutting it as %s is not clean: %w", vname(c.Tip.Version), err))
	}
	return &Cut{Version: c.Tip.Version, Written: []File{frozen, tip}, Files: after}, nil
}

// bindHistory compiles and binds the lineage's history alone.
func bindHistory(rt *thema.Runtime, name string, files []File, opts []Option) (thema.Lineage, error) {
	c, err := Compile(rt.Context(), name, files, opts...)
	if err != nil {
		return nil, err
	}
	return thema.BindLineage(rt.Context().BuildFile(c.File), cue.Path{}, rt, thema.WithVocabulary(Vocabulary(c)))
}

// pendingLenses is the E1004 error for a tip whose edge still lacks the
// lenses c.Pending reports, at the tip and with a note for each.
func pendingLenses(c *compile.Compiled) error {
	b := diag.New(diag.E1004).Headlinef("the tip cannot be cut as %s while a lens on its edge is missing", vname(c.Tip.Version))
	for i, e := range cerrors.Errors(c.Pending) {
		label := e.Error()
		if de, ok := e.(*diag.Error); ok {
			label = de.Diagnostic().Headline
		}
		if i == 0 {
			b = b.At(e.Position())
		}
		b = b.Note(token.NoPos, label)
	}
	return b.Note(token.NoPos, "a version that has been cut never changes, so the lenses into it are written before it is").
		Helpf("write the missing lenses in %s, then cut it", TipFile).
		Err()
}

// vname is version v as messages name it: v0.1. v.String() is the version
// as data writes it.
func vname(v thema.SyntacticVersion) string {
	return "v" + v.String()
}
