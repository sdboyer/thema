package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	gotoken "go/token"
	iofs "io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
	"cuelang.org/go/pkg/encoding/yaml"
	"github.com/dave/dst"
	"github.com/sdboyer/jenny"
	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"
	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/encoding/gocode"
	"github.com/sdboyer/thema/encoding/jsonschema"
	"github.com/sdboyer/thema/encoding/openapi"
)

type genCommand struct {
	group bool
	// include suffix in generated lineage factory
	suffix bool
	// lineage factory should be private
	private bool
	// don't generate the embed.FS
	noembed bool
	// don't generate the themaFSFunc impl
	nofsfunc bool

	// write to stdout instead of generator-specific file
	stdout bool

	quiet bool

	// input file format (yaml, json, etc.)
	format string

	lin thema.Lineage
	sch thema.Schema
	// go type to bind to
	bindtype string
	// go package name to target
	pkgname string
	// path for embedding
	epath string
	// directory to write generated files under
	outdir string
	// verify the files under outdir instead of writing them
	check bool

	lla *lineageLoadArgs
}

func (gc *genCommand) setup(cmd *cobra.Command) {
	cmd.AddCommand(genLineageCmd)
	gc.lla = new(lineageLoadArgs)
	addLinPathVars(genLineageCmd, gc.lla)
	genLineageCmd.PersistentPreRunE = mergeCobraefuncs(gc.lla.validateLineageInput, gc.lla.validateVersionInputOptional)

	gop := genOapiLineageCmd
	genLineageCmd.AddCommand(gop)
	gop.Flags().StringVarP(&gc.lla.verstr, "version", "v", "", "schema syntactic version to generate. Defaults to latest")
	gop.Flags().StringVarP(&gc.format, "format", "f", "yaml", "output format. \"json\" or \"yaml\".")
	gop.PreRunE = gc.checkFormat
	gop.RunE = gc.run

	gj := genJschLineageCmd
	genLineageCmd.AddCommand(gj)
	gj.Flags().StringVarP(&gc.lla.verstr, "version", "v", "", "schema syntactic version to generate. Defaults to latest")
	gj.Flags().StringVarP(&gc.format, "format", "f", "json", "output format. \"json\" or \"yaml\".")
	gj.PreRunE = gc.checkFormat
	gj.RunE = gc.run

	ggb := genGoBindingsLineageCmd
	genLineageCmd.AddCommand(ggb)
	ggb.Use = "gobindings -l <path> [-p <cue-path>] [--bind-type <name>] [--bindversion <synver>] [--suffix] [--private] [--no-embed]"
	ggb.Flags().StringVar(&gc.bindtype, "bind-type", "", "Generate a ConvergentLineage that binds a lineage's schema to this Go type")
	ggb.Flags().StringVarP(&gc.lla.verstr, "version", "v", "", "Only meaningful with --bind-type. Bind to this schema version. Defaults to latest")
	ggb.Flags().StringVar(&gc.pkgname, "pkgname", "", "Name for generated Go package. Defaults to lowercase lineage name")
	ggb.Flags().BoolVar(&gc.suffix, "suffix", false, "Generate the lineage factory as 'Lineage<TitleCaseName>()' instead of 'Lineage()'")
	ggb.Flags().BoolVar(&gc.private, "private", false, "Generate the lineage factory as an unexported (lowercase) func.")
	ggb.Flags().BoolVar(&gc.noembed, "no-embed", false, "Do not generate an embed.FS")
	ggb.Flags().BoolVar(&gc.nofsfunc, "no-fs-func", false, "Do not generate the func that returns fs.FS for loading")
	ggb.Flags().BoolVar(&gc.stdout, "stdout", false, "Write to stdout instead of an adjacent file")
	ggb.Flags().BoolVarP(&gc.quiet, "quiet", "q", false, "Do not print generated filename")
	ggb.RunE = gc.run

	ggt := genGoTypesLineageCmd
	genLineageCmd.AddCommand(ggt)
	ggt.Flags().StringVarP(&gc.lla.verstr, "version", "v", "", "schema syntactic version to generate, as types.go. Defaults to every version, each in a directory of its own")
	ggt.Flags().StringVar(&gc.pkgname, "pkgname", "", "Only with -v: name for the generated Go package. Defaults to lowercase lineage name")
	ggt.Flags().StringVarP(&gc.outdir, "out", "o", "", "directory to write the generated files under. Defaults to printing them to stdout as a txtar archive")
	ggt.Flags().BoolVar(&gc.check, "check", false, "Only with -o: write nothing, and fail if the files under the directory are not what would be generated")
	ggt.RunE = gc.runGoTypes

	// TODO
	// genLineageCmd.AddCommand(genTSTypesLineageCmd)
	// genTSTypesLineageCmd.Flags().StringVarP((*string)(&verstr), "version", "v", "", "schema syntactic version to generate. Defaults to latest")
}

