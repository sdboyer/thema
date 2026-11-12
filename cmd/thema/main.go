package main

import (
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue/cuecontext"
	"github.com/sdboyer/jenny"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/embedreg"
)

var ctx = cuecontext.New()
var rt = thema.NewRuntime(ctx)

// diagFormat is how diagnostics are written: text to stderr, or json to stdout.
var diagFormat string

func main() {
	setupCommands()

	if cmd, err := unknownCommand(rootCmd, os.Args[1:]); err != nil {
		os.Exit(report(commandLine(cmd, err)))
	}
	if cmd, err := rootCmd.ExecuteC(); err != nil {
		os.Exit(report(commandLine(cmd, err)))
	}
}

// setupCommands builds the command tree beneath rootCmd, as main executes it.
func setupCommands() {
	setupDataCommand(rootCmd)
	setupLineageCommand(rootCmd)
	setupServeCommand(rootCmd)

	rootCmd.PersistentFlags().StringVar(&diagFormat, "diag-format", "text", `how diagnostics are reported: "text" on stderr, or "json" on stdout`)

	// Stop cobra from being so "helpful"
	walkCommands(rootCmd, func(cmd *cobra.Command) {
		cmd.DisableFlagsInUseLine = true
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		cmd.CompletionOptions = cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		}
	})
	// cobra adds these during Execute, after markRan; add them now so that
	// markRan reaches their hooks too.
	rootCmd.InitDefaultHelpCmd()
	rootCmd.InitDefaultCompletionCmd()
	markRan(rootCmd)
}

// walkCommands calls f on cmd and on every command beneath it.
func walkCommands(cmd *cobra.Command, f func(*cobra.Command)) {
	f(cmd)
	for _, sub := range cmd.Commands() {
		walkCommands(sub, f)
	}
}

// readDiagFormat sets diagFormat from args alone, ignoring every other flag
// and argument. cobra stops reading a command line at what it cannot take,
// so --diag-format after that point is never set, yet the diagnostic that
// says so must be written as it asks.
func readDiagFormat(args []string) {
	fs := pflag.NewFlagSet("diag-format", pflag.ContinueOnError)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	fs.StringVar(&diagFormat, "diag-format", diagFormat, "")
	// Declared, or pflag stops at -h or --help as a request for help.
	fs.BoolP("help", "h", false, "")
	_ = fs.Parse(args)
}

// ran is an error a command's own code returned, as against one cobra
// returned for the command line before any of that code ran.
type ran struct{ err error }

func (r ran) Error() string { return r.err.Error() }
func (r ran) Unwrap() error { return r.err }

// markRan wraps the hooks of cmd and every command beneath it so that every
// error they return is marked as ran.
func markRan(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		markRan(sub)
	}
	for _, h := range []*cobraefunc{(*cobraefunc)(&cmd.PersistentPreRunE), (*cobraefunc)(&cmd.PreRunE), (*cobraefunc)(&cmd.RunE), (*cobraefunc)(&cmd.PostRunE), (*cobraefunc)(&cmd.PersistentPostRunE)} {
		if f := *h; f != nil {
			*h = func(cmd *cobra.Command, args []string) error {
				if err := f(cmd, args); err != nil {
					return ran{err}
				}
				return nil
			}
		}
	}
}

// unknownCommand is the error cobra gives for a word that names no command at
// the root, for such a word beneath a command that only groups others, where
// cobra prints the group's help and succeeds without reading its arguments.
// It returns no error for anything else, which cobra reports itself.
func unknownCommand(root *cobra.Command, args []string) (*cobra.Command, error) {
	cmd, rest, err := root.Find(args)
	if err != nil || !cmd.HasParent() || cmd.Runnable() || cmd.ParseFlags(rest) != nil {
		return nil, nil
	}
	words := cmd.Flags().Args()
	if len(words) == 0 {
		return nil, nil
	}
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	var suggested string
	if s := cmd.SuggestionsFor(words[0]); len(s) > 0 {
		suggested = "\n\nDid you mean this?\n\t" + strings.Join(s, "\n\t") + "\n"
	}
	return cmd, fmt.Errorf("unknown command %q for %q%s", words[0], cmd.CommandPath(), suggested)
}

