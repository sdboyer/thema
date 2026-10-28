// Package playground is thema's playground as an API: bind a lineage written
// in either form, validate and translate data with it, generate code from it,
// and convert it between forms, with every outcome reported as diagnostics.
//
// A [Session] answers operations by name, taking and returning JSON
// ([Session.Call]), which is how the wasm build (cmd/thema-wasm) exposes it to
// a browser. The same operations are methods for Go callers and tests.
//
// A session never touches the network or the filesystem: a lineage may import
// github.com/sdboyer/thema, served from the copy embedded in this program,
// and nothing else.
//
// The examples a session offers are thema's own, compiled in, and any a host
// registers ([Session.RegisterExamples]), so that a deployment of the
// playground can offer lineages of its own without rebuilding thema.wasm.
// The frontend in playground/ui fetches examples.json from beside the page at
// start-up and, when there is one, registers it: the file is a
// [RegisterExamplesRequest]. A 404 means the host has none.
package playground

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/util"
)

// Session holds one CUE runtime and the lineages it has bound. It is safe
// for concurrent use; operations run one at a time.
type Session struct {
	mu    sync.Mutex
	rt    *thema.Runtime
	cache *lineageCache
	ops   map[string]op
	// host is the examples a host registered, in its order.
	host []HostExample
}

// New creates a session. Loading thema's own CUE cannot fail in a correct
// build; if it does, New reports it as a diagnostic rather than panicking.
func New() (s *Session, err error) {
	defer func() {
		if r := recover(); r != nil {
			s, err = nil, diag.Internal(r)
		}
	}()
	return &Session{
		rt:    thema.NewRuntime(cuecontext.New()),
		cache: newLineageCache(cacheSize),
		ops:   operations(),
	}, nil
}

// cacheSize bounds the lineages a session keeps bound. Each holds CUE values
// in the session's context, which never shrinks, so a long session that binds
// many distinct lineages grows regardless; the bound keeps an editing session,
// which rebinds the same few lineages, from growing with every keystroke.
const cacheSize = 16

// op is one operation: it decodes its request and answers it.
type op func(s *Session, req json.RawMessage) any

// operations is the table a session answers [Session.Call] from, by name.
// Each session builds its own: the init operation lists the operations, so
// a package variable holding the table would be an initialization cycle.
func operations() map[string]op {
	return map[string]op{
		"init":         noRequest(func(s *Session) any { return s.Init() }),
		"bind":         typed(func(s *Session, r BindRequest) any { return s.Bind(r) }),
		"validate":     typed(func(s *Session, r ValidateRequest) any { return s.Validate(r) }),
		"validate-any": typed(func(s *Session, r ValidateAnyRequest) any { return s.ValidateAny(r) }),
		"translate":    typed(func(s *Session, r TranslateRequest) any { return s.Translate(r) }),
		"hydrate":      typed(func(s *Session, r HydrateRequest) any { return s.Hydrate(r) }),
		"dehydrate":    typed(func(s *Session, r HydrateRequest) any { return s.Dehydrate(r) }),
		"openapi":      typed(func(s *Session, r CodegenRequest) any { return s.OpenAPI(r) }),
		"jsonschema":   typed(func(s *Session, r CodegenRequest) any { return s.JSONSchema(r) }),
		"typescript":   typed(func(s *Session, r CodegenRequest) any { return s.TypeScript(r) }),
		"compile":      typed(func(s *Session, r CompileRequest) any { return s.Compile(r) }),
		"decompile":    typed(func(s *Session, r DecompileRequest) any { return s.Decompile(r) }),
		"format":       typed(func(s *Session, r FormatRequest) any { return s.Format(r) }),
		"examples":     noRequest(func(s *Session) any { return s.ListExamples() }),
		"example":      typed(func(s *Session, r GetExampleRequest) any { return s.GetExample(r) }),
		"register-examples": typed(func(s *Session, r RegisterExamplesRequest) any {
			return s.RegisterExamples(r)
		}),
	}
}

func typed[Req any](f func(*Session, Req) any) op {
	return func(s *Session, raw json.RawMessage) any {
		var req Req
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &req); err != nil {
				return failed(diag.New(diag.E0801).Headline("the request is not the JSON this operation takes").Detail(err.Error()).Err())
			}
		}
		return f(s, req)
	}
}

func noRequest(f func(*Session) any) op {
	return func(s *Session, _ json.RawMessage) any { return f(s) }
}

