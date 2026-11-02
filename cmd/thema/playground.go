package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sdboyer/thema/diag"
	tplayground "github.com/sdboyer/thema/playground"
)

var playgroundCmd = &cobra.Command{
	Use:   "playground <command>",
	Short: "Prepare inputs for a deployment of the thema playground",
}

var playgroundExamplesCmd = &cobra.Command{
	Use:   "examples <dir>",
	Short: "Print the examples a directory of lineages offers the playground",
	Long: `Print the examples a directory of lineages offers the playground, as the
examples.json the playground's frontend looks for beside thema.wasm.

Every directory beneath <dir> that holds version files, a tip.thema or a
history/ of frozen versions, is a lineage in the authoring form. The playground
shows its tip as the one file to edit, and its history read-only. It is an
example when it also holds an example.cue, which describes it:

    category: "Pets"            // required: the group it is listed in
    title:    "dog"             // optional: the lineage's name if absent
    summary:  "A dog's record." // optional

A lineage without an example.cue is not offered. Each example's ID is its
directory's path beneath <dir>; whether it binds is found by binding it with
its tip, as the playground does, and its data documents are its schemas'
examples.

A host that serves the playground's files statically generates examples.json
with this command; thema serve playground --examples <dir> serves the same.
`,
	Args: cobra.ArbitraryArgs,
}

func setupPlaygroundCommand(cmd *cobra.Command) {
	cmd.AddCommand(playgroundCmd)
	playgroundCmd.AddCommand(playgroundExamplesCmd)
	playgroundExamplesCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return diag.New(diag.E0620).Headlinef("%d arguments were given, where the examples directory is one path", len(args)).
				Help("pass the directory that holds the examples' lineages").Err()
		}
		manifest, err := examplesManifest(args[0])
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(manifest)
		return err
	}
}

// examplesManifest walks dir for the playground's examples, and returns them
// as examples.json.
func examplesManifest(dir string) ([]byte, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, diag.New(diag.E0601).Headlinef("%s cannot be resolved to a path", dir).Detail(diag.OSReason(err)).Err()
	}
	// Positions name files relative to dir's parent.
	parent := filepath.Dir(abs)
	sourceDirs = append(sourceDirs, parent)
	req, err := tplayground.WalkExamples(os.DirFS(parent), filepath.Base(abs))
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return nil, diag.InternalErr(fmt.Errorf("encoding the examples of %s: %w", dir, err))
	}
	return append(b, '\n'), nil
}
