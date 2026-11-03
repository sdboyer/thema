package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	upcue "cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/load"
	"cuelang.org/go/encoding/json"
	"cuelang.org/go/encoding/jsonschema"
	"cuelang.org/go/encoding/openapi"
	"cuelang.org/go/encoding/yaml"
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
	initLineageOpenAPICmd.Flags().StringVar(&ic.srcpath, "src-subpath", "", "Schema path within the OpenAPI document. Default: whole document")
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
	Use:   "openapi [<path>] [--src-subpath <path>] [-l <dir>] [--name <name>] [--stdout]",
	Args:  cobra.MaximumNArgs(1),
	Short: "Initialize with an OpenAPI v3 schema",
	Long: `Initialize the lineage with one schema, derived from an OpenAPI v3 document.

An OpenAPI document to be converted for the initial lineage schema must be given as an argument.

The new lineage's tip.thema is written in its directory, given by -l, or
printed with --stdout.
`,
}

var initLineageJSONSchemaCmd = &cobra.Command{
	Use:   "jsonschema [<path>] [--src-subpath <pointer>] [-l <dir>] [--name <name>] [--stdout]",
	Args:  cobra.MaximumNArgs(1),
	Short: "Initialize with a JSON Schema",
	Long: `Initialize the lineage with one schema, derived from a JSON Schema document.

A JSON Schema document to be converted for the initial lineage schema must be given as an argument.

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
		panic(fmt.Sprint("unrecognized command ", cmd.CalledAs()))
	}
	if ic.err != nil {
		return ic.err
	}
	if ic.stdout {
		_, err = cmd.OutOrStdout().Write(ic.tip.Text)
		return err
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

// A new lineage's directory and tip.thema have the permissions any other
// source directory and file have, before the umask.
const (
	newDirPerm  = 0o755
	newFilePerm = 0o644
)

// write writes the new lineage's tip, ic.tip, as dir/tip.thema, once the
// lineage it starts binds. A directory that already holds version files is
// refused.
func (ic *initCommand) write(cmd *cobra.Command, dir string) error {
	if isAuthoringDir(dir) {
		return diag.New(diag.E0623).Headlinef("%s already holds a lineage's version files", displayPath(dir)).
			Help("init creates a new lineage and never overwrites one: give -l a directory that holds no version files").Err()
	}
	sourceDirs = append(sourceDirs, dir)
	c, err := authoring.CompileDevelopment(ctx, filepath.Base(dir), []authoring.File{ic.tip})
	if err != nil {
		return err
	}
	if _, err = authoring.BindDevelopment(rt, c); err != nil {
		return err
	}
	if err = os.MkdirAll(dir, newDirPerm); err != nil {
		return diag.New(diag.E0616).Headlinef("%s cannot be written", displayPath(dir)).Detail(diag.OSReason(err)).Err()
	}
	p := filepath.Join(dir, ic.tip.Path)
	if err = os.WriteFile(p, ic.tip.Text, newFilePerm); err != nil {
		return diag.New(diag.E0616).Headlinef("%s cannot be written", displayPath(p)).Detail(diag.OSReason(err)).Err()
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), displayPath(p))
	return err
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

// newTip makes ic.tip, the tip of a new lineage whose one version is sch.
func (ic *initCommand) newTip(sch upcue.Value) {
	ic.tip, ic.err = authoring.NewTip(sch)
}

func (ic *initCommand) runJSONSchema(cmd *cobra.Command, args []string) {
	v := ctx.CompileBytes(ic.input)
	if v.Err() != nil {
		ic.err = v.Err()
		return
	}

	jcfg := &jsonschema.Config{
		Root: ic.srcpath,
	}

	f, err := jsonschema.Extract(v, jcfg)
	if err != nil {
		ic.err = err
		return
	}

	sch := ctx.BuildFile(f)
	// Remove attributes field
	astutil.Apply(f, func(c astutil.Cursor) bool {
		if _, ok := c.Node().(*ast.Attribute); ok {
			astutil.CopyComments(c.Node(), c.Parent().Node())
			c.Delete()
		}

		// Only descend into the file/top-level, not within fields
		_, is := c.Node().(*ast.File)
		return is
	}, nil)

	ic.newTip(sch.Eval())
}

// expects something else to have already gotten the input from either a file
// or stdin (as we do with pathOrStdin) and passed it as input param
func inputToFile(input []byte, args []string) (*ast.File, error) {
	if len(args) == 0 {
		args = append(args, "-")
	}

	cfg := &load.Config{
		Stdin: bytes.NewBuffer(input),
	}
	binsts := load.Instances(args, cfg)
	bf := binsts[0].OrphanedFiles[0]
	if bf == nil {
		return nil, diag.New(diag.E0613).Headline("the input file could not be loaded").Err()
	}

	var f *ast.File
	var err error
	switch bf.Encoding {
	case build.YAML:
		f, err = yaml.Extract("input", input)
	case build.JSON:
		expr, err := json.Extract("input", input)
		if err == nil {
			f = &ast.File{
				Decls: []ast.Decl{expr},
			}
		}
	default:
		err = diag.New(diag.E0614).Headlinef("%s is not a format thema converts", bf.Encoding).Help("pass an OpenAPI or JSON Schema document as JSON or YAML").Err()
	}

	if err != nil {
		return nil, err
	}

	return f, nil
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

	sch := ctx.BuildFile(fo)
	if ic.srcpath != "" {
		p := upcue.ParsePath(ic.srcpath)
		if p.Err() != nil {
			ic.err = diag.New(diag.E0620).Headlinef("--src-subpath %q is not a CUE path", ic.srcpath).Detail(p.Err().Error()).Err()
			return
		}
		// Eval will do dereferencing for us as needed, but may have other unintended
		// side effects.
		sch = sch.LookupPath(p).Eval()
		if !sch.Exists() {
			ic.err = diag.New(diag.E0620).Headlinef("there is no value at --src-subpath %s in the converted schema", p.String()).Err()
			return
		}
	}

	ic.newTip(sch)
}
