package main

import (
	"fmt"
	"os"
	"path/filepath"

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
		_, werr := fmt.Fprintf(out, "tip (clean at %s): nothing to cut\n", cut.Version)
		return werr
	}
	verb := "cut"
	if cc.dryRun {
		verb = "would cut"
	}
	if _, err = fmt.Fprintf(out, "%s the tip as %s\n", verb, cut.Version); err != nil {
		return err
	}
	// The tip's mode is the frozen file's too, and the lineage directory's is
	// history/'s, when the cut is the first.
	tipPath := filepath.Join(abs, authoring.TipFile)
	info, err := os.Stat(tipPath)
	if err != nil {
		return diag.New(diag.E0601).Headlinef("%s cannot be read", displayPath(tipPath)).Detail(diag.OSReason(err)).Err()
	}
	dirInfo, err := os.Stat(abs)
	if err != nil {
		return diag.New(diag.E0601).Headlinef("%s cannot be read", displayPath(abs)).Detail(diag.OSReason(err)).Err()
	}
	for _, f := range cut.Written {
		if !cc.dryRun {
			p := filepath.Join(abs, filepath.FromSlash(f.Path))
			if err := os.MkdirAll(filepath.Dir(p), dirInfo.Mode().Perm()); err != nil {
				return diag.New(diag.E0616).Headlinef("%s cannot be written", displayPath(filepath.Dir(p))).Detail(diag.OSReason(err)).Err()
			}
			if err := os.WriteFile(p, f.Text, info.Mode().Perm()); err != nil {
				return diag.New(diag.E0616).Headlinef("%s cannot be written", displayPath(p)).Detail(diag.OSReason(err)).Err()
			}
		}
		if _, err := fmt.Fprintln(out, f.Path); err != nil {
			return err
		}
	}
	return nil
}
