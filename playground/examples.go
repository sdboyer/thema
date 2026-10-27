package playground

import (
	"sort"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/examples"
)

// ListExamples lists the examples the playground offers.
func (s *Session) ListExamples() ListExamplesResponse {
	resp := ListExamplesResponse{Result: ok(), Examples: []ExampleInfo{}}
	for _, e := range examples.All() {
		resp.Examples = append(resp.Examples, exampleInfo(e))
	}
	return resp
}

// GetExample returns one example's lineage, and a data document for each
// example its schemas declare, so that an example that binds has data to
// validate and translate.
func (s *Session) GetExample(req GetExampleRequest) GetExampleResponse {
	e, found := examples.ByID(req.ID)
	if !found {
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
	for _, sch := range b.lin.All() {
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
			resp.Data = append(resp.Data, DataInput{
				Filename: sch.Version().String() + "-" + name + ".json",
				Text:     string(out) + "\n",
				Format:   formatJSON,
			})
		}
	}
	return resp
}

func exampleInfo(e examples.Example) ExampleInfo {
	kind := "normal"
	if e.Authoring {
		kind = "authoring"
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
