// Copyright 2020 CUE Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package openapi converts between thema schemas and OpenAPI documents:
// [GenerateSchema] generates an OpenAPI document from a schema, and [Extract]
// converts the schemas of an OpenAPI document to CUE.
package openapi

// Extract is forked from cuelang.org/go/encoding/openapi.Extract at
// cuelang.org/go v0.18.0-rc.3, to decode through internal/jsonschema, which
// keeps a property's x- keywords. Its Config is ExtractConfig, holding the
// fields of upstream's Config that Extract reads, and may be nil.

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
	"github.com/sdboyer/thema/internal/jsonschema"
)

// ExtractConfig controls the conversion of an OpenAPI document to CUE by
// [Extract].
type ExtractConfig struct {
	// PkgName defines to package name for a generated CUE package.
	PkgName string

	// StrictFeatures reports an error for features that are known
	// to be unsupported.
	StrictFeatures bool

	// StrictKeywords reports an error when unknown keywords
	// are encountered. For OpenAPI 3.0, this is implicitly always
	// true, as that specification explicitly prohibits unknown keywords
	// other than "x-" prefixed keywords.
	StrictKeywords bool
}

// Extract converts OpenAPI definitions to an equivalent CUE representation.
//
// It currently only converts entries in #/components/schema and extracts some
// meta data.
//
// It is cuelang.org/go/encoding/openapi.Extract, except that it keeps the x-
// keywords of each property's schema, which that skips, as one attribute on
// the property's field; see [Extensions] for its shape. The x- keywords of
// any other schema, such as a component schema itself or an array's items,
// are not kept.
//
// The result can be converted to a [cue.Value] via [cue.Context.BuildFile].
func Extract(data cue.InstanceOrValue, c *ExtractConfig) (*ast.File, error) {
	// TODO: find a good OpenAPI validator. Both go-openapi and kin-openapi
	// seem outdated. The k8s one might be good, but avoid pulling in massive
	// amounts of dependencies.

	if c == nil {
		c = &ExtractConfig{}
	}
	f := &ast.File{}
	add := func(d ast.Decl) {
		if d != nil {
			f.Decls = append(f.Decls, d)
		}
	}

	v := data.Value()
	versionValue := v.LookupPath(cue.MakePath(cue.Str("openapi")))
	if versionValue.Err() != nil {
		return nil, fmt.Errorf("openapi field is required but not found")
	}
	version, err := versionValue.String()
	if err != nil {
		return nil, fmt.Errorf("invalid openapi field (must be string): %v", err)
	}
	// A simple prefix match is probably OK for now, following
	// the same logic used by internal/encoding.isOpenAPI.
	// The specification says that the patch version should be disregarded:
	// https://swagger.io/specification/v3/
	var schemaVersion jsonschema.Version
	switch {
	case strings.HasPrefix(version, "3.0."):
		schemaVersion = jsonschema.VersionOpenAPI
	case strings.HasPrefix(version, "3.1."):
		schemaVersion = jsonschema.VersionDraft2020_12
	default:
		return nil, fmt.Errorf("unknown OpenAPI version %q", version)
	}

	doc, _ := v.LookupPath(cue.MakePath(cue.Str("info"), cue.Str("title"))).String() // Required
	if s, _ := v.LookupPath(cue.MakePath(cue.Str("info"), cue.Str("description"))).String(); s != "" {
		doc += "\n\n" + s
	}
	cg := jsonschema.NewComment(true, doc)

	if c.PkgName != "" {
		p := &ast.Package{Name: ast.NewIdent(c.PkgName)}
		ast.AddComment(p, cg)
		add(p)
	} else if cg != nil {
		add(cg)
	}

	js, err := jsonschema.Extract(data, &jsonschema.Config{
		Map:            openAPIMapping, //nolint:staticcheck // as upstream: MapRef would need a copy of jsonschema's fragment handling
		Root:           oapiSchemas,
		DefaultVersion: schemaVersion,
		StrictFeatures: c.StrictFeatures,
		// OpenAPI 3.0 is stricter than JSON Schema about allowed keywords.
		StrictKeywords: schemaVersion == jsonschema.VersionOpenAPI || c.StrictKeywords,
	})
	if err != nil {
		return nil, err
	}
	preamble := js.Preamble()
	body := js.Decls[len(preamble):]
	for _, d := range preamble {
		switch x := d.(type) {
		case *ast.Package:
			return nil, errors.Newf(x.Pos(), "unexpected package %q", x.Name.Name)

		default:
			add(x)
		}
	}

	// TODO: allow attributes before imports? Would be easier.

	// TODO: do we want to store the OpenAPI version?
	// if version, _ := v.Lookup("openapi").String(); version != "" {
	//  add(&ast.Attribute{Text: fmt.Sprintf("@openapi(version=%s)", version)})
	// }

	if info := v.LookupPath(cue.MakePath(cue.Str("info"))); info.Exists() {
		decls := []interface{}{}
		if st, ok := info.Syntax().(*ast.StructLit); ok {
			// Remove title.
			for _, d := range st.Elts {
				if f, ok := d.(*ast.Field); ok {
					switch name, _, _ := ast.LabelName(f.Label); name {
					case "title", "version":
						// title: *"title" | string
						decls = append(decls, &ast.Field{
							Label: f.Label,
							Value: ast.NewBinExpr(token.OR,
								&ast.UnaryExpr{Op: token.MUL, X: f.Value},
								ast.NewIdent("string")),
						})
						continue
					}
				}
				decls = append(decls, d)
			}
			add(&ast.Field{
				Label: ast.NewIdent("info"),
				Value: ast.NewStruct(decls...),
			})
		}
	}

	if len(body) > 0 {
		ast.SetRelPos(body[0], token.NewSection)
		f.Decls = append(f.Decls, body...)
	}

	return f, nil
}

const oapiSchemas = "#/components/schemas/"

// rootDefs is the fallback for schemas that are not valid identifiers.
// TODO: find something more principled.
const rootDefs = "#SchemaMap"

func openAPIMapping(pos token.Pos, a []string) ([]ast.Label, error) {
	if len(a) != 3 || a[0] != "components" || a[1] != "schemas" {
		return nil, errors.Newf(pos,
			`openapi: reference must be of the form %q; found "#/%s"`,
			oapiSchemas, strings.Join(a, "/"))
	}
	name := a[2]
	if name != rootDefs[1:] && !ast.StringLabelNeedsQuoting(name) {
		return []ast.Label{ast.NewIdent("#" + name)}, nil
	}
	return []ast.Label{ast.NewIdent(rootDefs), ast.NewString(name)}, nil
}
