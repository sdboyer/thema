package main

import (
	"errors"
	"fmt"
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
	Use:   "playground [--addr <host:port>] [--open]",
	Short: "Serve the thema playground",
	Long: `Serve the thema playground: an editor for lineages in either form, with
validation, translation and code generation, running thema compiled to
WebAssembly in the browser. Nothing is sent to the server but requests for the
playground's own files.

The playground served is the one this thema was built with, so its behaviour
matches this CLI's.
`,
	Args: cobra.NoArgs,
}

type serveCommand struct {
	addr string
	open bool
}

func setupServeCommand(cmd *cobra.Command) {
	cmd.AddCommand(serveCmd)
	sc := new(serveCommand)
	serveCmd.AddCommand(servePlaygroundCmd)
	servePlaygroundCmd.Flags().StringVar(&sc.addr, "addr", "127.0.0.1:8080", "address to listen on")
	servePlaygroundCmd.Flags().BoolVar(&sc.open, "open", false, "open the playground in a browser")
	servePlaygroundCmd.RunE = sc.run
}

func (sc *serveCommand) run(cmd *cobra.Command, _ []string) error {
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
	fmt.Fprintf(cmd.OutOrStdout(), "thema playground at %s\n", url)
	if sc.open {
		openBrowser(url)
	}
	return http.Serve(ln, playground.Handler(bundle)) //nolint:gosec // a local tool serving static files
}

// openBrowser opens url in the user's browser, if the platform says how.
// Failing to is not an error: the URL is printed.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
