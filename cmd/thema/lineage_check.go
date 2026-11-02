package main

import (
	"github.com/spf13/cobra"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
)

type checkCommand struct {
	lla       *lineageLoadArgs
	published string
}

var checkLineageCmd = &cobra.Command{
	Use:   "check -l <lineage-fs-path> --published <lineage-fs-path> [-p <cue-path>]",
	Short: "Check that a lineage only appends to the version of it that was published",
	Long: `Check that a lineage only appends to the version of it that was published.

The lineage given by -l is a candidate: the next revision of the lineage
given by --published, which is the record of what was published, such as a
checkout of the lineage's directory at the release's git revision. The
candidate only appends when every published version is still in it, at the
same version number and with the same identity, its new versions all come
after the newest published one, and its name is the same. A version's
identity is the digest of its schema and the digest of the lenses on the edge
into it, so formatting and comments do not change it.

Each violation is an error, positioned in the candidate: a changed schema
(E0250), a changed lens (E0251), a removed version (E0252), a version
inserted before the newest published one (E0253), or a changed name (E0254).

Both lineages are read as they are published. A lineage in the authoring form
is read from its history/ alone, so its tip is never part of the check.
Either path takes the forms -l takes, and -p applies to both.
`,
	Args: cobra.NoArgs,
}

func (cc *checkCommand) setup(cmd *cobra.Command) {
	cmd.AddCommand(checkLineageCmd)
	cc.lla = new(lineageLoadArgs)
	addLinPathVars(checkLineageCmd, cc.lla)
	checkLineageCmd.Flags().StringVar(&cc.published, "published", "", "path to the lineage as it was published")
	checkLineageCmd.RunE = cc.run
}

func (cc *checkCommand) run(cmd *cobra.Command, _ []string) error {
	if cc.published == "" {
		return diag.New(diag.E0620).Headline("no published lineage was given").
			Help("pass the lineage as it was published with --published").Err()
	}
	// The published lineage is loaded first, and its directory then dropped
	// from the excerpts' sources: both lineages may position their files by
	// the same relative paths, and every diagnostic of the check is the
	// candidate's.
	pla := &lineageLoadArgs{inputLinFilePath: cc.published, lincuepath: cc.lla.lincuepath}
	n := len(sourceDirs)
	if err := pla.validateLineageInput(cmd, nil); err != nil {
		return err
	}
	sourceDirs = sourceDirs[:n]
	if err := cc.lla.validateLineageInput(cmd, nil); err != nil {
		return err
	}
	return thema.CheckAppendOnly(pla.dl.lin, cc.lla.dl.lin)
}