// Ops returns the names of the operations [Session.Call] answers, sorted.
func (s *Session) Ops() []string {
	names := make([]string, 0, len(s.ops))
	for name := range s.ops {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Call answers the operation named op, given its request as JSON, with its
// response as JSON. It never panics and never fails: an unknown operation, a
// malformed request and a panic inside thema are all diagnostics in a
// response.
func (s *Session) Call(name string, req json.RawMessage) json.RawMessage {
	var resp any
	if f, ok := s.ops[name]; ok {
		resp = s.guard(func() any { return f(s, req) })
	} else {
		resp = failed(diag.New(diag.E0801).Headlinef("%q is not an operation of the playground", name).
			Helpf("the operations are %s", joinNames(s.Ops())).Err())
	}
	out, err := json.Marshal(resp)
	if err != nil {
		out, _ = json.Marshal(failed(diag.InternalErr(fmt.Errorf("encoding the response to %s: %w", name, err))))
	}
	return out
}

// guard runs one operation with the session's lock held, and turns a panic
// into an internal-error diagnostic.
func (s *Session) guard(f func() any) (resp any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			resp = failed(diag.Internal(r))
		}
	}()
	return f()
}

// Init describes the playground.
func (s *Session) Init() InitResponse {
	return InitResponse{
		Result:       ok(),
		ThemaVersion: util.ThemaModuleVersion,
		CUEVersion:   cueVersion(),
		Ops:          s.Ops(),
	}
}

// cueVersion is the version of CUE this program was built with.
func cueVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, dep := range info.Deps {
		if dep.Path == "cuelang.org/go" {
			if dep.Replace != nil {
				return dep.Replace.Version
			}
			return dep.Version
		}
	}
	return ""
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		switch {
		case i == 0:
		case i == len(names)-1:
			out += " and "
		default:
			out += ", "
		}
		out += n
	}
	return out
}

// ok is a successful result with nothing to say.
func ok() Result {
	return Result{OK: true, Diagnostics: []diag.Diagnostic{}}
}

// failed is the result of errs: every diagnostic they carry, and OK false
// when any is an error. Errors that are not diagnostics are converted.
func failed(errs ...error) Result {
	ds := []diag.Diagnostic{}
	for _, err := range errs {
		if err != nil {
			ds = append(ds, diag.From(err, diag.Context{})...)
		}
	}
	return resultOf(ds)
}

// resultOf is the result of ds: OK unless some diagnostic is an error.
// Positions in a normal-form file loaded as a package name it by the path
// the request gave it, not by the module directory it was loaded from.
func resultOf(ds []diag.Diagnostic) Result {
	r := Result{OK: true, Diagnostics: ds}
	for i := range ds {
		d := &ds[i]
		if d.Severity == diag.SeverityError {
			r.OK = false
		}
		unroot(d.Primary)
		for j := range d.Notes {
			unroot(d.Notes[j].Pos)
		}
	}
	return r
}

// moduleRoot is the directory a normal-form file that imports thema is
// loaded from.
var moduleRoot = filepath.Join(util.Prefix, playgroundModule) + string(filepath.Separator)

func unroot(p *diag.Pos) {
	if p != nil {
		p.File = strings.TrimPrefix(p.File, moduleRoot)
	}
}

// lineageCache keeps the most recently bound lineages, by their input.
type lineageCache struct {
	size  int
	order []string
	byKey map[string]cached
}

// cached is one input's outcome: the bound lineage, or the diagnostics that
// stopped it, and its normal form.
type cached struct {
	lin thema.Lineage
	// raw is the lineage's value as loaded, before binding unifies it with
	// thema's own CUE: what the decompiler reads.
	raw    cue.Value
	normal string
	err    error
}

func newLineageCache(size int) *lineageCache {
	return &lineageCache{size: size, byKey: make(map[string]cached, size)}
}

func cacheKey(in LineageInput) string {
	b, _ := json.Marshal(in)
	sum := sha256.Sum256(b)
	return string(sum[:])
}

func (c *lineageCache) get(in LineageInput) (cached, bool) {
	e, ok := c.byKey[cacheKey(in)]
	return e, ok
}

func (c *lineageCache) put(in LineageInput, e cached) {
	key := cacheKey(in)
	if _, ok := c.byKey[key]; !ok {
		c.order = append(c.order, key)
	}
	c.byKey[key] = e
	for len(c.order) > c.size {
		delete(c.byKey, c.order[0])
		c.order = c.order[1:]
	}
}
