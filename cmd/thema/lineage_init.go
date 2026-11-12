package main

import (
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	upcue "cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/build"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/load"
	"cuelang.org/go/encoding/json"
	"cuelang.org/go/encoding/jsonschema"
	"cuelang.org/go/encoding/openapi"
	"cuelang.org/go/encoding/yaml"
	"github.com/sdboyer/jenny"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
	"github.com/spf13/cobra"
)

type initCommand struct {
	srcpath string
	input   []byte
	dir     string
	name    string
	stdout  bool

	// gen holds the new lineage's tip, generated, and tip is that file.
	gen *jenny.FS
	tip authoring.File
	err error
}

func (ic *initCommand) setup(cmd *cobra.Command) {
	cmd.AddCommand(initLineageCmd)
	initLineageCmd.PersistentFlags().StringVarP(&ic.dir, "lineage", "l", ".", "path to the new lineage's authoring directory, whose base name is the lineage's name")
	initLineageCmd.PersistentFlags().StringVar(&ic.name, "name", "", "the new lineage's name, and its directory's, under the current one unless -l gives the directory")
	initLineageCmd.PersistentFlags().BoolVar(&ic.stdout, "stdout", false, "print the new lineage's tip.thema, and write nothing")

	initLineageCmd.AddCommand(initLineageEmptyCmd)
	initLineageEmptyCmd.RunE = ic.run

	initLineageCmd.AddCommand(initLineageOpenAPICmd)
	initLineageOpenAPICmd.Flags().StringVar(&ic.srcpath, "src-subpath", "", "JSON pointer to one schema within the OpenAPI document (e.g. #/components/schemas/Foo). Default: whole document")
	initLineageOpenAPICmd.RunE = ic.run
	initLineageOpenAPICmd.PreRunE = ic.processInput

	initLineageCmd.AddCommand(initLineageJSONSchemaCmd)
	initLineageJSONSchemaCmd.Flags().StringVar(&ic.srcpath, "src-subpath", "", "Schema path within the JSON Schema document (e.g. #/...) Default: whole document")
	initLineageJSONSchemaCmd.RunE = ic.run
	initLineageJSONSchemaCmd.PreRunE = ic.processInput
}

var initLineageCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a new lineage",
	Long: `Create a new lineage, in the authoring form.

Each subcommand takes the new lineage's schema from a different kind of input
source, and writes the lineage's tip.thema in the lineage's directory, given
by -l, the current directory by default. A lineage's name is its directory's
base name: -l ship writes ship/tip.thema, for the lineage ship. --name ship,
without -l, does the same; with -l, --name must be -l's base name. The
directory is created if it does not exist, and a directory that already holds
version files is refused: init does not overwrite a lineage. The new lineage
is bound before it is written.

The history is empty, so the tip cuts as 0.0; cut it with thema lineage cut
once it is ready.

With --stdout, the tip.thema is printed instead, and nothing is written; -l
and --name do not apply.
`,
}

var initLineageEmptyCmd = &cobra.Command{
	Use:   "empty [-l <dir>] [--name <name>] [--stdout]",
	Args:  cobra.MaximumNArgs(0),
	Short: "Initialize with an empty schema",
	Long: `Initialize the lineage with an empty schema.

The new lineage's tip.thema is written in its directory, given by -l, or
printed with --stdout.
`,
}

var initLineageOpenAPICmd = &cobra.Command{
	Use:   "openapi [<path>] [--src-subpath <pointer>] [-l <dir>] [--name <name>] [--stdout]",
	Args:  cobra.MaximumNArgs(1),
	Short: "Initialize with an OpenAPI v3 schema",
	Long: `Initialize the lineage with one schema, derived from an OpenAPI v3 document.

The OpenAPI 3.0 or 3.1 document is read from <path>, as JSON or YAML by its
extension, or from stdin, as JSON or else YAML.

The schema is the whole document's components, each a definition, or, with
--src-subpath, the one schema it points at, such as
#/components/schemas/Foo, with the components that schema refers to as
definitions beside it.

The new lineage's tip.thema is written in its directory, given by -l, or
printed with --stdout.
`,
}

var initLineageJSONSchemaCmd = &cobra.Command{
	Use:   "jsonschema [<path>] [--src-subpath <pointer>] [-l <dir>] [--name <name>] [--stdout]",
	Args:  cobra.MaximumNArgs(1),
	Short: "Initialize with a JSON Schema",
	Long: `Initialize the lineage with one schema, derived from a JSON Schema document.

The JSON Schema document is read from <path>, or from stdin. The schema is
the whole document, or, with --src-subpath, the one schema it points at, such
as #/$defs/ship, with the schemas that schema refers to as definitions beside
it.

The new lineage's tip.thema is written in its directory, given by -l, or
printed with --stdout.
`,
}

