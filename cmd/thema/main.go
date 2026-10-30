package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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
	setupServeCommand(rootCmd)
	setupPlaygroundCommand(rootCmd)

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
		if err := diag.RenderJSON(os.Stdout, displayedJSON(ds)); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	default:
		diag.Render(os.Stderr, ds, renderOptions)
	}
	return 1
}

// warn writes the diagnostics in err, warnings of an operation that
// succeeded, to stderr as text, where they stay apart from the command's
// output in either format.
func warn(err error) {
	if err == nil {
		return
	}
	diag.Render(os.Stderr, diag.From(err, diag.Context{}), renderOptions)
}

// renderOptions renders a diagnostic's excerpts from where its positions say:
// each by the name the position carries, and shown as the user knows it.
var renderOptions = diag.RenderOptions{Source: readSource, Display: displayFile}

// displayedJSON returns ds with each position in the workspace named as the
// user knows the file ([workspace.display]), for tools that open it.
func displayedJSON(ds []diag.Diagnostic) []diag.Diagnostic {
	show := func(p *diag.Pos) {
		if p != nil && isName(p.File) {
			p.File = ws.display(p.File)
		}
	}
	for i := range ds {
		show(ds[i].Primary)
		for j := range ds[i].Notes {
			show(ds[i].Notes[j].Pos)
		}
	}
	return ds
}

// inlineSources holds text that was read from somewhere other than the
// workspace, such as data on stdin, under the name its positions carry.
var inlineSources = map[string][]byte{}

// readSource reads a file for an excerpt, by the name its position carries:
// thema's own CUE from the embedded module, text a command read from stdin
// from memory, and anything else from the workspace. It never reads the
// host's filesystem but through the workspace.
func readSource(file string) ([]byte, bool) {
	if rel, ok := embedreg.KernelPath(file); ok {
		b, err := thema.CueFS.ReadFile(rel)
		return b, err == nil
	}
	if b, ok := inlineSources[file]; ok {
		return b, true
	}
	if !isName(file) {
		return nil, false
	}
	b, err := fs.ReadFile(ws.fsys, file)
	return b, err == nil
}

// displayFile shows the file a position names: thema's own CUE as
// thema/<file>, and a workspace file as the user knows it.
func displayFile(file string) string {
	if rel, ok := embedreg.KernelPath(file); ok {
		return "thema/" + rel
	}
	if _, inline := inlineSources[file]; inline || !isName(file) {
		return file
	}
	return ws.display(file)
}

// displayPath is a workspace file's name as a message shows it.
func displayPath(name string) string {
	if ws == nil {
		return name
	}
	return ws.display(name)
}

// displayHostPath shows a path on the host, for the code generators, which
// write beside the lineage and read its module's go.mod there: relative to
// the working directory when it lies beneath it.
func displayHostPath(p string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return p
	}
	if rel, ok := within(filepath.Clean(cwd), p); ok {
		return filepath.FromSlash(rel)
	}
	return p
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
	tipLineageCmd,
	compileLineageCmd,
	genLineageCmd,
	genTSTypesLineageCmd,
	genGoBindingsLineageCmd,
	genOapiLineageCmd,
	genJschLineageCmd,
	serveCmd,
	servePlaygroundCmd,
	playgroundCmd,
	playgroundExamplesCmd,
}

var rootCmd = &cobra.Command{
	Use:   "thema <command>",
	Short: "A tool for putting Thema lineages to work",
	Long: `A tool for putting Thema lineages to work.

This program offers several kinds of behavior for working with Thema:

* Validating and inspecting of written lineages.
* Given a valid lineage, provides basic Thema operations (validate, translate,
  [de]hydrate) on some input data.
* Serve the thema playground locally (thema serve playground), and prepare
  a directory of lineages as examples for a deployment of it (thema
  playground examples).
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
