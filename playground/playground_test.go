package playground

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/diagtest"
	"github.com/sdboyer/thema/internal/examples"
)

func newSession(t *testing.T) *Session {
	t.Helper()
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Every example binds exactly when it says it does, its diagnostics follow
// the authoring principles, and its data documents are instances of the
// schemas they came from.
func TestExamples(t *testing.T) {
	s := newSession(t)
	list := s.ListExamples()
	if !list.OK || len(list.Examples) == 0 {
		t.Fatalf("no examples: %+v", list.Result)
	}
	for _, info := range list.Examples {
		t.Run(info.ID, func(t *testing.T) {
			ex := s.GetExample(GetExampleRequest{ID: info.ID})
			if !ex.OK {
				t.Fatalf("GetExample: %+v", ex.Diagnostics)
			}
			b := s.Bind(BindRequest{Lineage: ex.Lineage})
			form := diagtest.Normal
			if info.Kind == "authoring" {
				form = diagtest.Authoring
			}
			diagtest.Lint(t, b.Diagnostics, form)
			if b.OK != info.Binds {
				t.Fatalf("example says binds=%v, but Bind reports ok=%v: %+v", info.Binds, b.OK, b.Diagnostics)
			}
			if !b.OK && len(b.Diagnostics) == 0 {
				t.Fatal("a lineage that does not bind has no diagnostics")
			}
			for _, d := range ex.Data {
				version, _, _ := strings.Cut(d.Filename, "-")
				v := s.Validate(ValidateRequest{Lineage: ex.Lineage, Data: d, Version: version})
				if !v.OK {
					t.Errorf("%s is not an instance of %s: %+v", d.Filename, version, v.Diagnostics)
				}
			}
		})
	}
}

// Each archive in testdata is one call: #op names the operation; the
// request is request.json, with its lineage taken from the archive's
// lineage files and its data from data.json or data.yaml when the archive
// has them. #raw sends request.json as it is. out/response.json is the
// response.
func TestCalls(t *testing.T) {
	s := newSession(t)
	for _, a := range diagtest.Archives(t, "testdata") {
		t.Run(a.Name(), func(t *testing.T) {
			dirs, _ := a.Directives()
			op := dirs["op"]
			if op == "" {
				t.Fatal("no #op")
			}
			req := []byte("{}")
			if r, ok := a.File("request.json"); ok {
				req = []byte(r)
			}
			if dirs["raw"] != "true" {
				req = buildRequest(t, a, req)
			}
			resp := s.Call(op, req)
			var out bytes.Buffer
			if err := json.Indent(&out, resp, "", "  "); err != nil {
				t.Fatalf("response is not JSON: %v\n%s", err, resp)
			}
			var r struct {
				Result
			}
			if err := json.Unmarshal(resp, &r); err != nil {
				t.Fatal(err)
			}
			form := diagtest.Normal
			for _, f := range a.Inputs() {
				if strings.HasSuffix(f.Name, ".thema") {
					form = diagtest.Authoring
				}
			}
			diagtest.Lint(t, r.Diagnostics, form)
			a.Compare(t, map[string]string{"out/response.json": out.String() + "\n"})
		})
	}
}

// buildRequest fills a request's lineage and data from the archive's files.
func buildRequest(t *testing.T, a *diagtest.Archive, base []byte) []byte {
	t.Helper()
	req := map[string]any{}
	if err := json.Unmarshal(base, &req); err != nil {
		t.Fatalf("request.json: %v", err)
	}
	var in LineageInput
	for _, f := range a.Inputs() {
		dir, rest, nested := strings.Cut(f.Name, "/")
		switch {
		case nested && strings.HasSuffix(rest, ".thema"):
			in.Name = dir
			in.Files = append(in.Files, File{Path: rest, Text: string(f.Data)})
		case f.Name == "lineage.cue":
			in.Normal = &File{Path: f.Name, Text: string(f.Data)}
		case f.Name == "data.json" || f.Name == "data.yaml":
			req["data"] = DataInput{Filename: f.Name, Text: string(f.Data)}
		}
	}
	if p, ok := req["path"].(string); ok {
		in.Path = p
		delete(req, "path")
	}
	if in.Normal != nil || len(in.Files) > 0 {
		req["lineage"] = in
	}
	// compile, decompile and format take their files directly.
	switch dirs, _ := a.Directives(); dirs["op"] {
	case "compile":
		req = map[string]any{"name": in.Name, "files": in.Files}
	case "decompile":
		req["normal"] = in.Normal
		delete(req, "lineage")
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A panic inside an operation is a diagnostic, not a crash.
func TestGuard(t *testing.T) {
	s := newSession(t)
	resp := s.guard(func() any { panic("boom") })
	r, ok := resp.(Result)
	if !ok || r.OK || len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != diag.E0901 {
		t.Fatalf("want one E0901, got %+v", resp)
	}
}

// Every example the examples package declares is listed.
func TestExamplesListed(t *testing.T) {
	s := newSession(t)
	if got, want := len(s.ListExamples().Examples), len(examples.All()); got != want {
		t.Fatalf("listed %d examples, want %d", got, want)
	}
}