func (ic *initCommand) run(cmd *cobra.Command, args []string) error {
	dir, err := ic.lineageDir(cmd)
	if err != nil {
		return err
	}
	switch cmd.CalledAs() {
	case "empty":
		ic.runEmpty(cmd, args)
	case "jsonschema":
		ic.runJSONSchema(cmd, args)
	case "openapi":
		ic.runOpenAPI(cmd, args)
	default:
		panic(fmt.Sprint("unrecognized command ", cmd.CalledAs())) //nolint:forbidigo // run is the RunE of empty, jsonschema and openapi alone, and none has an alias
	}
	if ic.err != nil {
		return ic.err
	}
	if ic.stdout {
		_, err = cmd.OutOrStdout().Write(ic.tip.Text)
		return stdoutErr(err)
	}
	return ic.write(cmd, dir)
}

// lineageDir returns the absolute path of the directory init writes the new
// lineage to, or "" with --stdout. A lineage's name is its directory's base
// name, so --name and -l cannot disagree: --name alone names a directory under
// the current one, and with -l it must be -l's base name.
func (ic *initCommand) lineageDir(cmd *cobra.Command) (string, error) {
	dirSet := cmd.Flags().Changed("lineage")
	if ic.stdout {
		if dirSet || ic.name != "" {
			return "", diag.New(diag.E0620).Headline("--stdout writes no directory, so it takes neither -l nor --name").
				Help("drop --stdout to write the lineage's directory, or drop -l and --name to print its tip.thema").Err()
		}
		return "", nil
	}
	dir := ic.dir
	if ic.name != "" && !dirSet {
		if filepath.Base(ic.name) != ic.name {
			return "", diag.New(diag.E0620).Headlinef("--name %q is a path, not a lineage's name", ic.name).
				Help("a lineage's name is its directory's base name; give the directory with -l").Err()
		}
		dir = ic.name
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", diag.New(diag.E0601).Headlinef("%s cannot be resolved to a path", dir).Detail(diag.OSReason(err)).Err()
	}
	if base := filepath.Base(abs); ic.name != "" && base != ic.name {
		return "", diag.New(diag.E0620).Headlinef("--name %s disagrees with -l %s, whose base name is %s", ic.name, ic.dir, base).
			Helpf("a lineage's name is its directory's base name: drop --name, or give -l a directory named %s", ic.name).Err()
	}
	return abs, nil
}

// write writes the new lineage's tip, ic.tip, as dir/tip.thema, once the
// lineage it starts binds. A directory that already holds version files is
// refused.
func (ic *initCommand) write(cmd *cobra.Command, dir string) error {
	if isAuthoringDir(dir) {
		return diag.New(diag.E0623).Headlinef("%s already holds a lineage's version files", displayPath(dir)).
			Help("init creates a new lineage and never overwrites one: give -l a directory that holds no version files").Err()
	}
	// The bind's diagnostics point into the tip, which is not on disk yet:
	// they are rendered from its text, under the name its positions carry.
	at, _ := positionDir(dir)
	inlineSources[path.Join(at, ic.tip.Path)] = ic.tip.Text
	c, err := authoring.CompileDevelopment(ctx, filepath.Base(dir), []authoring.File{ic.tip}, authoring.At(at))
	if err == nil {
		_, err = authoring.BindDevelopment(rt, c)
	}
	if err != nil {
		// The name the bind rejects is the directory's, which the author
		// gave init, not anything in the tip.
		for _, e := range cerrors.Errors(err) {
			if d := diag.Diagnostics(e); len(d) == 1 && d[0].Code == diag.E0137 {
				diag.AddHelp(e, fmt.Sprintf("the name is the base name of the directory init was given, %s; nothing was written", displayPath(dir)))
			}
		}
		return err
	}
	if err = writeFS(cmd, ic.gen, dir); err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), displayPath(filepath.Join(dir, ic.tip.Path)))
	return stdoutErr(err)
}

// process both openapi and json schema, abstract over stdin
func (ic *initCommand) processInput(cmd *cobra.Command, args []string) error {
	byt, err := pathOrStdin(args)
	if err != nil {
		return err
	}

	ic.input = byt
	return nil
}

func (ic *initCommand) runEmpty(cmd *cobra.Command, args []string) {
	ic.newTip(ctx.CompileString("{}"))
}

