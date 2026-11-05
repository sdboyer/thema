package main

import (
	"github.com/spf13/cobra"
)

var linCmd = &cobra.Command{
	Use:   "lineage <command>",
	Short: "Perform operations directly on lineages and schema",
	Long: `Perform operations directly on lineages and schema.

Create, modify, and validate Thema lineages. Generate representations
of Thema lineages and schema in other schema languages.
`,
}

func setupLineageCommand(cmd *cobra.Command) {
	cmd.AddCommand(linCmd)
	ic := new(initCommand)
	ic.setup(linCmd)

	gc := new(genCommand)
	gc.setup(linCmd)

	fc := new(fmtCommand)
	fc.setup(linCmd)

	tc := new(tipCommand)
	tc.setup(linCmd)

	cc := new(checkCommand)
	cc.setup(linCmd)

	cut := new(cutCommand)
	cut.setup(linCmd)

	compile := new(compileCommand)
	compile.setup(linCmd)
}
