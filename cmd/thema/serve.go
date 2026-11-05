package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/sdboyer/jenny"
	"github.com/spf13/cobra"

	"github.com/sdboyer/thema/cmd/thema/playground"
	"github.com/sdboyer/thema/diag"
	tplayground "github.com/sdboyer/thema/playground"
)

var serveCmd = &cobra.Command{
	Use:   "serve <command>",
	Short: "Serve thema's web tools locally",
}

var servePlaygroundCmd = &cobra.Command{
	Use:   "playground [--addr <host:port>] [--open] [--examples <dir> [--export <dir> [--check]]]",
	Short: "Serve the thema playground, or export what a static host of it serves",
	Long: `Serve the thema playground: an editor for lineages in either form, with
validation, translation and code generation, running thema compiled to
WebAssembly in the browser. Nothing is sent to the server but requests for the
playground's own files.

The playground served is the one this thema was built with, so its behavior
matches this CLI's.

--examples offers the lineages in a directory as examples beside thema's own,
served as /examples.json, which the playground registers when it starts. The
directory is read once, at start-up. Every directory beneath it that holds
version files, a tip.thema or a history/ of frozen versions, is a lineage in
the authoring form. The playground shows its tip as the one file to edit, and
its history read-only. It is an example when it also holds an example.cue,
which describes it:

    category: "Pets"            // required: the group it is listed in
    title:    "dog"             // optional: the lineage's name if absent
    summary:  "A dog's record." // optional

A lineage without an example.cue is not offered. Each example's ID is its
directory's path beneath the directory; whether it binds is found by binding
it with its tip, as the playground does, and its data documents are its
schemas' examples.

--export <dir> serves nothing. It writes into <dir> the files a host that
serves the playground statically publishes beside thema.wasm, and exits:
examples.json, so it needs --examples. With --check, nothing is written, and
the command fails for each file under <dir> that is missing or is not what
would be written.
`,
	Args: cobra.NoArgs,
}

type serveCommand struct {
	addr     string
	open     bool
	examples string
	export   string
	check    bool
}

func setupServeCommand(cmd *cobra.Command) {
	cmd.AddCommand(serveCmd)
	sc := new(serveCommand)
	serveCmd.AddCommand(servePlaygroundCmd)
	servePlaygroundCmd.Flags().StringVar(&sc.addr, "addr", "127.0.0.1:8080", "address to listen on")
	servePlaygroundCmd.Flags().BoolVar(&sc.open, "open", false, "open the playground in a browser")
	servePlaygroundCmd.Flags().StringVar(&sc.examples, "examples", "", "directory of lineages to offer as examples")
	servePlaygroundCmd.Flags().StringVar(&sc.export, "export", "", "write the files a static host serves into this directory, and serve nothing")
	servePlaygroundCmd.Flags().BoolVar(&sc.check, "check", false, "Only with --export: write nothing, and fail if the files under the directory are not what would be written")
	servePlaygroundCmd.RunE = sc.run
}

func (sc *serveCommand) run(cmd *cobra.Command, _ []string) error {
	if sc.export != "" || sc.check {
		return sc.runExport(cmd)
	}
	// The examples are read before listening, so that a problem with them is
	// reported before the playground's address is.
	var manifest []byte
	if sc.examples != "" {
		fs, err := examplesManifest(sc.examples)
		if err != nil {
			return err
		}
		if manifest, err = fs.ReadFile(tplayground.ManifestFile); err != nil {
			return diag.InternalErr(err)
		}
	}
	ln, err := net.Listen("tcp", sc.addr)
	if err != nil {
		return diag.New(diag.E0618).Headlinef("cannot listen on %s", sc.addr).Detail(err.Error()).
			Help("choose another address with --addr").Err()
	}
	bundle, err := playground.Bundle()
	if err != nil {
		_ = ln.Close()
		if errors.Is(err, playground.ErrNotBuilt) {
			return diag.New(diag.E0617).Headline("this thema was built without the playground").
				Help("build it in: go generate ./cmd/thema/playground && go build -tags playgroundui ./cmd/thema").Err()
		}
		return diag.InternalErr(err)
	}
	url := fmt.Sprintf("http://%s/", ln.Addr())
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "thema playground at %s\n", url)
	if sc.open {
		openBrowser(url)
	}
	return http.Serve(ln, servePlaygroundHandler(bundle, manifest)) //nolint:gosec // a local tool serving static files
}

// runExport writes, or with --check verifies, what a static host of the
// playground serves beside thema.wasm.
func (sc *serveCommand) runExport(cmd *cobra.Command) error {
	switch {
	case sc.export == "":
		return diag.New(diag.E0620).Headline("--check verifies the files under --export, and no --export was given").Err()
	case sc.examples == "":
		return diag.New(diag.E0620).Headline("--export writes the examples --examples gives, and no --examples was given").
			Help("pass the directory of lineages to offer with --examples").Err()
	}
	for _, f := range []string{"addr", "open"} {
		if cmd.Flags().Changed(f) {
			return diag.New(diag.E0620).Headlinef("--export serves nothing, and --%s says how to serve", f).
				Help("drop --" + f + ", or --export to serve the playground").Err()
		}
	}
	fs, err := examplesManifest(sc.examples)
	if err != nil {
		return err
	}
	if sc.check {
		return driftDiagnostics(fs.Verify(cmd.Context(), sc.export), "--export", sc.export)
	}
	return writeFS(cmd, fs, sc.export)
}

// examplesManifest walks dir for the playground's examples, and returns the
// manifest of them a host serves.
func examplesManifest(dir string) (*jenny.FS, error) {
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
	fs, err := tplayground.Manifest.GenerateFS(req)
	if err != nil {
		return nil, diag.InternalErr(fmt.Errorf("encoding the examples of %s: %w", dir, err))
	}
	return fs, nil
}

// servePlaygroundHandler serves bundle, and manifest as /examples.json when
// there is one.
func servePlaygroundHandler(bundle fs.FS, manifest []byte) http.Handler {
	h := playground.Handler(bundle)
	if manifest == nil {
		return h
	}
	return playground.WithExamples(h, manifest)
}

// openBrowser opens url in the user's browser, if the platform says how.
// Failing to is not an error: the URL is printed.
func openBrowser(url string) {
	name, args := "xdg-open", []string{url}
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	}
	_ = exec.Command(name, args...).Start() //nolint:gosec // the URL is this process's own listen address
}
