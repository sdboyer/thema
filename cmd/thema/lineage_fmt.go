package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

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
	abs, err := filepath.Abs(fc.dir)
	if err != nil {
		return diag.New(diag.E0601).Headlinef("%s cannot be resolved to a path", fc.dir).Detail(diag.OSReason(err)).Err()
	}
	if !isAuthoringDir(abs) {
		return diag.New(diag.E0110).Headlinef("%s holds no version file, so it is not a lineage in the authoring form", fc.dir).
			Helpf("a lineage in the authoring form is a directory holding %s and its frozen versions under %s/, such as %s/0.0%s", authoring.TipFile, authoring.HistoryDir, authoring.HistoryDir, authoring.Ext).
			Err()
	}
	_, files, err := authoring.Load(os.DirFS(filepath.Dir(abs)), filepath.Base(abs))
	if err != nil {
		return err
	}
	for _, f := range files {
		// Parsed under its path first, so that a syntax error points into
		// the file; Format parses without one.
		if _, err := parser.ParseFile(filepath.Join(fc.dir, filepath.FromSlash(f.Path)), f.Text, parser.ParseComments); err != nil {
			return diag.Convert(err, diag.Context{Op: diag.OpCompile, Generic: diag.E0102})
		}
		out, err := authoring.Format(f.Text)
		if err != nil {
			return diag.InternalErr(fmt.Errorf("formatting %s: %w", f.Path, err))
		}
		if bytes.Equal(out, f.Text) {
			continue
		}
		// Rewritten with the mode it had.
		p := filepath.Join(abs, filepath.FromSlash(f.Path))
		info, err := os.Stat(p)
		if err != nil {
			return diag.New(diag.E0601).Headlinef("%s cannot be read", displayPath(p)).Detail(diag.OSReason(err)).Err()
		}
		if err := os.WriteFile(p, out, info.Mode().Perm()); err != nil {
			return diag.New(diag.E0616).Headlinef("%s cannot be written", displayPath(p)).Detail(diag.OSReason(err)).Err()
		}
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), f.Path); err != nil {
			return err
		}
	}
	return nil
}