// newTip makes ic.gen and ic.tip, the tip of a new lineage whose one version
// is sch.
func (ic *initCommand) newTip(sch upcue.Value) {
	ic.gen, ic.err = authoring.InitialTip.GenerateFS(sch)
	if ic.err != nil {
		return
	}
	f := ic.gen.AsFiles()[0]
	text, err := f.Render()
	ic.tip, ic.err = authoring.File{Path: f.Path(), Text: text}, err
}

func (ic *initCommand) runJSONSchema(cmd *cobra.Command, args []string) {
	v := ctx.CompileBytes(ic.input)
	if v.Err() != nil {
		ic.err = v.Err()
		return
	}

	// The pointer names one schema, as it does for init openapi, not a
	// location whose every member is a schema.
	ic.err = ic.extractTip(v, &jsonschema.Config{
		Root:       ic.srcpath,
		SingleRoot: ic.srcpath != "",
	})
}

// extractTip makes ic.tip from the schema jsonschema.Extract makes of doc
// with cfg, as the extraction writes it: ... for an object that allows other
// properties, and close() for one that does not. Evaluating the result first
// would drop both, and every object would close with the schema.
func (ic *initCommand) extractTip(doc upcue.Value, cfg *jsonschema.Config) error {
	f, err := jsonschema.Extract(doc, cfg)
	if err != nil {
		return err
	}
	// Remove the file's attributes, such as the @jsonschema(schema=...) the
	// extraction writes for the document's $schema.
	astutil.Apply(f, func(c astutil.Cursor) bool {
		if _, ok := c.Node().(*ast.Attribute); ok {
			astutil.CopyComments(c.Node(), c.Parent().Node())
			c.Delete()
		}

		// Only descend into the file/top-level, not within fields
		_, is := c.Node().(*ast.File)
		return is
	}, nil)
	ic.newTip(ctx.BuildFile(f))
	return ic.err
}

// inputToFile parses input, the document processInput read from the file
// args names or from stdin, as JSON or YAML. A file's format is its
// extension's. stdin has no extension, and the loader would take it as CUE,
// so it is tried as JSON, then as YAML, as data validate tries its data.
func inputToFile(input []byte, args []string) (*ast.File, error) {
	if len(args) == 0 {
		if f, err := jsonFile(input); err == nil {
			return f, nil
		}
		if f, err := yaml.Extract("input", input); err == nil {
			return f, nil
		}
		return nil, diag.New(diag.E0611).Headline("the document on stdin is neither JSON nor YAML").
			Help("pipe an OpenAPI document as JSON or YAML, or pass its path").Err()
	}

	binsts := load.Instances(args, &load.Config{})
	if len(binsts[0].OrphanedFiles) == 0 {
		return nil, diag.New(diag.E0614).Headlinef("%s is not a format thema converts", args[0]).Help("pass an OpenAPI or JSON Schema document as JSON or YAML").Err()
	}
	switch bf := binsts[0].OrphanedFiles[0]; bf.Encoding {
	case build.YAML:
		return yaml.Extract("input", input)
	case build.JSON:
		return jsonFile(input)
	default:
		return nil, diag.New(diag.E0614).Headlinef("%s is not a format thema converts", bf.Encoding).Help("pass an OpenAPI or JSON Schema document as JSON or YAML").Err()
	}
}

// jsonFile parses input as one JSON value, the file's only declaration.
func jsonFile(input []byte) (*ast.File, error) {
	expr, err := json.Extract("input", input)
	if err != nil {
		return nil, err
	}
	return &ast.File{Decls: []ast.Decl{expr}}, nil
}

func (ic *initCommand) runOpenAPI(cmd *cobra.Command, args []string) {
	f, err := inputToFile(ic.input, args)
	if err != nil {
		ic.err = err
		return
	}

	v := ctx.BuildFile(f)
	if err := v.Err(); err != nil {
		ic.err = err
		return
	}
	version, err := openAPIVersion(v)
	if err != nil {
		ic.err = err
		return
	}
	if ic.srcpath != "" {
		ic.err = ic.openAPISchema(v, version)
		return
	}
	fo, err := openapi.Extract(v, &openapi.Config{})
	if err != nil {
		ic.err = err
		return
	}
	// Remove info field
	var done bool
	astutil.Apply(fo, func(c astutil.Cursor) bool {
		if x, ok := c.Node().(*ast.Field); ok {
			n, _, _ := ast.LabelName(x.Label)
			if n == "info" {
				c.Delete()
				done = true
			}
		}

		if done {
			return false
		}
		// Only descend into the file/top-level, not within fields
		_, is := c.Node().(*ast.File)
		return is
	}, nil)

	ic.newTip(ctx.BuildFile(fo))
}

