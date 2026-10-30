package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"

	cerrors "cuelang.org/go/cue/errors"
	"github.com/spf13/cobra"

	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/load"
)

// newSourceFileMode is the mode of a normalized.gen.cue written where there
// was none: a source file, readable by all, like the version files beside it.
const newSourceFileMode fs.FileMode = 0o644

type compileCommand struct {
	dir   string
	check bool
}

var compileLineageCmd = &cobra.Command{
	Use:   "compile -l <dir> [--check]",
	Short: "Write each lineage's normal form to its normalized.gen.cue",
	Long: `Write each lineage's normal form to its normalized.gen.cue.

A lineage of a collection keeps its normal form beside its tip and history/,
in normalized.gen.cue: the versions under history/, compiled, never the tip.
The file is a CUE package named for the lineage, which imports thema's CUE
module, so native CUE can import the lineage by its directory's path. It is
generated, committed, and never edited by hand.

-l is one lineage's directory, or a collection of them: a directory whose
immediate subdirectories are lineages. Each lineage's normalized.gen.cue is
rewritten when it differs from what its history/ compiles to, and the ones
rewritten are listed. A lineage whose history/ does not compile and bind is
an error, and the others are still written.

With --check, nothing is written: a normalized.gen.cue that is missing or
differs is an error (E0151), for CI to check that it was regenerated.
`,
	Args: cobra.NoArgs,
}

func (cc *compileCommand) setup(cmd *cobra.Command) {
	cmd.AddCommand(compileLineageCmd)
	compileLineageCmd.Flags().StringVarP(&cc.dir, "lineage", "l", ".", "path to a lineage's authoring directory, or a collection of them")
	compileLineageCmd.Flags().BoolVar(&cc.check, "check", false, "write nothing, and fail where normalized.gen.cue is missing or differs")
	compileLineageCmd.RunE = cc.run
}

func (cc *compileCommand) run(cmd *cobra.Command, _ []string) error {
	if err := openWorkspace(cc.dir); err != nil {
		return err
	}
	dir, err := ws.name(cc.dir)
	if err != nil {
		return err
	}

	// One lineage, or each of a collection's.
	var lins []authoring.Member
	if isAuthoringDir(dir) {
		m := authoring.Member{Dir: dir}
		m.Name, m.Files, m.Err = authoring.Load(ws.fsys, dir)
		lins = append(lins, m)
	} else {
		lins, err = authoring.LoadCollection(ws.fsys, dir)
		if err != nil {
			return err
		}
	}

	var errs cerrors.Error
	fail := func(err error) { errs = cerrors.Append(errs, cerrors.Promote(err, "")) }
	for _, l := range lins {
		if l.Err != nil {
			fail(l.Err)
			continue
		}
		// Bound first, as the collection's loader binds it: the normal form
		// of a lineage that does not bind is nothing to import.
		if _, err := load.AuthoringLineage(ws.fsys, l.Dir, rt); err != nil {
			fail(err)
			continue
		}
		c, err := authoring.Compile(ctx, l.Name, l.Files)
		if err != nil {
			fail(err)
			continue
		}
		if cc.check {
			if err = authoring.CheckNormalizedFile(c, ws.fsys, l.Dir); err != nil {
				fail(err)
			}
			continue
		}
		want, err := authoring.GenerateNormalizedFile(c)
		if err != nil {
			fail(err)
			continue
		}
		name := path.Join(l.Dir, authoring.NormalizedFile)
		committed, err := fs.ReadFile(ws.fsys, name)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			fail(diag.New(diag.E0601).Headlinef("%s cannot be read", displayPath(name)).Detail(diag.OSReason(err)).Err())
			continue
		case bytes.Equal(committed, want):
			continue
		}
		// fsys is read-only: the file is written at its host path, as the
		// workspace maps its name.
		p := ws.osPath(name)
		// A new file is a source file like the version files beside it; a
		// rewritten one keeps its mode.
		mode := newSourceFileMode
		if info, serr := fs.Stat(ws.fsys, name); serr == nil {
			mode = info.Mode().Perm()
		}
		if err = os.WriteFile(p, want, mode); err != nil {
			fail(diag.New(diag.E0616).Headlinef("%s cannot be written", displayPath(name)).Detail(diag.OSReason(err)).Err())
			continue
		}
		if _, err = fmt.Fprintln(cmd.OutOrStdout(), displayPath(name)); err != nil {
			return err
		}
	}
	if errs != nil {
		return errs
	}
	return nil
}
