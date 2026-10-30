package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	cerrors "cuelang.org/go/cue/errors"
	"github.com/spf13/cobra"

	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/load"
)

// newSourceFileMode is the mode of a lineage.cue written where there was none:
// a source file, readable by all, like the version files beside it.
const newSourceFileMode fs.FileMode = 0o644

type compileCommand struct {
	dir   string
	check bool
}

var compileLineageCmd = &cobra.Command{
	Use:   "compile -l <dir> [--check]",
	Short: "Write each lineage's normal form to its lineage.cue",
	Long: `Write each lineage's normal form to its lineage.cue.

A lineage of a collection keeps its normal form beside its tip and history/,
in lineage.cue: the versions under history/, compiled, never the tip. The
file is a CUE package named for the lineage, which imports thema's CUE
module, so native CUE can import the lineage by its directory's path. It is
generated, committed, and never edited by hand.

-l is one lineage's directory, or a collection of them: a directory whose
immediate subdirectories are lineages. Each lineage's lineage.cue is
rewritten when it differs from what its history/ compiles to, and the ones
rewritten are listed. A lineage whose history/ does not compile and bind is
an error, and the others are still written.

With --check, nothing is written: a lineage.cue that is missing or differs
is an error (E0151), for CI to check that it was regenerated.
`,
	Args: cobra.NoArgs,
}

func (cc *compileCommand) setup(cmd *cobra.Command) {
	cmd.AddCommand(compileLineageCmd)
	compileLineageCmd.Flags().StringVarP(&cc.dir, "lineage", "l", ".", "path to a lineage's authoring directory, or a collection of them")
	compileLineageCmd.Flags().BoolVar(&cc.check, "check", false, "write nothing, and fail where lineage.cue is missing or differs")
	compileLineageCmd.RunE = cc.run
}

func (cc *compileCommand) run(cmd *cobra.Command, _ []string) error {
	abs, err := filepath.Abs(cc.dir)
	if err != nil {
		return diag.New(diag.E0601).Headlinef("%s cannot be resolved to a path", cc.dir).Detail(diag.OSReason(err)).Err()
	}
	fsys := os.DirFS(filepath.Dir(abs))
	base := filepath.Base(abs)

	// One lineage, or each of a collection's.
	type lineage struct {
		dir string // beneath fsys
		authoring.Member
	}
	var lins []lineage
	if isAuthoringDir(abs) {
		m := authoring.Member{Name: base}
		_, m.Files, m.Err = authoring.Load(fsys, base)
		lins = append(lins, lineage{dir: base, Member: m})
	} else {
		members, err := authoring.LoadCollection(fsys, base)
		if err != nil {
			return err
		}
		for _, m := range members {
			lins = append(lins, lineage{dir: path.Join(base, m.Name), Member: m})
		}
	}

	var errs cerrors.Error
	fail := func(err error) { errs = cerrors.Append(errs, cerrors.Promote(err, "")) }
	for _, l := range lins {
		// Each lineage positions its files relative to its own directory.
		sourceDirs = append(sourceDirs, filepath.Join(filepath.Dir(abs), filepath.FromSlash(l.dir)))
		if l.Err != nil {
			fail(l.Err)
			continue
		}
		// Bound first, as the collection's loader binds it: the normal form
		// of a lineage that does not bind is nothing to import.
		if _, err := load.AuthoringLineage(fsys, l.dir, rt); err != nil {
			fail(err)
			continue
		}
		c, err := authoring.Compile(ctx, l.Name, l.Files)
		if err != nil {
			fail(err)
			continue
		}
		p := filepath.Join(filepath.Dir(abs), filepath.FromSlash(l.dir), authoring.LineageFile)
		committed, err := os.ReadFile(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			committed = nil
		case err != nil:
			fail(diag.New(diag.E0601).Headlinef("%s cannot be read", displayPath(p)).Detail(diag.OSReason(err)).Err())
			continue
		case committed == nil:
			committed = []byte{}
		}
		if cc.check {
			if err = authoring.CheckLineageFile(c, committed); err != nil {
				fail(err)
			}
			continue
		}
		want, err := authoring.GenerateLineageFile(c)
		if err != nil {
			fail(err)
			continue
		}
		if bytes.Equal(committed, want) {
			continue
		}
		// A new file is a source file like the version files beside it; a
		// rewritten one keeps its mode.
		mode := newSourceFileMode
		if info, serr := os.Stat(p); serr == nil {
			mode = info.Mode().Perm()
		}
		if err = os.WriteFile(p, want, mode); err != nil {
			fail(diag.New(diag.E0616).Headlinef("%s cannot be written", displayPath(p)).Detail(diag.OSReason(err)).Err())
			continue
		}
		if _, err = fmt.Fprintln(cmd.OutOrStdout(), displayPath(p)); err != nil {
			return err
		}
	}
	if errs != nil {
		return errs
	}
	return nil
}