// openAPISchema makes the tip from the one schema at --src-subpath, a JSON
// pointer into the OpenAPI document doc, whose schemas are in the dialect
// version.
//
// openapi.Extract makes every component schema a definition, #Foo, and
// nothing done to a cue.Value looked up at #Foo opens it again: it stays
// recursively closed, and exporting it, as NewTip does, wraps it as
// {_#def, _#def: ...}. So the schema is extracted on its own, as the root of
// the result, the way init jsonschema --src-subpath extracts one, and is a
// regular value. Its openness is the document's: the extraction writes ...
// for an object that allows other properties, and close() for one that does
// not. The component schemas it refers to are definitions beside it, named
// as openapi.Extract names them.
func (ic *initCommand) openAPISchema(doc upcue.Value, version jsonschema.Version) error {
	frag, ok := strings.CutPrefix(ic.srcpath, "#/")
	if !ok {
		return diag.New(diag.E0620).Headlinef("--src-subpath %q is not a JSON pointer into the OpenAPI document", ic.srcpath).
			Help("point at a schema in the document, such as #/components/schemas/Foo").Err()
	}
	at := doc
	for tok := range json.Pointer("/" + frag).Tokens() {
		sel := upcue.Str(tok)
		if at.Kind() == upcue.ListKind {
			i, err := strconv.Atoi(tok)
			if err != nil {
				at = upcue.Value{}
				break
			}
			sel = upcue.Index(i)
		}
		at = at.LookupPath(upcue.MakePath(sel))
	}
	if !at.Exists() {
		return diag.New(diag.E0620).Headlinef("there is no value at --src-subpath %s in the OpenAPI document", ic.srcpath).Err()
	}
	return ic.extractTip(doc, &jsonschema.Config{
		Root:           ic.srcpath,
		SingleRoot:     true,
		MapRef:         openAPIRef,
		DefaultVersion: version,
		// As openapi.Extract: OpenAPI 3.0 allows no keyword JSON Schema
		// does not know.
		StrictKeywords: version == jsonschema.VersionOpenAPI,
	})
}

// openAPIVersion returns the JSON Schema dialect of the schemas in the
// OpenAPI document doc, as openapi.Extract reads it: 3.0's own, and JSON
// Schema 2020-12 for 3.1.
func openAPIVersion(doc upcue.Value) (jsonschema.Version, error) {
	v, err := doc.LookupPath(upcue.MakePath(upcue.Str("openapi"))).String()
	switch {
	case err != nil:
		return jsonschema.VersionUnknown, diag.New(diag.E0624).Headline("the document declares no OpenAPI version, so it is not an OpenAPI document thema converts").
			Help(`an OpenAPI 3 document declares its version as a string, as in openapi: "3.0.0"; convert an OpenAPI 2 (Swagger) document to OpenAPI 3 first`).Err()
	case strings.HasPrefix(v, "3.0."):
		return jsonschema.VersionOpenAPI, nil
	case strings.HasPrefix(v, "3.1."):
		return jsonschema.VersionDraft2020_12, nil
	}
	return jsonschema.VersionUnknown, diag.New(diag.E0624).Headlinef("the document is OpenAPI %s, a version thema does not convert", v).
		Help("thema converts OpenAPI 3.0 and 3.1 documents").Err()
}

// openAPIRef places the component schema a reference names,
// #/components/schemas/Foo, at the definition #Foo, as openapi.Extract
// places it; a name that is not an identifier is a field of #SchemaMap.
func openAPIRef(loc jsonschema.SchemaLoc) (string, upcue.Path, error) {
	sels := loc.Path.Selectors()
	isStr := func(i int, s string) bool {
		return sels[i].LabelType() == upcue.StringLabel && sels[i].Unquoted() == s
	}
	if !loc.IsLocal || len(sels) != 3 || !isStr(0, "components") || !isStr(1, "schemas") || sels[2].LabelType() != upcue.StringLabel {
		return "", upcue.Path{}, fmt.Errorf("%v: a reference must be to a schema under #/components/schemas/", loc)
	}
	name := sels[2].Unquoted()
	if name == "SchemaMap" || ast.StringLabelNeedsQuoting(name) {
		return "", upcue.MakePath(upcue.Def("#SchemaMap"), upcue.Str(name)), nil
	}
	return "", upcue.MakePath(upcue.Def("#" + name)), nil
}
