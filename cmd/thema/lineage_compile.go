package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
)

type compileCommand struct {
	dir   string
	check bool
}

var compileLineageCmd = &cobra.Command{
	Use:   "compile -l <dir> [--check]",
	Short: "Write a lineage's normal form beside it, as normalized.gen.cue",
	Long: `Write a lineage's normal form beside it, as normalized.gen.cue.

The lineage is the one in the authoring form in the directory given by -l. Its
history, every version under history/, is compiled to the normal form and
bound, and written to normalized.gen.cue in the same directory: a CUE package
named for the lineage, whose value is its normal form, so that plain CUE
imports the lineage by its directory's path. The tip is never part of it. A lineage named
for a CUE keyword, such as for, declares package for_, imported as
"<module>/<path>/for:for_".

The file is generated; it is never edited by hand. thema lineage cut rewrites
it with every cut. With --check, nothing is written, and the command fails if
normalized.gen.cue is missing or is not what would be written, as for CI.
`,
	Args: cobra.NoArgs,
}

func (cc *compileCommand) setup(cmd *cobra.Command) {
	cmd.AddCommand(compileLineageCmd)
	compileLineageCmd.Flags().StringVarP(&cc.dir, "lineage", "l", ".", "path to the lineage's authoring directory")
	compileLineageCmd.Flags().BoolVar(&cc.check, "check", false, "write nothing, and fail if normalized.gen.cue is not what would be written")
	compileLineageCmd.RunE = cc.run
}

func (cc *compileCommand) run(cmd *cobra.Command, _ []string) error {
	abs, err := filepath.Abs(cc.dir)
	if err != nil {
		return diag.New(diag.E0601).Headlinef("%s cannot be resolved to a path", cc.dir).Detail(diag.OSReason(err)).Err()
	}
	name, files, at, err := loadAuthoring(abs)
	if err != nil {
		return err
	}
	c, err := authoring.Compile(rt.Context(), name, files, at)
	if err != nil {
		return err
	}
	if _, err = authoring.BindDevelopment(rt, c); err != nil {
		return err
	}
	fs, err := authoring.NormalForm.GenerateFS(c)
	if err != nil {
		return err
	}
	if cc.check {
		return driftDiagnostics(fs.Verify(cmd.Context(), abs), "-l", abs)
	}
	if err = writeFS(cmd, fs, abs); err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), authoring.NormalizedFile)
	return err
}
