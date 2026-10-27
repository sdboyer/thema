package playground

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"cuelang.org/go/cue"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/vmux"
)

// Validate validates a data document against one schema.
func (s *Session) Validate(req ValidateRequest) ValidateResponse {
	lin, data, err := s.linData(req.Lineage, req.Data)
	if err != nil {
		return ValidateResponse{Result: failed(err)}
	}
	sch, err := schema(lin, req.Version)
	if err != nil {
		return ValidateResponse{Result: failed(err)}
	}
	if _, err := sch.Validate(data); err != nil {
		return ValidateResponse{Result: failed(err), Version: sch.Version().String()}
	}
	return ValidateResponse{Result: ok(), Version: sch.Version().String()}
}

// ValidateAny searches the lineage for a schema the data is an instance of,
// newest first, and reports what every schema said.
func (s *Session) ValidateAny(req ValidateAnyRequest) ValidateAnyResponse {
	lin, data, err := s.linData(req.Lineage, req.Data)
	if err != nil {
		return ValidateAnyResponse{Result: failed(err), PerVersion: []VersionDiagnostics{}}
	}
	resp := ValidateAnyResponse{PerVersion: []VersionDiagnostics{}}
	all := lin.All()
	for i := len(all) - 1; i >= 0; i-- {
		sch := all[i]
		vd := VersionDiagnostics{Version: sch.Version().String(), Diagnostics: []diag.Diagnostic{}}
		if _, err := sch.Validate(data); err != nil {
			vd.Diagnostics = failed(err).Diagnostics
		} else if resp.Version == "" {
			resp.Version = vd.Version
		}
		resp.PerVersion = append(resp.PerVersion, vd)
	}
	if resp.Version != "" {
		resp.Result = ok()
		return resp
	}
	resp.Result = failed(noSchemaAccepts())
	return resp
}

// noSchemaAccepts is the failure of validating data against every schema in
// the lineage.
func noSchemaAccepts() error {
	return diag.New(diag.E0508).Headline("the data is an instance of no schema in the lineage").
		Help("each version's diagnostics say what that schema rejects").Err()
}

// instance validates data against the schema at version, or searches the
// lineage for its schema when version is empty.
func instance(lin thema.Lineage, data cue.Value, version string) (*thema.Instance, error) {
	if version == "" {
		inst := lin.ValidateAny(data)
		if inst == nil {
			return nil, noSchemaAccepts()
		}
		return inst, nil
	}
	sch, err := schema(lin, version)
	if err != nil {
		return nil, err
	}
	return sch.Validate(data)
}

// Translate translates a data document from one version to another.
func (s *Session) Translate(req TranslateRequest) TranslateResponse {
	lin, data, err := s.linData(req.Lineage, req.Data)
	if err != nil {
		return TranslateResponse{Result: failed(err)}
	}
	if req.To == "" {
		return TranslateResponse{Result: failed(diag.New(diag.E0803).Headline("no version to translate to was given").
			Help("name the version, such as 1.0").Err())}
	}
	// Translate panics on a version the lineage does not have (#57); ask
	// first.
	to, err := schema(lin, req.To)
	if err != nil {
		return TranslateResponse{Result: failed(err)}
	}
	inst, err := instance(lin, data, req.From)
	if err != nil {
		return TranslateResponse{Result: failed(err)}
	}
	resp := TranslateResponse{From: inst.Schema().Version().String(), To: to.Version().String()}
	out, lacunas, err := inst.Translate(to.Version())
	if err != nil {
		resp.Result = failed(err)
		return resp
	}
	resp.Output, err = marshal(out.Underlying())
	if err != nil {
		resp.Result = failed(err)
		return resp
	}
	resp.Lacunas = lacunas
	resp.Result = ok()
	return resp
}

// Hydrate fills a data document with its schema's defaults.
func (s *Session) Hydrate(req HydrateRequest) HydrateResponse {
	return s.hydrate(req, (*thema.Instance).Hydrate)
}

// Dehydrate removes from a data document the values its schema's defaults
// imply.
func (s *Session) Dehydrate(req HydrateRequest) HydrateResponse {
	return s.hydrate(req, (*thema.Instance).Dehydrate)
}

func (s *Session) hydrate(req HydrateRequest, f func(*thema.Instance) *thema.Instance) HydrateResponse {
	lin, data, err := s.linData(req.Lineage, req.Data)
	if err != nil {
		return HydrateResponse{Result: failed(err)}
	}
	inst, err := instance(lin, data, req.Version)
	if err != nil {
		return HydrateResponse{Result: failed(err)}
	}
	resp := HydrateResponse{Version: inst.Schema().Version().String()}
	before, err := marshal(inst.Underlying())
	if err != nil {
		resp.Result = failed(err)
		return resp
	}
	resp.Output, err = marshal(f(inst).Underlying())
	if err != nil {
		resp.Result = failed(err)
		return resp
	}
	resp.Changed = !sameJSON(before, resp.Output)
	resp.Result = ok()
	return resp
}

// sameJSON reports whether two JSON documents hold the same value, whatever
// the order of their fields.
func sameJSON(a, b []byte) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return bytes.Equal(a, b)
	}
	return reflect.DeepEqual(va, vb)
}

// marshal renders a concrete value as indented JSON.
func marshal(v cue.Value) (json.RawMessage, error) {
	b, err := v.MarshalJSON()
	if err != nil {
		return nil, diag.InternalErr(err)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		return nil, diag.InternalErr(err)
	}
	return out.Bytes(), nil
}

// linData binds the lineage and decodes the data document.
func (s *Session) linData(in LineageInput, d DataInput) (thema.Lineage, cue.Value, error) {
	e := s.lineage(in)
	if e.err != nil {
		return nil, cue.Value{}, e.err
	}
	data, err := s.decode(d)
	if err != nil {
		return nil, cue.Value{}, err
	}
	return e.lin, data, nil
}

// decode parses a data document as JSON or YAML, positioned under its
// filename.
func (s *Session) decode(d DataInput) (cue.Value, error) {
	name := d.Filename
	if name == "" {
		name = "data.json"
	}
	if strings.HasSuffix(name, ".cue") {
		return cue.Value{}, diag.New(diag.E0801).Headlinef("the data document is named %s, like a CUE file", name).
			Help("name a data document .json or .yaml; the lineage is the only CUE").Err()
	}
	ctx := s.rt.Context()
	parse := func(format string) (cue.Value, error) {
		var codec vmux.Codec
		if format == "yaml" {
			codec = vmux.NewYAMLCodec(name)
		} else {
			codec = vmux.NewJSONCodec(name)
		}
		v, err := codec.Decode(ctx, []byte(d.Text))
		if err != nil {
			b := diag.New(diag.E0507).Headlinef("%s is not valid %s", name, strings.ToUpper(format))
			var ce cerrors.Error
			if errors.As(err, &ce) {
				b.At(ce.Position())
			}
			return cue.Value{}, b.Detail(err.Error()).Err()
		}
		return v, nil
	}
	switch d.Format {
	case "json", "yaml":
		return parse(d.Format)
	case "":
		if v, err := parse("json"); err == nil {
			return v, nil
		}
		if v, err := parse("yaml"); err == nil {
			return v, nil
		}
		return cue.Value{}, diag.New(diag.E0611).Headlinef("%s is neither JSON nor YAML", name).
			Help("name the document's format to see what its parser reports").Err()
	}
	return cue.Value{}, diag.New(diag.E0801).Headlinef("%q is not a data format the playground reads", d.Format).
		Help("the formats are json and yaml").Err()
}