func (gc *genCommand) run(cmd *cobra.Command, args []string) error {
	// TODO encapsulate these properly
	gc.lin = gc.lla.dl.lin
	gc.sch = gc.lla.dl.sch
	gc.epath = gc.lla.inputLinFilePath

	fi, err := os.Stat(gc.lla.inputLinFilePath)
	if err != nil {
		return diag.New(diag.E0601).Headlinef("%s cannot be read", displayPath(gc.lla.inputLinFilePath)).Detail(diag.OSReason(err)).Err()
	}

	gc.epath = "*.cue"
	if !fi.IsDir() {
		gc.epath = filepath.Base(gc.lla.inputLinFilePath)
	}

	switch cmd.CalledAs() {
	case "jsonschema":
		err = gc.runJSONSchema(cmd, args)
	case "openapi":
		err = gc.runOpenAPI(cmd, args)
	case "gobindings":
		err = gc.runGoBindings(cmd, args)
	case "gotypes":
		err = gc.runGoTypes(cmd, args)
	case "tstypes":
		err = gc.runTSTypes(cmd, args)
	default:
		panic(fmt.Sprint("unrecognized command ", cmd.CalledAs()))
	}
	return err
}

// The formats openapi and jsonschema write, as -f names them.
const (
	formatJSON = "json"
	formatYAML = "yaml"
	formatYML  = "yml"
)

// checkFormat refuses an -f that names no format openapi and jsonschema
// write, before anything is generated.
func (gc *genCommand) checkFormat(_ *cobra.Command, _ []string) error {
	switch gc.format {
	case formatJSON, formatYAML, formatYML:
		return nil
	}
	return diag.New(diag.E0620).Headlinef("-f names the output format, and %q is not a format thema writes", gc.format).
		Help("-f takes json, yaml or yml").Err()
}

var genLineageCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate code from a lineage",
	Long: `Generate code from a lineage and its schemas.

Each subcommand supports generating code for a different language target.

Note that the controls offered by each subcommand are intentionally simplified.
But, each subcommand is implemented as a thin layer atop the packages in
github.com/sdboyer/thema/format/*. If the CLI lacks the fine-grained control
you require, it is recommended to write your own code generator using those packages.
`,
}

var genOapiLineageCmd = &cobra.Command{
	Use:   "openapi",
	Short: "Generate OpenAPI from a lineage",
	Long: `Generate OpenAPI from a lineage.

Generate an OpenAPI document containing a OpenAPI schema components representing a
single schema in a lineage.

Only OpenAPI 3.0 is currently supported.
`,
}

func (gc *genCommand) runOpenAPI(cmd *cobra.Command, args []string) error {
	f, err := openapi.GenerateSchema(gc.sch, nil)
	if err != nil {
		return err
	}

	var str string
	switch gc.format {
	case formatJSON:
		var b []byte
		b, err = rt.Context().BuildFile(f).MarshalJSON()
		if b != nil {
			nb := new(bytes.Buffer)
			err = json.Indent(nb, b, "", "  ")
			str = nb.String()
		}
	case formatYAML, formatYML:
		str, err = yaml.Marshal(rt.Context().BuildFile(f))
	}
	if err != nil {
		return err
	}
	fmt.Fprint(cmd.OutOrStdout(), str)
	return nil
}