// commandLine is err as the CLI reports it: what a command's own code
// returned, as it returned it, and anything else, which cobra returned for a
// command line it could not take (an unknown command or flag, a flag without
// its value, too many arguments), as E0620, with cobra's account of it.
func commandLine(cmd *cobra.Command, err error) error {
	var r ran
	if errors.As(err, &r) {
		return r.err
	}
	readDiagFormat(os.Args[1:])
	b := diag.New(diag.E0620).Headlinef("%s cannot take this command line", cmd.CommandPath())
	// An unknown command's message ends with the commands cobra suggests,
	// which read as help, not as part of what went wrong.
	msg, suggested, ok := strings.Cut(err.Error(), "\n\nDid you mean this?\n")
	b = b.Detail(msg)
	if ok {
		b = b.Help("did you mean " + strings.Join(strings.Fields(suggested), " or ") + "?")
	}
	return b.Help(fmt.Sprintf("%s --help lists the commands, flags and arguments it takes", cmd.CommandPath())).Err()
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

// warn writes the diagnostics in err, warnings of an operation that
// succeeded, to stderr as text, where they stay apart from the command's
// output in either format.
func warn(err error) {
	if err == nil {
		return
	}
	cwd, _ := os.Getwd()
	diag.Render(os.Stderr, diag.From(err, diag.Context{}), diag.RenderOptions{Source: readSource, Rel: cwd})
}

// inlineSources holds text that was read from somewhere diagnostics cannot
// reread, such as data on stdin, under the name its positions carry.
var inlineSources = map[string][]byte{}

// sourceDirs are directories that relative positions may be relative to,
// besides the working directory: a lineage in the authoring form outside the
// working directory positions its errors relative to its parent (positionDir).
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

// positionDir is the directory the files of the authoring lineage in abs are
// positioned under (authoring.At), and the directory those positions are
// relative to. Where the lineage lies beneath the working directory, it is
// the lineage's path from there, so that a diagnostic names a file as the
// command was given it, my-ship/tip.thema, relative to the working directory.
// No fs.FS path reaches a lineage elsewhere, and its files are positioned
// under its base name, relative to its parent.
func positionDir(abs string) (dir, root string) {
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, abs); err == nil && filepath.IsLocal(rel) {
			return filepath.ToSlash(rel), cwd
		}
	}
	return filepath.Base(abs), filepath.Dir(abs)
}

// loadAuthoring reads the authoring files of the lineage in abs, and returns
// the option that positions them as positionDir says. The directory those
// positions are relative to becomes one of the excerpts' sourceDirs.
func loadAuthoring(abs string) (name string, files []authoring.File, at authoring.Option, err error) {
	dir, root := positionDir(abs)
	sourceDirs = append(sourceDirs, root)
	name, files, err = authoring.Load(os.DirFS(filepath.Dir(abs)), filepath.Base(abs))
	return name, files, authoring.At(dir), err
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

// writeFS writes the generated files under dir. A file that cannot be
// written is E0616, naming the path that failed.
func writeFS(cmd *cobra.Command, fs *jenny.FS, dir string) error {
	err := fs.Write(cmd.Context(), dir)
	if err == nil {
		return nil
	}
	p := dir
	var pe *iofs.PathError
	if errors.As(err, &pe) {
		p = pe.Path
	}
	return diag.New(diag.E0616).Headlinef("%s cannot be written", displayPath(p)).Detail(diag.OSReason(err)).Err()
}

func addLinPathVars(cmd *cobra.Command, lla *lineageLoadArgs) {
	cmd.PersistentFlags().StringVarP(&lla.inputLinFilePath, "lineage", "l", ".", "path to .cue file or package containing lineage")
	cmd.MarkFlagRequired("lineage")
	cmd.PersistentFlags().StringVarP(&lla.lincuepath, "path", "p", "", "CUE expression for path to the lineage object within file, if not root")
}

var rootCmd = &cobra.Command{
	Use:   "thema <command>",
	Short: "A tool for putting Thema lineages to work",
	Long: `A tool for putting Thema lineages to work.

This program offers several kinds of behavior for working with Thema:

* Validating and inspecting of written lineages.
* Given a valid lineage, provides basic Thema operations (validate, translate,
  [de]hydrate) on some input data.
* Serve the thema playground locally (thema serve playground), and export
  a directory of lineages as examples for a static deployment of it (thema
  serve playground --examples <dir> --export <out>).
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
