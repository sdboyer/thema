package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sdboyer/thema/authoring"
)

type tipCommand struct {
	dir   string
	clean bool
}

var tipLineageCmd = &cobra.Command{
	Use:   "tip",
	Short: "Report the version a lineage's tip cuts as",
	Long: `Report the version a lineage's tip cuts as.

The tip, tip.thema in the lineage's directory given by -l, is the version
being worked on. Its version is never written: it is derived by the
compatibility check against the newest version under history/, the next minor
version when the tip is backward compatible with it and the next major version
otherwise, or 0.0 when history/ is empty. The tip is printed as, for example,
"tip (cuts as 2.1)", or "tip (clean at 2.0)" when it equals the newest version
under history/ and declares no lenses: there is nothing to cut.

With --check-clean, a tip that is not clean is an error (E1003), for CI to
assert that the tip was cut before merging.
`,
	Args: cobra.NoArgs,
}

func (tc *tipCommand) setup(cmd *cobra.Command) {
	cmd.AddCommand(tipLineageCmd)
	tipLineageCmd.Flags().StringVarP(&tc.dir, "lineage", "l", ".", "path to the lineage's authoring directory")
	tipLineageCmd.Flags().BoolVar(&tc.clean, "check-clean", false, "fail unless the tip equals the newest version under history/, with no lenses")
	tipLineageCmd.RunE = tc.run
}

func (tc *tipCommand) run(cmd *cobra.Command, _ []string) error {
	if err := openWorkspace(tc.dir); err != nil {
		return err
	}
	dir, err := ws.name(tc.dir)
	if err != nil {
		return err
	}
	_, files, err := authoring.Load(ws.fsys, dir)
	if err != nil {
		return err
	}
	tip, err := authoring.ReadTip(ctx, files)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), tip); err != nil {
		return err
	}
	if tc.clean {
		return authoring.CheckClean(ctx, files)
	}
	return nil
}
