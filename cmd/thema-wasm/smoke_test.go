//go:build !js

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// maxWasmSize is the size budget for thema.wasm, built as a release builds
// it. The browser downloads it before the playground can do anything; a
// change that crosses the budget should be a decision, not an accident.
const maxWasmSize = 45 << 20

// The wasm build answers through its JS bridge under node as it does
// natively: testdata/smoke.mjs loads it as a page's worker does and calls
// through every layer, including CUE's module loader.
func TestWasmSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("builds thema.wasm")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	wasm := filepath.Join(t.TempDir(), "thema.wasm")
	build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", wasm, ".") //nolint:gosec // builds this package into the test's temporary directory
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("building thema.wasm: %v\n%s", err, out)
	}
	info, err := os.Stat(wasm)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxWasmSize {
		t.Errorf("thema.wasm is %d bytes, over the budget of %d", info.Size(), maxWasmSize)
	}
	t.Logf("thema.wasm is %d bytes", info.Size())

	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	wasmExec := filepath.Join(strings.TrimSpace(string(goroot)), "lib", "wasm", "wasm_exec.js")
	themaFS, err := filepath.Abs("thema-fs.js")
	if err != nil {
		t.Fatal(err)
	}
	out, err = exec.Command(node, filepath.Join("testdata", "smoke.mjs"), themaFS, wasmExec, wasm).CombinedOutput() //nolint:gosec // the test's own script
	if err != nil {
		t.Fatalf("smoke test failed: %v\n%s", err, out)
	}
	if !strings.HasPrefix(string(out), "ok thema ") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}
