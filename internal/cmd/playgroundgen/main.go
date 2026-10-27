// Command playgroundgen assembles the playground bundle that thema serve
// playground embeds: the frontend, and this thema's own thema.wasm and the
// JavaScript that loads it. It runs as go generate in cmd/thema/playground:
//
//	go generate ./cmd/thema/playground
//
// The frontend is pinned in cmd/thema/playground/VERSION, as a release tag of
// sdboyer/thema-playground and the SHA-256 of its tarball:
//
//	v0.1.0 4f9c…e1
//
// or as "placeholder", for a minimal page, embedded here, that calls thema's
// operations directly. The bundle is written to -out, which is emptied first:
//
//	index.html, …     the frontend (or the placeholder's page and worker)
//	thema.wasm        cmd/thema-wasm, built as a release builds it
//	thema.wasm.gz     the same, compressed, which serve prefers
//	wasm_exec.js      Go's loader for thema.wasm, from the Go that built it
//	thema-fs.js       the filesystem thema.wasm needs in a browser
//	types.ts          the wire format's TypeScript declarations
//	MANIFEST.json     the versions the bundle was built from
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sdboyer/thema/cmd/thema/playground"
	"github.com/sdboyer/thema/internal/util"
)

//go:embed placeholder
var placeholder embed.FS

const releaseURL = "https://github.com/sdboyer/thema-playground/releases/download/%[1]s/thema-playground-%[1]s.tar.gz"

// The bundle is go generate's to write and go:embed's to read; its files are
// the generator's own.
const (
	dirPerm  = 0o750
	filePerm = 0o600
)

// fetchTimeout bounds downloading a frontend release.
const fetchTimeout = 2 * time.Minute

// maxEntrySize bounds one file of a frontend release. An entry claiming more
// is not a frontend file.
const maxEntrySize = 64 << 20

func main() {
	out := flag.String("out", "dist", "directory to write the bundle to; emptied first")
	version := flag.String("version", "VERSION", "file pinning the frontend release")
	flag.Parse()
	if err := run(*out, *version); err != nil {
		fmt.Fprintln(os.Stderr, "playgroundgen:", err)
		os.Exit(1)
	}
}

func run(out, versionFile string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	if out, err = filepath.Abs(out); err != nil {
		return err
	}
	pin, err := os.ReadFile(versionFile) //nolint:gosec // the pin file named by -version
	if err != nil {
		return err
	}
	if err = os.RemoveAll(out); err != nil {
		return err
	}
	if err = os.MkdirAll(out, dirPerm); err != nil {
		return err
	}

	m := playground.Manifest{ThemaVersion: util.ThemaModuleVersion}
	tag, digest, pinned := strings.Cut(strings.TrimSpace(string(pin)), " ")
	switch {
	case !pinned && tag == "placeholder":
		m.UI = tag
		if err = copyFS(out, placeholder, "placeholder"); err != nil {
			return err
		}
	case pinned:
		m.UI = tag
		if err = fetchUI(out, tag, digest); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%s holds %q; want \"<tag> <sha256>\" or \"placeholder\"", versionFile, tag)
	}

	wasm := filepath.Join(out, "thema.wasm")
	build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", wasm, "./cmd/thema-wasm") //nolint:gosec // builds thema.wasm into -out
	build.Dir = root
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	build.Stderr = os.Stderr
	if err = build.Run(); err != nil {
		return fmt.Errorf("building thema.wasm: %w", err)
	}
	b, err := os.ReadFile(wasm) //nolint:gosec // the file go build just wrote
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	m.WasmSHA256 = hex.EncodeToString(sum[:])
	if err = writeGzip(wasm+".gz", b); err != nil {
		return err
	}

	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return fmt.Errorf("go env GOROOT: %w", err)
	}
	copies := map[string]string{
		"wasm_exec.js": filepath.Join(strings.TrimSpace(string(goroot)), "lib", "wasm", "wasm_exec.js"),
		"thema-fs.js":  filepath.Join(root, "cmd", "thema-wasm", "thema-fs.js"),
		"types.ts":     filepath.Join(root, "playground", "types.ts"),
	}
	for name, src := range copies {
		if err = copyFile(filepath.Join(out, name), src); err != nil {
			return err
		}
	}

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

// fetchUI downloads the frontend release tag, checks it against its pinned
// digest, and unpacks it into out.
func fetchUI(out, tag, want string) error {
	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Get(fmt.Sprintf(releaseURL, tag))
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // read to the end below
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching thema-playground %s: %s", tag, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("thema-playground %s has digest %s, but VERSION pins %s", tag, got, want)
	}
	return untar(out, bytes.NewReader(b))
}

// untar unpacks a gzipped tarball of regular files and directories into out,
// refusing any entry that would land outside it.
func untar(out string, r io.Reader) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(h.Name))
		if !filepath.IsLocal(name) {
			return fmt.Errorf("the tarball's entry %q is outside its root", h.Name)
		}
		dst := filepath.Join(out, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, dirPerm); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
				return err
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, filePerm) //nolint:gosec // name is checked to be local above
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, maxEntrySize)); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("the tarball's entry %q is not a file or directory", h.Name)
		}
	}
}

func copyFS(out string, fsys fs.FS, dir string) error {
	return fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, dir+"/")
		return os.WriteFile(filepath.Join(out, filepath.FromSlash(rel)), b, filePerm)
	})
}

func copyFile(dst, src string) error {
	b, err := os.ReadFile(src) //nolint:gosec // a file of this module or of the Go that built it
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, filePerm) //nolint:gosec // one of the bundle's fixed names, under -out
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
