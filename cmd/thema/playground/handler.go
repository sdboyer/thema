package playground

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// Handler serves a bundle. thema.wasm goes out compressed when the client
// accepts gzip and the bundle holds thema.wasm.gz, always as application/wasm
// (which WebAssembly.instantiateStreaming requires), with the digest from
// MANIFEST.json as its ETag. A path with no file extension that the bundle
// does not hold is the frontend's to route, and gets index.html.
func Handler(bundle fs.FS) http.Handler {
	var m Manifest
	if b, err := fs.ReadFile(bundle, "MANIFEST.json"); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	files := http.FileServerFS(bundle)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "thema.wasm" {
			serveWasm(w, r, bundle, m)
			return
		}
		if name != "" && path.Ext(name) == "" {
			if _, err := fs.Stat(bundle, name); err != nil {
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	})
}

// WithExamples serves manifest at /examples.json, where the frontend looks
// for a host's examples to register, and everything else from h.
func WithExamples(h http.Handler, manifest []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path.Clean(r.URL.Path) != "/examples.json" {
			h.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		http.ServeContent(w, r, "examples.json", time.Time{}, bytes.NewReader(manifest))
	})
}

func serveWasm(w http.ResponseWriter, r *http.Request, bundle fs.FS, m Manifest) {
	h := w.Header()
	h.Set("Content-Type", "application/wasm")
	h.Set("Vary", "Accept-Encoding")
	if m.WasmSHA256 != "" {
		etag := `"` + m.WasmSHA256 + `"`
		h.Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	name := "thema.wasm"
	if acceptsGzip(r) {
		if _, err := fs.Stat(bundle, "thema.wasm.gz"); err == nil {
			name = "thema.wasm.gz"
			h.Set("Content-Encoding", "gzip")
		}
	}
	b, err := fs.ReadFile(bundle, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(b)
}

func acceptsGzip(r *http.Request) bool {
	for part := range strings.SplitSeq(r.Header.Get("Accept-Encoding"), ",") {
		enc, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		if enc == "gzip" {
			return true
		}
	}
	return false
}
