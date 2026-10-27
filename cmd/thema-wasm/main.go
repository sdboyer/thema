//go:build js && wasm

// Command thema-wasm is thema's playground compiled to WebAssembly, for a
// browser to run in a Web Worker. It exposes three functions on the global
// object, each synchronous, taking and returning strings:
//
//   - themaInit() returns the init response as JSON, creating the session on
//     first use.
//   - themaCall(op, request) answers one operation, given its request as JSON,
//     with its response as JSON. playground/types.ts declares both.
//   - themaVersion() returns the thema version the module was built from.
//
// When the functions are in place it calls globalThis.onThemaReady, if the
// page defined one, and then blocks forever: the functions are the program.
//
// A browser has no filesystem, and Go's wasm_exec.js stubs one that fails
// every call in a way CUE's loader does not accept. Load thema-fs.js, from
// this directory, before wasm_exec.js: it supplies a filesystem in which
// nothing exists, which is the truth.
//
// Build:
//
//	GOOS=js GOARCH=wasm go build -trimpath -ldflags='-s -w' -o thema.wasm ./cmd/thema-wasm
package main

import (
	"encoding/json"
	"sync"
	"syscall/js"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/util"
	"github.com/sdboyer/thema/playground"
)

func diagnosticsOf(err error) []diag.Diagnostic {
	return diag.From(err, diag.Context{})
}

var (
	once    sync.Once
	session *playground.Session
	initErr string
)

// get returns the session, creating it on first use. A failure is kept and
// answered on every call as the init response's diagnostics.
func get() *playground.Session {
	once.Do(func() {
		s, err := playground.New()
		if err != nil {
			b, _ := json.Marshal(map[string]any{"ok": false, "diagnostics": diagnosticsOf(err)})
			initErr = string(b)
			return
		}
		session = s
	})
	return session
}

func main() {
	js.Global().Set("themaInit", js.FuncOf(func(js.Value, []js.Value) any {
		s := get()
		if s == nil {
			return initErr
		}
		return string(s.Call("init", nil))
	}))
	js.Global().Set("themaCall", js.FuncOf(func(_ js.Value, args []js.Value) any {
		s := get()
		if s == nil {
			return initErr
		}
		if len(args) != 2 || args[0].Type() != js.TypeString || args[1].Type() != js.TypeString {
			return string(s.Call("", json.RawMessage(`null`)))
		}
		return string(s.Call(args[0].String(), json.RawMessage(args[1].String())))
	}))
	js.Global().Set("themaVersion", js.FuncOf(func(js.Value, []js.Value) any {
		return util.ThemaModuleVersion
	}))
	if ready := js.Global().Get("onThemaReady"); ready.Type() == js.TypeFunction {
		ready.Invoke()
	}
	select {}
}
