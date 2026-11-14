package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sdboyer/thema/diag"
	tplayground "github.com/sdboyer/thema/playground"
)

// writeExamplesDir writes a directory of lineages, one of them an example,
// and returns it.
func writeExamplesDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "lineages")
	files := map[string]string{
		"pets/cat/history/0.0.thema":  "Schema: {\n\tlives: int\n}\n",
		"pets/cat/tip.thema":          "Schema: {\n\tlives: int\n}\n",
		"pets/cat/example.cue":        "category: \"Pets\"\nsummary: \"A cat.\"\n",
		"wip/draft/history/0.0.thema": "Schema: {\n\tx: int\n}\n",
		"wip/draft/tip.thema":         "Schema: {\n\tx: int\n}\n",
	}
	for name, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func codesOf(err error) []diag.Code {
	var codes []diag.Code
	for _, d := range diag.Diagnostics(err) {
		codes = append(codes, d.Code)
	}
	return codes
}

// Flags the serve tests repeat.
const (
	examplesFlag = "--examples"
	exportFlag   = "--export"
	checkFlag    = "--check"
)

// thema serve playground --examples <dir> --export <out> writes the examples
// the directory offers into out, as the register-examples request a static
// host serves, and serves nothing. --check then finds it up to date, and
// fails once it changes.
func TestServePlaygroundExport(t *testing.T) {
	examples := writeExamplesDir(t)
	out := filepath.Join(t.TempDir(), "site")
	export := []string{"serve", "playground", examplesFlag, examples, exportFlag, out}
	if _, errOut, err := runThema(t, t.TempDir(), "", export...); err != nil {
		t.Fatalf("--export: %v\n%s", err, errOut)
	}
	written := filepath.Join(out, tplayground.ManifestFile)
	b, err := os.ReadFile(written) //nolint:gosec // a path the test chose
	if err != nil {
		t.Fatal(err)
	}
	var req tplayground.RegisterExamplesRequest
	if jerr := json.Unmarshal(b, &req); jerr != nil {
		t.Fatalf("%s is not a register-examples request: %v\n%s", tplayground.ManifestFile, jerr, b)
	}
	if len(req.Examples) != 1 {
		t.Fatalf("wrote %d examples, want 1:\n%s", len(req.Examples), b)
	}
	if e := req.Examples[0].Example; e.ID != "pets/cat" || e.Category != "Pets" || e.Summary != "A cat." || !e.Binds {
		t.Errorf("wrote %+v", e)
	}

	// What is exported is what serving serves.
	served, err := (&serveCommand{examples: examples}).manifest()
	if err != nil {
		t.Fatal(diag.Diagnostics(err))
	}
	if !bytes.Equal(served, b) {
		t.Errorf("--export wrote other bytes than serving serves:\n%s", served)
	}

	check := slices.Concat(export, []string{checkFlag})
	if _, errOut, cerr := runThema(t, t.TempDir(), "", check...); cerr != nil {
		t.Errorf("--check of what --export just wrote: %v\n%s", cerr, errOut)
	}
	if err = os.WriteFile(written, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := runThema(t, t.TempDir(), "", check...)
	if err == nil || !strings.Contains(errOut, "error[E0628]") || !strings.Contains(errOut, "relative to --export") {
		t.Errorf("--check of a changed %s: got %v, want E0628 relative to --export:\n%s", tplayground.ManifestFile, err, errOut)
	}
}

// --export takes --examples, and none of the flags that say how to serve;
// --check takes --export. A problem with the examples directory is its own.
func TestServePlaygroundExportArgs(t *testing.T) {
	examples := writeExamplesDir(t)
	out := filepath.Join(t.TempDir(), "site")
	for _, tc := range []struct {
		name string
		args []string
		want diag.Code
	}{
		{"--export without --examples", []string{exportFlag, out}, diag.E0620},
		{"--export empty", []string{examplesFlag, examples, exportFlag, ""}, diag.E0620},
		{"--export empty with --check", []string{examplesFlag, examples, exportFlag + "=", checkFlag}, diag.E0620},
		{"--check without --export", []string{examplesFlag, examples, "--check"}, diag.E0620},
		{"--export with --addr", []string{examplesFlag, examples, exportFlag, out, "--addr", "127.0.0.1:0"}, diag.E0620},
		{"--export with --open", []string{examplesFlag, examples, exportFlag, out, "--open"}, diag.E0620},
		{"missing", []string{examplesFlag, filepath.Join(examples, "nope"), exportFlag, out}, diag.E0601},
		{"without example", []string{examplesFlag, filepath.Join(examples, "wip"), exportFlag, out}, diag.E0622},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errOut, err := runThema(t, t.TempDir(), "", append([]string{"serve", "playground"}, tc.args...)...)
			if err == nil || !strings.Contains(errOut, "error["+string(tc.want)+"]") {
				t.Errorf("got %v, want %s in:\n%s", err, tc.want, errOut)
			}
		})
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("a refused --export wrote %s", out)
	}
}

// thema serve playground --examples <dir> serves the directory's examples at
// /examples.json; without it, /examples.json is the bundle's to answer.
func TestServePlaygroundExamples(t *testing.T) {
	commandTree()
	if servePlaygroundCmd.Flags().Lookup("examples") == nil {
		t.Fatal("thema serve playground has no --examples")
	}
	fs, err := examplesManifest(writeExamplesDir(t))
	if err != nil {
		t.Fatal(diag.Diagnostics(err))
	}
	manifest, err := fs.ReadFile(tplayground.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}
	bundle := fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}
	get := func(h http.Handler) *http.Response {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/examples.json", nil))
		return rec.Result()
	}

	r := get(servePlaygroundHandler(bundle, manifest))
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != http.StatusOK || r.Header.Get("Content-Type") != "application/json" || !bytes.Equal(b, manifest) {
		t.Errorf("with examples, /examples.json is %d %s:\n%s", r.StatusCode, r.Header.Get("Content-Type"), b)
	}
	if r := get(servePlaygroundHandler(bundle, nil)); r.StatusCode != http.StatusNotFound {
		t.Errorf("without examples, /examples.json is %d, want 404", r.StatusCode)
	}
}

// A problem with --examples is reported before the playground listens.
func TestServePlaygroundExamplesFails(t *testing.T) {
	sc := &serveCommand{addr: "127.0.0.1:0", examples: filepath.Join(t.TempDir(), "nope")}
	err := sc.run(servePlaygroundCmd, nil)
	if codes := codesOf(err); len(codes) != 1 || codes[0] != diag.E0601 {
		t.Fatalf("codes %v, want [E0601]", codes)
	}
}
