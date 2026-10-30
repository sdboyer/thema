package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"

	"cuelang.org/go/cue/parser"
	"github.com/spf13/cobra"

	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
)

type fmtCommand struct {
	dir string
}

var fmtLineageCmd = &cobra.Command{
	Use:   "fmt",
	Short: "Format a lineage's authoring files",
	Long: `Format a lineage's authoring files, in place.

Every file of the lineage in the directory given by -l, its tip.thema and the
versions under history/, is rewritten in the authoring form's one layout:
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
	if err := openWorkspace(fc.dir); err != nil {
		return err
	}
	dir, err := ws.name(fc.dir)
	if err != nil {
		return err
	}
	if !isAuthoringDir(dir) {
		return diag.New(diag.E0110).Headlinef("%s holds no version file, so it is not a lineage in the authoring form", fc.dir).
			Helpf("a lineage in the authoring form is a directory holding %s and its frozen versions under %s/, such as %s/0.0%s", authoring.TipFile, authoring.HistoryDir, authoring.HistoryDir, authoring.Ext).
			Err()
	}
	_, files, err := authoring.Load(ws.fsys, dir)
	if err != nil {
		return err
	}
	for _, f := range files {
		// Parsed under its name first, so that a syntax error points into
		// the file; Format parses without one.
		if _, err := parser.ParseFile(f.Name, f.Text, parser.ParseComments); err != nil {
			return diag.Convert(err, diag.Context{Op: diag.OpCompile, Generic: diag.E0102})
		}
		out, err := authoring.Format(f.Text)
		if err != nil {
			return diag.InternalErr(fmt.Errorf("formatting %s: %w", f.Path, err))
		}
		if bytes.Equal(out, f.Text) {
			continue
		}
		// Rewritten with the mode it had, at its host path: fsys is
		// read-only.
		info, err := fs.Stat(ws.fsys, f.Name)
		if err != nil {
			return diag.New(diag.E0601).Headlinef("%s cannot be read", displayPath(f.Name)).Detail(diag.OSReason(err)).Err()
		}
		if err := os.WriteFile(ws.osPath(f.Name), out, info.Mode().Perm()); err != nil {
			return diag.New(diag.E0616).Headlinef("%s cannot be written", displayPath(f.Name)).Detail(diag.OSReason(err)).Err()
		}
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), f.Path); err != nil {
			return err
		}
	}
	return nil
}
