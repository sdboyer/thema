package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/sdboyer/thema/cmd/thema/playground"
	"github.com/sdboyer/thema/diag"
)

var serveCmd = &cobra.Command{
	Use:   "serve <command>",
	Short: "Serve thema's web tools locally",
}

var servePlaygroundCmd = &cobra.Command{
	Use:   "playground [--addr <host:port>] [--open] [--examples <dir>]",
	Short: "Serve the thema playground",
	Long: `Serve the thema playground: an editor for lineages in either form, with
validation, translation and code generation, running thema compiled to
WebAssembly in the browser. Nothing is sent to the server but requests for the
playground's own files.

The playground served is the one this thema was built with, so its behavior
matches this CLI's.

--examples offers the lineages in a directory as examples beside thema's own,
served as /examples.json, which the playground registers when it starts. The
directory is read once, at start-up; thema playground examples --help says
how it is laid out.
`,
	Args: cobra.NoArgs,
}

type serveCommand struct {
	addr     string
	open     bool
	examples string
}

func setupServeCommand(cmd *cobra.Command) {
	cmd.AddCommand(serveCmd)
	sc := new(serveCommand)
	serveCmd.AddCommand(servePlaygroundCmd)
	servePlaygroundCmd.Flags().StringVar(&sc.addr, "addr", "127.0.0.1:8080", "address to listen on")
	servePlaygroundCmd.Flags().BoolVar(&sc.open, "open", false, "open the playground in a browser")
	servePlaygroundCmd.Flags().StringVar(&sc.examples, "examples", "", "directory of lineages to offer as examples")
	servePlaygroundCmd.RunE = sc.run
}

func (sc *serveCommand) run(cmd *cobra.Command, _ []string) error {
	// The examples are read before listening, so that a problem with them is
	// reported before the playground's address is.
	var manifest []byte
	if sc.examples != "" {
		var err error
		if manifest, err = examplesManifest(sc.examples); err != nil {
			return err
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
