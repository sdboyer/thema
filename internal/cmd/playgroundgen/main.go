// Command playgroundgen assembles the playground bundle that thema serve
// playground embeds: the frontend in playground/ui, built, and this thema's
// own thema.wasm and the JavaScript that loads it. It runs as go generate in
// cmd/thema/playground:
//
//	go generate ./cmd/thema/playground
//
// It needs node, with the frontend's dependencies installed (npm ci in
// playground/ui). thema.wasm and its loaders go first to playground/ui/public,
// where the frontend's dev server and tests find them, so that one build
// serves both; the frontend's build then writes the bundle to -out, which is
// emptied first, and the rest is added:
//
//	index.html, …     the frontend
//	thema-worker.js   the frontend's worker, which runs thema.wasm
//	thema.wasm        cmd/thema-wasm, built as a release builds it
//	thema.wasm.gz     the same, compressed, which serve prefers
//	wasm_exec.js      Go's loader for thema.wasm, from the Go that built it
//	thema-fs.js       the filesystem thema.wasm needs in a browser
//	types.ts          the wire format's TypeScript declarations
//	MANIFEST.json     the versions the bundle was built from
package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sdboyer/thema/cmd/thema/playground"
	"github.com/sdboyer/thema/internal/util"
)

// filePerm is for the files this program writes, which only go generate
// writes and only go:embed reads.
const filePerm = 0o600

func main() {
	out := flag.String("out", "dist", "directory to write the bundle to; emptied first")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "playgroundgen:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	if out, err = filepath.Abs(out); err != nil {
		return err
	}
	ui := filepath.Join(root, "playground", "ui")
	if _, err = os.Stat(filepath.Join(ui, "node_modules")); err != nil {
		return fmt.Errorf("the frontend's dependencies are not installed: run npm ci in %s", ui)
	}

	// thema's side, beside the frontend's worker: the dev server and the
	// tests find it here, and the frontend's build copies it into the bundle.
	pub := filepath.Join(ui, "public")
	wasm := filepath.Join(pub, "thema.wasm")
	build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", wasm, "./cmd/thema-wasm") //nolint:gosec // builds thema.wasm into the frontend's public directory
	build.Dir = root
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	build.Stderr = os.Stderr
	if err = build.Run(); err != nil {
		return fmt.Errorf("building thema.wasm: %w", err)
	}
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return fmt.Errorf("go env GOROOT: %w", err)
	}
	if err = copyFile(filepath.Join(pub, "wasm_exec.js"), filepath.Join(strings.TrimSpace(string(goroot)), "lib", "wasm", "wasm_exec.js")); err != nil {
		return err
	}
	if err = copyFile(filepath.Join(pub, "thema-fs.js"), filepath.Join(root, "cmd", "thema-wasm", "thema-fs.js")); err != nil {
		return err
	}

	// The frontend, built into the bundle. vite copies public/ along with it.
	npm := exec.Command("npm", "run", "build", "--", "--outDir", out, "--emptyOutDir") //nolint:gosec // out is -out
	npm.Dir = ui
	npm.Stdout = os.Stderr
	npm.Stderr = os.Stderr
	if err = npm.Run(); err != nil {
		return fmt.Errorf("building the frontend: %w", err)
	}

	b, err := os.ReadFile(wasm) //nolint:gosec // the file go build just wrote
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	if err = writeGzip(filepath.Join(out, "thema.wasm.gz"), b); err != nil {
		return err
	}
	if err = copyFile(filepath.Join(out, "types.ts"), filepath.Join(ui, "src", "api", "types.ts")); err != nil {
		return err
	}
	m := playground.Manifest{ThemaVersion: util.ThemaModuleVersion, WasmSHA256: hex.EncodeToString(sum[:])}
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "MANIFEST.json"), append(mb, '\n'), filePerm)
}

// moduleRoot is the directory of thema's go.mod.
func moduleRoot() (string, error) {
	b, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	gomod := strings.TrimSpace(string(b))
	if gomod == "" || gomod == os.DevNull {
		return "", errors.New("not inside thema's module")
	}
	return filepath.Dir(gomod), nil
}

func copyFile(dst, src string) error {
	b, err := os.ReadFile(src) //nolint:gosec // a file of this module or of the Go that built it
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, filePerm) //nolint:gosec // one of the bundle's fixed names
}

func writeGzip(dst string, b []byte) error {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return err
	}
	if _, err := zw.Write(b); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(dst, buf.Bytes(), filePerm)
}