var genJschLineageCmd = &cobra.Command{
	Use:   "jsonschema",
	Short: "Generate JSON Schema from a lineage",
	Long: `Generate JSON Schema from a lineage.

Generate a JSON Schema (draft 2020-12) document representing a single schema in a lineage,
and print it to stdout.
`,
}

func (gc *genCommand) runJSONSchema(cmd *cobra.Command, args []string) error {
	f, err := jsonschema.GenerateSchema(gc.sch)
	if err != nil {
		return err
	}

	var str string
	switch gc.format {
	case formatJSON:
		var b []byte
		b, err = rt.Context().BuildFile(f).MarshalJSON()
		if b != nil {
			nb := new(bytes.Buffer)
			err = json.Indent(nb, b, "", "  ")
			str = nb.String()
		}
	case formatYAML, formatYML:
		str, err = yaml.Marshal(rt.Context().BuildFile(f))
	}
	if err != nil {
		return err
	}
	fmt.Fprint(cmd.OutOrStdout(), str)
	return nil
}

var genGoBindingsLineageCmd = &cobra.Command{
	Short: "Generate Go bindings for a lineage",
	Long: `Generate Go bindings for a lineage.

Generate Go bindings to a Thema lineage. These bindings provide access to the
key Thema operations (see "thema help data") in a running Go program, for that
particular lineage.

If --bindtype is omitted, a basic LineageFactory is generated.
If --bindtype is provided, a ConvergentLineageFactory is also generated, layered
on top of the basic factory. The type itself is not generated by this command.

Output is written to the same directory that contains the lineage, in a file
named $NAME_bindings_gen.go, where $NAME is the lowercase string value of the
lineage's name.

The correctness of the generated embed.FS and CUE loading behavior is
sensitive to location of the generated file relative to a cue.mod directory,
if any, and in any parent directory. As such, if --stdout is passed, the
command cannot offer any correctness guarantees. The generator no longer
produces a themaFSFor$NAME() implementation. However, the lineage factory
still calls themaFSFor$NAME(), making it the responsibility of the user to
hand-write their own implementation in another .go file within the same
package.

LineageFactory: https://pkg.go.dev/github.com/sdboyer/thema#LineageFactory
ConvergentLineageFactory: https://pkg.go.dev/github.com/sdboyer/thema#ConvergentLineageFactory
`,
}

// checkPkgname refuses a --pkgname, name, that is not a Go package name.
// An empty name is the default, which each command derives for itself.
func checkPkgname(name string) error {
	if name != "" && (!gotoken.IsIdentifier(name) || name == "_") {
		return diag.New(diag.E0620).Headlinef("--pkgname names a Go package, and %q is not a Go package name", name).
			Help("name the package with a Go identifier that is not a keyword, such as ships").Err()
	}
	return nil
}

