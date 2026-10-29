package playground

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/diagtest"
)

const (
	petID     = "host/pet"
	petNormal = "name: \"pet\"\nschemas: {\n\t\"0\": {\n\t\t\"0\": {\n\t\t\tschema: {\n\t\t\t\tname: string\n\t\t\t}\n\t\t}\n\t}\n}\n"
	petSchema = "Schema: {\n\tname: string\n}\n"
	noSchemas = "name: \"empty\"\nschemas: {}\n"
)

func hostExample(id, category string, binds bool, in LineageInput) HostExample {
	return HostExample{
		Example: ExampleInfo{ID: id, Title: id, Category: category, Summary: "A host's example.", Binds: binds},
		Lineage: in,
	}
}

func normalInput(text string) LineageInput {
	return LineageInput{Normal: &File{Path: "lineage.cue", Text: text}}
}

// A host's examples are registered after their checks, refused with a
// diagnostic each when they fail one, and then listed and served after
// thema's own.
func TestRegisterExamples(t *testing.T) {
	s := newSession(t)
	builtin := len(s.ListExamples().Examples)

	pet := hostExample(petID, "Host", true, normalInput(petNormal))
	pet.Data = []DataInput{{Filename: "0.0-rex.json", Text: "{\"name\": \"rex\"}\n", Format: "json"}}
	authoring := hostExample("host/pet-authoring", "Host", true, LineageInput{Name: "pet", Files: []File{{Path: "0.0.thema", Text: petSchema}}})
	wrongKind := hostExample("host/wrong-kind", "Host", true, normalInput(petNormal))
	wrongKind.Example.Kind = kindAuthoring
	untitled := hostExample("host/untitled", "Host", true, normalInput(petNormal))
	untitled.Example.Title = ""
	both := hostExample("host/both", "Host", true, normalInput(petNormal))
	both.Lineage.Name, both.Lineage.Files = "pet", authoring.Lineage.Files

	req := RegisterExamplesRequest{Examples: []HostExample{
		pet,
		authoring,
		hostExample("host/empty", "Host errors", false, normalInput(noSchemas)),
		hostExample("host/empty-binds", "Host errors", true, normalInput(noSchemas)),
		hostExample("host/pet-fails", "Host", false, normalInput(petNormal)),
		hostExample(petID, "Host", true, normalInput(petNormal)),
		hostExample("authoring/ship", "Host", true, normalInput(petNormal)),
		hostExample("host/exemplar", "Exemplars", true, normalInput(petNormal)),
		hostExample("", "Host", true, normalInput(petNormal)),
		untitled,
		wrongKind,
		both,
		hostExample("host/none", "Host", true, LineageInput{}),
	}}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var resp RegisterExamplesResponse
	if err := json.Unmarshal(s.Call("register-examples", b), &resp); err != nil {
		t.Fatal(err)
	}
	diagtest.Lint(t, resp.Diagnostics, diagtest.Normal)

	wantRegistered := []string{petID, "host/pet-authoring", "host/empty"}
	if !slices.Equal(resp.Registered, wantRegistered) {
		t.Errorf("registered %v, want %v", resp.Registered, wantRegistered)
	}
	if resp.OK {
		t.Error("a request with refused examples is OK")
	}
	wantCodes := []diag.Code{
		diag.E0805, // host/empty-binds
		diag.E0805, // host/pet-fails
		diag.E0804, // host/pet again
		diag.E0804, // authoring/ship
		diag.E0804, // Exemplars
		diag.E0804, // no id
		diag.E0804, // no title
		diag.E0804, // wrong kind
		diag.E0802, // both forms
		diag.E0802, // no lineage
	}
	var codes []diag.Code
	for _, d := range resp.Diagnostics {
		codes = append(codes, d.Code)
	}
	if !slices.Equal(codes, wantCodes) {
		t.Fatalf("codes %v, want %v: %+v", codes, wantCodes, resp.Diagnostics)
	}
	if notes := resp.Diagnostics[0].Notes; len(notes) == 0 {
		t.Error("a host example that does not bind has no note of what binding it reports")
	}

	list := s.ListExamples().Examples
	if len(list) != builtin+len(wantRegistered) {
		t.Fatalf("listed %d examples, want %d", len(list), builtin+len(wantRegistered))
	}
	for i, id := range wantRegistered {
		if got := list[builtin+i].ID; got != id {
			t.Errorf("example %d is %s, want %s", builtin+i, got, id)
		}
	}
	if kind := list[builtin+1].Kind; kind != kindAuthoring {
		t.Errorf("an empty kind is taken from the lineage's form: got %q, want authoring", kind)
	}

	ex := s.GetExample(GetExampleRequest{ID: petID})
	if !ex.OK || ex.Example.Kind != kindNormal || ex.Lineage.Normal == nil || ex.Lineage.Normal.Text != petNormal {
		t.Fatalf("GetExample(host/pet) = %+v", ex)
	}
	if len(ex.Data) != 1 || ex.Data[0].Filename != "0.0-rex.json" {
		t.Errorf("host/pet's data is %+v, want what was registered", ex.Data)
	}
	if v := s.Validate(ValidateRequest{Lineage: ex.Lineage, Data: ex.Data[0]}); !v.OK {
		t.Errorf("host/pet's data does not validate: %+v", v.Diagnostics)
	}
	if ex := s.GetExample(GetExampleRequest{ID: "host/empty"}); !ex.OK || ex.Data == nil {
		t.Errorf("GetExample(host/empty) = %+v, want its lineage and no data", ex)
	}

	// A registration replaces the one before it.
	again := s.RegisterExamples(RegisterExamplesRequest{})
	if !again.OK || len(again.Registered) != 0 {
		t.Fatalf("registering nothing: %+v", again)
	}
	if got := len(s.ListExamples().Examples); got != builtin {
		t.Errorf("after registering nothing, %d examples are listed, want %d", got, builtin)
	}
	if ex := s.GetExample(GetExampleRequest{ID: petID}); ex.OK {
		t.Error("an example from a replaced registration is still served")
	}
}
