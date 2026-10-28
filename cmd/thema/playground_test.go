package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/sdboyer/thema/diag"
	tplayground "github.com/sdboyer/thema/playground"
)

var setupPlaygroundOnce, setupServeOnce sync.Once

// writeExamplesDir writes a directory of lineages, one of them an example,
// and returns it.
func writeExamplesDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "lineages")
	files := map[string]string{
		"pets/cat/0.0.thema":   "Schema: {\n\tlives: int\n}\n",
		"pets/cat/example.cue": "category: \"Pets\"\nsummary: \"A cat.\"\n",
		"wip/draft/0.0.thema":  "Schema: {\n\tx: int\n}\n",
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

func runPlaygroundExamples(t *testing.T, args ...string) (string, error) {
	t.Helper()
	setupPlaygroundOnce.Do(func() { setupPlaygroundCommand(rootCmd) })
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	defer rootCmd.SetOut(nil)
	rootCmd.SetArgs(append([]string{"playground", "examples"}, args...))
	err := rootCmd.Execute()
	return out.String(), err
}

func codesOf(err error) []diag.Code {
	var codes []diag.Code
	for _, d := range diag.Diagnostics(err) {
		codes = append(codes, d.Code)
	}
	return codes
}

// thema playground examples <dir> prints the examples the directory offers,
// as a register-examples request.
func TestPlaygroundExamples(t *testing.T) {
	out, err := runPlaygroundExamples(t, writeExamplesDir(t))
	if err != nil {
		t.Fatal(diag.Diagnostics(err))
	}
	var req tplayground.RegisterExamplesRequest
	if err := json.Unmarshal([]byte(out), &req); err != nil {
		t.Fatalf("output is not a register-examples request: %v\n%s", err, out)
	}
	if len(req.Examples) != 1 {
		t.Fatalf("printed %d examples, want 1:\n%s", len(req.Examples), out)
	}
	if e := req.Examples[0].Example; e.ID != "pets/cat" || e.Category != "Pets" || e.Summary != "A cat." || !e.Binds {
		t.Errorf("printed %+v", e)
	}
}

// It takes exactly one directory.
func TestPlaygroundExamplesArgs(t *testing.T) {
	dir := writeExamplesDir(t)
	tests := map[string]struct {
		args []string
		want diag.Code
	}{
		"none":            {args: nil, want: diag.E0620},
		"two":             {args: []string{dir, dir}, want: diag.E0620},
		"missing":         {args: []string{filepath.Join(dir, "nope")}, want: diag.E0601},
		"without example": {args: []string{filepath.Join(dir, "wip")}, want: diag.E0622},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			out, err := runPlaygroundExamples(t, tt.args...)
			if err == nil {
				t.Fatalf("succeeded:\n%s", out)
			}
			if codes := codesOf(err); len(codes) != 1 || codes[0] != tt.want {
				t.Errorf("codes %v, want [%s]", codes, tt.want)
			}
		})
	}
}

// thema serve playground --examples <dir> serves the directory's examples at
// /examples.json; without it, /examples.json is the bundle's to answer.
func TestServePlaygroundExamples(t *testing.T) {
	setupServeOnce.Do(func() { setupServeCommand(rootCmd) })
	if servePlaygroundCmd.Flags().Lookup("examples") == nil {
		t.Fatal("thema serve playground has no --examples")
	}
	manifest, err := examplesManifest(writeExamplesDir(t))
	if err != nil {
		t.Fatal(diag.Diagnostics(err))
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