func (gc *genCommand) runGoBindings(cmd *cobra.Command, args []string) error {
	if err := checkPkgname(gc.pkgname); err != nil {
		return err
	}
	cfg := &gocode.BindingConfig{
		FactoryNameSuffix:   gc.suffix,
		PrivateFactory:      gc.private,
		TargetSchemaVersion: gc.sch.Version(),
		PackageName:         gc.pkgname,
	}

	if gc.lla.lincuepath != "" {
		cfg.CuePath = cue.ParsePath(gc.lla.lincuepath)
	}

	// emitting on stdout just skips all the complicated conditional gen bits
	if !gc.stdout {
		if !gc.noembed {
			cfg.EmbedPath = gc.epath
		}

		roots, err := findModRoot(gc.lla.absInput)
		if err != nil {
			return err
		}
		if roots.Exists() {
			if !gc.nofsfunc {
				cfg.LoadDir = "."
				cfg.CueModName = roots.Modname()
				rel, err := filepath.Rel(roots.Modpath(), gc.lla.absInput)
				if !gc.lla.pathIsDir {
					rel, err = filepath.Rel(roots.Modpath(), filepath.Dir(gc.lla.absInput))
				}
				if err != nil {
					return err
				}

				if roots.cuemod != "" && rel != "." {
					cfg.CueModName = filepath.ToSlash(path.Join(cfg.CueModName, rel))
				}
			}
		}
	}

	if gc.bindtype != "" {
		cfg.Assignee = dst.NewIdent(gc.bindtype)
	}

	f, err := gocode.GenerateLineageBinding(gc.lin, cfg)
	if err != nil {
		return err
	}

	buf := new(bytes.Buffer)
	if gc.stdout {
		fmt.Fprint(buf, goheader)
		buf.Write(f)
		fmt.Fprint(cmd.OutOrStdout(), buf.String())
		return nil
	}

	fmt.Fprintf(buf, goheaderp, gc.epath)
	buf.Write(f)

	path := gc.lla.absInput
	if !gc.lla.pathIsDir {
		path = filepath.Dir(path)
	}
	path = filepath.Join(path, fmt.Sprintf("%s_bindings_gen.go", strings.ToLower(gc.lin.Name())))
	err = os.WriteFile(path, buf.Bytes(), 0644)
	if err != nil {
		return err
	}

	if gc.quiet {
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "generated go bindings for %s at %s\n", gc.lin.Name(), path)
	return nil
}

func findModRoot(abspath string) (rootfind, error) {
	stat, err := os.Stat(abspath)
	if err != nil {
		return rootfind{}, diag.New(diag.E0601).Headlinef("%s cannot be read", displayPath(abspath)).Detail(diag.OSReason(err)).Err()
	}
	if !stat.IsDir() {
		abspath = filepath.Dir(abspath)
	}

	p := abspath
	r := rootfind{}

	for {
		cf, err := os.ReadFile(filepath.Join(p, "cue.mod", "module.cue"))
		if err == nil {
			r.cuemod, err = cuecontext.New().CompileBytes(cf).LookupPath(cue.MakePath(cue.Str("module"))).String()
			if err != nil {
				return rootfind{}, diag.New(diag.E0606).Headlinef("%s does not declare module", displayPath(filepath.Join(p, "cue.mod", "module.cue"))).Cause(err).Help(`a module file declares module: "<path>" and language: version: "<version>"`).Err()
			}
			r.cuemodpath = p
			break
		}

		if r.gomodpath == "" {
			gf, err := os.ReadFile(filepath.Join(p, "go.mod"))
			if err == nil {
				r.gomod = modfile.ModulePath(gf)
				r.gomodpath = p
			} else if !os.IsNotExist(err) {
				return rootfind{}, err
			}
		}

		if p == filepath.Dir(p) {
			break
		}
		p = filepath.Dir(p)
	}

	return r, nil
}

type rootfind struct {
	cuemod, cuemodpath string
	gomod, gomodpath   string
}

func (r rootfind) Exists() bool {
	return r.cuemod != "" || r.gomod != ""
}

func (r rootfind) Modname() string {
	if !r.Exists() {
		return ""
	}

	if r.cuemod != "" {
		return r.cuemod
	}
	return r.gomod
}

func (r rootfind) Modpath() string {
	if !r.Exists() {
		return ""
	}

	if r.cuemodpath != "" {
		return r.cuemodpath
	}
	return r.gomodpath
}

var genGoTypesLineageCmd = &cobra.Command{
	Use:   "gotypes -l <path> [-p <cue-path>] [-v <synver> [--pkgname <name>]] [-o <dir> [--check]]",
	Short: "Generate Go types from a lineage",
	Long: `Generate Go types from a lineage.

Generate a Go package for each schema in a lineage, declaring a type its
instances can be decoded into. Version X.Y is written to vX_Y/types.go, in
package vX_Y, and the type for lineage "name" is Name in every version; nested
structs and string enums get named types prefixed with it.

With -v, generate the one schema as types.go, in the package --pkgname names.

The files are written under the directory -o names, or printed to stdout as a
txtar archive. With --check, nothing is written, and the command fails for each
file under -o that is missing or differs from what would be generated.
`,
}

