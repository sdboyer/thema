package playground

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func fakeBundle(t *testing.T) fstest.MapFS {
	t.Helper()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write([]byte("\x00asm-raw")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>thema playground</title>")},
		"app.js":        {Data: []byte("console.log('app')")},
		"thema.wasm":    {Data: []byte("\x00asm-raw")},
		"thema.wasm.gz": {Data: gz.Bytes()},
		"MANIFEST.json": {Data: []byte(`{"themaVersion": "v0.1.0", "wasmSHA256": "abc123"}`)},
	}
}

func get(t *testing.T, h http.Handler, path string, header map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func body(t *testing.T, r *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHandlerWasm(t *testing.T) {
	h := Handler(fakeBundle(t))

	r := get(t, h, "/thema.wasm", map[string]string{"Accept-Encoding": "br, gzip;q=0.9"})
	if r.Header.Get("Content-Type") != "application/wasm" || r.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("headers = %v, want application/wasm with gzip", r.Header)
	}
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); string(b) != "\x00asm-raw" {
		t.Fatalf("decompressed body = %q", b)
	}
	if r.Header.Get("ETag") != `"abc123"` {
		t.Errorf("ETag = %q", r.Header.Get("ETag"))
	}

	r = get(t, h, "/thema.wasm", nil)
	if r.Header.Get("Content-Encoding") != "" || body(t, r) != "\x00asm-raw" {
		t.Fatalf("without gzip: headers %v", r.Header)
	}

	r = get(t, h, "/thema.wasm", map[string]string{"If-None-Match": `"abc123"`})
	if r.StatusCode != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", r.StatusCode)
	}
}

func TestHandlerFiles(t *testing.T) {
	h := Handler(fakeBundle(t))
	if b := body(t, get(t, h, "/", nil)); b != "<!doctype html><title>thema playground</title>" {
		t.Errorf("/ = %q", b)
	}
	if b := body(t, get(t, h, "/app.js", nil)); b != "console.log('app')" {
		t.Errorf("/app.js = %q", b)
	}
	// A route of the frontend's own is index.html.
	if b := body(t, get(t, h, "/share", nil)); b != "<!doctype html><title>thema playground</title>" {
		t.Errorf("/share = %q", b)
	}
	if r := get(t, h, "/missing.js", nil); r.StatusCode != http.StatusNotFound {
		t.Errorf("/missing.js status = %d, want 404", r.StatusCode)
	}
}
