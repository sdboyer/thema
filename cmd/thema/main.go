package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue/cuecontext"
	"github.com/spf13/cobra"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/embedreg"
)

var ctx = cuecontext.New()
var rt = thema.NewRuntime(ctx)

// diagFormat is how diagnostics are written: text to stderr, or json to stdout.
var diagFormat string

func main() {
	setupDataCommand(rootCmd)
	setupLineageCommand(rootCmd)

	rootCmd.PersistentFlags().StringVar(&diagFormat, "diag-format", "text", `how diagnostics are reported: "text" on stderr, or "json" on stdout`)

	// Stop cobra from being so "helpful"
	for _, cmd := range allCmds {
		cmd.DisableFlagsInUseLine = true
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		cmd.CompletionOptions = cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		}
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(report(err))
	}
}

// silent is a failure a command has already said everything about, or was
// asked (-q) to say nothing about. It exits 1 with no diagnostics.
type silent struct{}

func (silent) Error() string { return "" }

// report writes err's diagnostics in the chosen format and returns the exit
// status. Every error is diagnostics: one thema's code produced carries its
// own, and anything else is converted, so that nothing reaches the user as
// bare text.
func report(err error) int {
	if errors.Is(err, silent{}) {
		return 1
	}
	ds := diag.From(err, diag.Context{})
	switch diagFormat {
	case "json":
		if err := diag.RenderJSON(os.Stdout, ds); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	default:
		cwd, _ := os.Getwd()
		diag.Render(os.Stderr, ds, diag.RenderOptions{Source: readSource, Rel: cwd})
	}
	return 1
}

// inlineSources holds text that was read from somewhere diagnostics cannot
// reread, such as data on stdin, under the name its positions carry.
var inlineSources = map[string][]byte{}

// sourceDirs are directories that relative positions may be relative to,
// besides the working directory: a lineage in the authoring form positions its
// errors by paths relative to its own directory.
var sourceDirs []string

// readSource reads a file for an excerpt: thema's own CUE from the embedded
// module, text a command read from stdin from memory, anything else from disk.
func readSource(file string) ([]byte, bool) {
	if rel, ok := embedreg.KernelPath(file); ok {
		b, err := thema.CueFS.ReadFile(rel)
		return b, err == nil
	}
	if b, ok := inlineSources[file]; ok {
		return b, true
	}
	if b, err := os.ReadFile(file); err == nil {
		return b, true
	}
	if !filepath.IsAbs(file) {
		for _, dir := range sourceDirs {
			if b, err := os.ReadFile(filepath.Join(dir, file)); err == nil {
				return b, true
			}
		}
	}
	return nil, false
}

// displayPath is path as a diagnostic names it: relative to the working
// directory when it lies beneath it.
func displayPath(path string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func addLinPathVars(cmd *cobra.Command, lla *lineageLoadArgs) {
	cmd.PersistentFlags().StringVarP(&lla.inputLinFilePath, "lineage", "l", ".", "path to .cue file or package containing lineage")
	cmd.MarkFlagRequired("lineage")
	cmd.PersistentFlags().StringVarP(&lla.lincuepath, "path", "p", "", "CUE expression for path to the lineage object within file, if not root")
}

// List of all commands, for batching stuff
var allCmds = []*cobra.Command{
	rootCmd,
	dataCmd,
	translateCmd,
	validateCmd,
	validateAnyCmd,
	linCmd,
	initLineageCmd,
	initLineageEmptyCmd,
	initLineageOpenAPICmd,
	initLineageJSONSchemaCmd,
	lineageBumpCmd,
	fmtLineageCmd,
	genLineageCmd,
	genTSTypesLineageCmd,
	genGoBindingsLineageCmd,
	genOapiLineageCmd,
	genJschLineageCmd,
}

var rootCmd = &cobra.Command{
	Use:   "thema <command>",
	Short: "A tool for putting Thema lineages to work",
	Long: `A tool for putting Thema lineages to work.

This program offers several kinds of behavior for working with Thema:

* Validating and inspecting of written lineages.
* Given a valid lineage, provides basic Thema operations (validate, translate,
  [de]hydrate) on some input data.
* Run an HTTP server that exposes basic Thema operations to the network. (TODO)
* Provides scaffolding for writing lineages, lenses, and schema. (TODO)

Every failure is reported as a diagnostic with a code; docs/errors in thema's
repository explains each code. --diag-format json emits them as JSON.
`,
}

type cobraefunc func(cmd *cobra.Command, args []string) error

func mergeCobraefuncs(f ...cobraefunc) cobraefunc {
	return func(cmd *cobra.Command, args []string) error {
		for _, fun := range f {
			if err := fun(cmd, args); err != nil {
				return err
			}
		}

		return nil
	}
}