func (gc *genCommand) runGoTypes(cmd *cobra.Command, _ []string) error {
	if gc.pkgname != "" && gc.lla.verstr == "" {
		return diag.New(diag.E0620).Headline("--pkgname names the package of the one schema -v generates, and no -v was given").
			Help("without -v, each version's package is named for its directory, such as v0_0").Err()
	}
	if err := checkPkgname(gc.pkgname); err != nil {
		return err
	}
	if gc.check && gc.outdir == "" {
		return diag.New(diag.E0620).Headline("--check verifies the files under -o, and no -o was given").Err()
	}

	var fs *jenny.FS
	var err error
	if gc.lla.verstr == "" {
		fs, err = gocode.LineageTypes(gocode.LineageTypeConfig{}).GenerateFS(gc.lla.dl.lin)
	} else {
		fs, err = gocode.SchemaTypes(gocode.TypeConfig{PackageName: gc.pkgname}).GenerateFS(gc.lla.dl.sch)
	}
	if err != nil {
		return err
	}

	switch {
	case gc.outdir == "":
		var a txtar.Archive
		for _, f := range fs.AsFiles() {
			b, rerr := f.Render()
			if rerr != nil {
				return rerr
			}
			a.Files = append(a.Files, txtar.File{Name: f.Path(), Data: b})
		}
		_, err = cmd.OutOrStdout().Write(txtar.Format(&a))
		return err
	case gc.check:
		return driftDiagnostics(fs.Verify(cmd.Context(), gc.outdir), "-o", gc.outdir)
	default:
		return writeFS(cmd, fs, gc.outdir)
	}
}

// driftDiagnostics returns an E0628 for each file under dir, the directory
// the flag named flag gave, that jenny's Verify found missing or different,
// or could not read. Paths in the headlines are relative to dir, which a note
// names, so that a long directory does not push the headline past its limit.
func driftDiagnostics(err error, flag, dir string) error {
	if err == nil {
		return nil
	}
	under := func(b *diag.B) *diag.B {
		return b.Notef(token.NoPos, "the path is relative to %s, %s", flag, displayPath(dir)).
			Help("run the command without --check to regenerate it")
	}
	var pe *iofs.PathError
	if errors.As(err, &pe) {
		// Verify stops at a file it cannot read, and reports nothing else.
		return under(diag.New(diag.E0628).Headlinef("a generated file on disk can be read, and %s cannot", pe.Path)).Detail(diag.OSReason(err)).Err()
	}
	errs := []error{err}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		errs = j.Unwrap()
	}
	var ds cerrors.Error
	for _, e := range errs {
		var drift *jenny.DriftError
		if !errors.As(e, &drift) {
			return err
		}
		b := diag.New(diag.E0628)
		if drift.Missing {
			b = b.Headlinef("a generated file is on disk, and %s is missing", drift.Path)
		} else {
			b = b.Headlinef("a generated file on disk is what would be generated, and %s differs", drift.Path)
		}
		ds = cerrors.Append(ds, under(b).Err())
	}
	return ds
}

var genTSTypesLineageCmd = &cobra.Command{
	Use:   "tstypes",
	Short: "Generate TypeScript types from a lineage",
	Long: `Generate TypeScript types from a lineage.

Generate a JSON Schema document representing a single schema in a lineage.
`,
}

func (gc *genCommand) runTSTypes(cmd *cobra.Command, args []string) error {
	panic("TODO")
}

var goheader = `// THIS FILE IS GENERATED. EDITING IS FUTILE.
//
// Generated by "thema lineage gen"

`

var goheaderp = `// THIS FILE IS GENERATED. EDITING IS FUTILE.
//
// Generated by "thema lineage gen" from lineage defined in %s

`
