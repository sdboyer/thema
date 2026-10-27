package playground

import (
	"fmt"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/pkg/encoding/yaml"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/encoding/jsonschema"
	"github.com/sdboyer/thema/encoding/openapi"
	"github.com/sdboyer/thema/encoding/typescript"
)

// OpenAPI generates an OpenAPI document of one schema, as JSON or YAML (the
// default).
func (s *Session) OpenAPI(req CodegenRequest) CodegenResponse {
	return s.codegen(req, formatYAML, "OpenAPI", func(sch thema.Schema) (*ast.File, error) {
		return openapi.GenerateSchema(sch, nil)
	})
}

// JSONSchema generates a JSON Schema of one schema, as JSON (the default) or
// YAML.
func (s *Session) JSONSchema(req CodegenRequest) CodegenResponse {
	return s.codegen(req, formatJSON, "JSON Schema", jsonschema.GenerateSchema)
}

// TypeScript generates TypeScript types of one schema.
func (s *Session) TypeScript(req CodegenRequest) CodegenResponse {
	sch, resp, done := s.codegenSchema(req)
	if done {
		return resp
	}
	f, err := typescript.GenerateTypes(sch, nil)
	if err != nil {
		resp.Result = failed(codegenFailed("TypeScript", sch, err))
		return resp
	}
	resp.Output = f.String()
	resp.Result = ok()
	return resp
}

// codegen generates a document whose generator returns CUE syntax, and
// renders it in the requested format.
func (s *Session) codegen(req CodegenRequest, def, what string, gen func(thema.Schema) (*ast.File, error)) CodegenResponse {
	sch, resp, done := s.codegenSchema(req)
	if done {
		return resp
	}
	format := req.Format
	if format == "" {
		format = def
	}
	if format != formatJSON && format != formatYAML {
		resp.Result = failed(diag.New(diag.E0801).Headlinef("%q is not a format the playground generates %s in", format, what).
			Help("the formats are json and yaml").Err())
		return resp
	}
	f, err := gen(sch)
	if err != nil {
		resp.Result = failed(codegenFailed(what, sch, err))
		return resp
	}
	v := s.rt.Context().BuildFile(f)
	switch format {
	case formatJSON:
		b, err := marshal(v)
		if err != nil {
			resp.Result = failed(codegenFailed(what, sch, err))
			return resp
		}
		resp.Output = string(b) + "\n"
	case formatYAML:
		out, err := yaml.Marshal(v)
		if err != nil {
			resp.Result = failed(codegenFailed(what, sch, err))
			return resp
		}
		resp.Output = out
	}
	resp.Result = ok()
	return resp
}

// codegenSchema binds the lineage and finds the schema to generate from. It
// reports done with the response to return when it cannot.
func (s *Session) codegenSchema(req CodegenRequest) (thema.Schema, CodegenResponse, bool) {
	e := s.lineage(req.Lineage)
	if e.err != nil {
		return nil, CodegenResponse{Result: failed(e.err)}, true
	}
	sch, err := schema(e.lin, req.Version)
	if err != nil {
		return nil, CodegenResponse{Result: failed(err)}, true
	}
	return sch, CodegenResponse{Version: sch.Version().String()}, false
}

// codegenFailed describes a generator's failure on a schema the lineage
// accepted: a construct the target language has no equivalent for.
func codegenFailed(what string, sch thema.Schema, err error) error {
	return diag.New(diag.E0701).Headlinef("generating %s from schema %s failed", what, sch.Version()).
		Detail(err.Error()).
		Help(fmt.Sprintf("the schema is valid; %s cannot express some construct in it", what)).
		Err()
}
