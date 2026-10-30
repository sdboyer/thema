package playground

import (
	"cuelang.org/go/cue/parser"

	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
)

// Compile compiles authoring files to the normal form, with the tip, as the
// playground binds them, without binding it.
func (s *Session) Compile(req CompileRequest) CompileResponse {
	if req.Name == "" {
		return CompileResponse{Result: failed(diag.New(diag.E0802).Headline("the authoring form's lineage has no name").
			Help("a lineage in the authoring form is named by its directory; send its name").Err())}
	}
	c, err := authoring.CompileDevelopment(s.rt.Context(), req.Name, authoringFiles(req.Files))
	if err != nil {
		return CompileResponse{Result: failed(err)}
	}
	normal, err := formatNode(c.File)
	if err != nil {
		return CompileResponse{Result: failed(diag.InternalErr(err))}
	}
	return CompileResponse{Result: failed(c.Pending), Normal: normal}
}

// Decompile recovers the authoring files of a normal-form lineage. The
// lineage must bind: the authoring form has no way to write one that does
// not. It is decompiled from its value as loaded, without thema's own CUE.
func (s *Session) Decompile(req DecompileRequest) DecompileResponse {
	e := s.lineage(LineageInput{Normal: &req.Normal, Path: req.Path})
	if e.err != nil {
		return DecompileResponse{Result: failed(e.err)}
	}
	name, files, err := authoring.Decompile(e.raw)
	if err != nil {
		return DecompileResponse{Result: failed(err)}
	}
	resp := DecompileResponse{Result: ok(), Name: name}
	for _, f := range files {
		resp.Files = append(resp.Files, File{Path: f.Path, Text: string(f.Text)})
	}
	return resp
}

// Format lays out one file of the authoring form in its layout.
func (s *Session) Format(req FormatRequest) FormatResponse {
	path := req.File.Path
	if path == "" {
		path = authoring.TipFile
	}
	// Parsed under its path first, so that a syntax error points into the
	// file; Format parses without one.
	if _, err := parser.ParseFile(path, req.File.Text, parser.ParseComments); err != nil {
		return FormatResponse{Result: failed(diag.Convert(err, diag.Context{Op: diag.OpCompile, Generic: diag.E0102}))}
	}
	out, err := authoring.Format([]byte(req.File.Text))
	if err != nil {
		return FormatResponse{Result: failed(diag.InternalErr(err))}
	}
	return FormatResponse{Result: ok(), Text: string(out)}
}
