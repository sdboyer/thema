package playground

import (
	"sort"

	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/examples"
)

// ListExamples lists the examples the playground offers: thema's own, then
// those a host registered.
func (s *Session) ListExamples() ListExamplesResponse {
	resp := ListExamplesResponse{Result: ok(), Examples: []ExampleInfo{}}
	for _, e := range examples.All() {
		resp.Examples = append(resp.Examples, exampleInfo(e))
	}
	for _, h := range s.host {
		resp.Examples = append(resp.Examples, h.Example)
	}
	return resp
}

// GetExample returns one example's lineage, and a data document for each
// example its schemas declare, so that an example that binds has data to
// validate and translate.
func (s *Session) GetExample(req GetExampleRequest) GetExampleResponse {
	e, found := examples.ByID(req.ID)
	if !found {
		for _, h := range s.host {
			if h.Example.ID == req.ID {
				return GetExampleResponse{Result: ok(), Example: h.Example, Lineage: h.Lineage, Data: h.Data}
			}
		}
		return GetExampleResponse{Result: failed(diag.New(diag.E0801).Headlinef("there is no example %q", req.ID).
			Help("list the examples with the examples operation").Err()), Data: []DataInput{}}
	}
	resp := GetExampleResponse{Result: ok(), Example: exampleInfo(e), Lineage: lineageOf(e), Data: []DataInput{}}
	if !e.Binds {
		return resp
	}
	b := s.lineage(resp.Lineage)
	if b.err != nil {
		// The example is declared to bind; a test holds it to that.
		return resp
	}
	resp.Data = schemaExamples(b.lin)
	return resp
}

// schemaExamples is a data document for each example lin's schemas declare,
// named for its version and its name.
func schemaExamples(lin thema.Lineage) []DataInput {
	data := []DataInput{}
	for _, sch := range lin.All() {
		exs := sch.Examples()
		names := make([]string, 0, len(exs))
		for name := range exs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			out, err := marshal(exs[name].Underlying())
			if err != nil {
				continue
			}
			data = append(data, DataInput{
				Filename: sch.Version().String() + "-" + name + ".json",
				Text:     string(out) + "\n",
				Format:   formatJSON,
			})
		}
	}
	return data
}

// The kinds of example: the form its lineage is written in.
const (
	kindAuthoring = "authoring"
	kindNormal    = "normal"
)

func exampleInfo(e examples.Example) ExampleInfo {
	kind := kindNormal
	if e.Authoring {
		kind = kindAuthoring
	}
	return ExampleInfo{ID: e.ID, Title: e.Title, Category: e.Category, Summary: e.Summary, Kind: kind, Binds: e.Binds}
}

func lineageOf(e examples.Example) LineageInput {
	if e.Authoring {
		in := LineageInput{Name: e.Name}
		for _, f := range e.Files {
			in.Files = append(in.Files, File{Path: f.Path, Text: f.Text})
		}
		return in
	}
	return LineageInput{Normal: &File{Path: e.Normal.Path, Text: e.Normal.Text}, Path: e.Path}
}

// RegisterExamples replaces the examples a host registered with req's. Each
// is checked before it is registered: its description must be complete, its
// ID and category its own, and its lineage must bind exactly when it says it
// does. An example that fails a check is refused with a diagnostic; the
// others are registered.
func (s *Session) RegisterExamples(req RegisterExamplesRequest) RegisterExamplesResponse {
	ids, categories := map[string]bool{}, map[string]bool{}
	for _, e := range examples.All() {
		ids[e.ID] = true
		categories[e.Category] = true
	}
	s.host = nil
	resp := RegisterExamplesResponse{Registered: []string{}}
	var errs []error
	for i, h := range req.Examples {
		if err := s.checkHostExample(i, &h, ids, categories); err != nil {
			errs = append(errs, err)
			continue
		}
		ids[h.Example.ID] = true
		s.host = append(s.host, h)
		resp.Registered = append(resp.Registered, h.Example.ID)
	}
	resp.Result = failed(errs...)
	return resp
}

// checkHostExample checks the host example at index i of a request, given
// the IDs already taken and thema's own categories, and fills in its kind
// and data when they are empty.
func (s *Session) checkHostExample(i int, h *HostExample, ids, categories map[string]bool) error {
	info := &h.Example
	if info.ID == "" {
		return diag.New(diag.E0804).Headlinef("the host example at index %d has no id", i).
			Help("give every example an id, unique among all the examples").Err()
	}
	switch {
	case info.Title == "":
		return diag.New(diag.E0804).Headlinef("host example %q has no title", info.ID).
			Help("the title is what the playground lists the example as").Err()
	case info.Category == "":
		return diag.New(diag.E0804).Headlinef("host example %q has no category", info.ID).
			Help("the category is the group the playground lists the example in").Err()
	case ids[info.ID]:
		return diag.New(diag.E0804).Headlinef("host example %q has the id of another example", info.ID).
			Help("give every example an id of its own; the examples operation lists the ids already taken").Err()
	case categories[info.Category]:
		return diag.New(diag.E0804).Headlinef("host example %q is in %q, a category of thema's own examples", info.ID, info.Category).
			Help("put a host's examples in categories of their own").Err()
	}

	var kind string
	switch in := h.Lineage; {
	case in.Normal != nil && len(in.Files) > 0:
		return diag.New(diag.E0802).Headlinef("host example %q gives its lineage in both forms", info.ID).
			Help("give either the authoring form's name and files or one normal-form file").Err()
	case in.Normal != nil:
		kind = kindNormal
	case len(in.Files) > 0:
		kind = kindAuthoring
	default:
		return diag.New(diag.E0802).Headlinef("host example %q gives no lineage", info.ID).
			Help("give the authoring form's name and files, or one normal-form file").Err()
	}
	if info.Kind == "" {
		info.Kind = kind
	} else if info.Kind != kind {
		return diag.New(diag.E0804).Headlinef("host example %q has kind %q, but its lineage is in the %s form", info.ID, info.Kind, kind).
			Helpf("set kind to %q, or leave it out", kind).Err()
	}

	b := s.lineage(h.Lineage)
	switch {
	case info.Binds && b.err != nil:
		e := diag.New(diag.E0805).Headlinef("host example %q is declared to bind, but does not", info.ID)
		for _, d := range diag.From(b.err, diag.Context{}) {
			e.Notef(token.NoPos, "binding it reports %s: %s", d.Code, d.Headline)
		}
		return e.Help("fix the lineage, or set binds to false if the example is meant to show an error").Err()
	case !info.Binds && b.err == nil:
		return diag.New(diag.E0805).Headlinef("host example %q is declared not to bind, but binds", info.ID).
			Help("set binds to true").Err()
	}
	if h.Data == nil {
		h.Data = []DataInput{}
	}
	return nil
}
