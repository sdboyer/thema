package main

import (
	"fmt"
	"path/filepath"

	"github.com/sdboyer/jenny"
	"github.com/spf13/cobra"

	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
)

type cutCommand struct {
	dir    string
	dryRun bool
}

var cutLineageCmd = &cobra.Command{
	Use:   "cut",
	Short: "Cut a lineage's tip to history/ at the version it cuts as",
	Long: `Cut a lineage's tip to history/ at the version it cuts as.

The tip, tip.thema in the lineage's directory given by -l, is frozen as
history/<major>.<minor>.thema at the version it cuts as (see thema lineage
tip): the whole file, its notes included, with its lenses naming the tip's
instance by that version, v<major>_<minor>, in place of tip. tip.thema is
then reset to clean: its Schema, with its doc comment and the comments within
it, and the imports the Schema uses. The lenses and the notes stay in the
frozen file.

The tip is bound on the development path first, and is not cut while a lens
its edge needs is missing (E1004), or while binding reports an error. Cutting
a clean tip does nothing. The files are written in place; nothing is
committed.

With --dry-run, the version the tip would be cut as and the files the cut
would write are printed, and nothing is written.
`,
	Args: cobra.NoArgs,
}

func (cc *cutCommand) setup(cmd *cobra.Command) {
	cmd.AddCommand(cutLineageCmd)
	cutLineageCmd.Flags().StringVarP(&cc.dir, "lineage", "l", ".", "path to the lineage's authoring directory")
	cutLineageCmd.Flags().BoolVar(&cc.dryRun, "dry-run", false, "print what the cut would write, and write nothing")
	cutLineageCmd.RunE = cc.run
}

func (cc *cutCommand) run(cmd *cobra.Command, _ []string) error {
	abs, err := filepath.Abs(cc.dir)
	if err != nil {
		return diag.New(diag.E0601).Headlinef("%s cannot be resolved to a path", cc.dir).Detail(diag.OSReason(err)).Err()
	}
	name, files, at, err := loadAuthoring(abs)
	if err != nil {
		return err
	}
	cut, err := authoring.CutTip(rt, name, files, at)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if cut.Clean {
		_, werr := fmt.Fprintf(out, "tip (clean at %s): nothing to cut\n", vname(cut.Version))
		return werr
	}
	verb := "cut"
	if cc.dryRun {
		verb = "would cut"
	}
	if _, err = fmt.Fprintf(out, "%s the tip as %s\n", verb, vname(cut.Version)); err != nil {
		return err
	}
	// The frozen version is written before the tip is reset, each on its
	// own: a tip reset while its version failed to freeze would lose the
	// lenses and notes the tip held.
	for _, j := range []jenny.Jenny[*authoring.Cut]{authoring.FrozenVersion, authoring.CleanTip} {
		fs, err := j.GenerateFS(cut)
		if err != nil {
			return err
		}
		if !cc.dryRun {
			if err := writeFS(cmd, fs, abs); err != nil {
				return err
			}
		}
		for _, f := range fs.AsFiles() {
			if _, err := fmt.Fprintln(out, f.Path()); err != nil {
				return err
			}
		}
	}
	return nil
}
