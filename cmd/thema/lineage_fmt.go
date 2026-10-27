package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sdboyer/thema/authoring"
	"github.com/spf13/cobra"
)

type fmtCommand struct {
	dir string
}

var fmtLineageCmd = &cobra.Command{
	Use:   "fmt",
	Short: "Format a lineage's authoring files",
	Long: `Format a lineage's authoring files, in place.

Every version file of the lineage in the directory given by -l, at its top
level and under history/, is rewritten in the authoring form's one layout:
every block keeps its braces, and every field has its own line. Files already
in that layout are left untouched; the ones rewritten are listed.
`,
}

func (fc *fmtCommand) setup(cmd *cobra.Command) {
	cmd.AddCommand(fmtLineageCmd)
	fmtLineageCmd.Flags().StringVarP(&fc.dir, "lineage", "l", ".", "path to the lineage's authoring directory")
	fmtLineageCmd.RunE = fc.run
}

func (fc *fmtCommand) run(cmd *cobra.Command, _ []string) error {
	abs, err := filepath.Abs(fc.dir)
	if err != nil {
		return err
	}
	if !isAuthoringDir(abs) {
		return fmt.Errorf("%s holds no version file (%s); it is not a lineage in the authoring form", fc.dir, "*"+authoring.Ext)
	}
	_, files, err := authoring.Load(os.DirFS(filepath.Dir(abs)), filepath.Base(abs))
	if err != nil {
		return err
	}
	for _, f := range files {
		out, err := authoring.Format(f.Text)
		if err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
		if bytes.Equal(out, f.Text) {
			continue
		}
		// Rewritten with the mode it had.
		p := filepath.Join(abs, filepath.FromSlash(f.Path))
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(p, out, info.Mode().Perm()); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), f.Path); err != nil {
			return err
		}
	}
	return nil
}
