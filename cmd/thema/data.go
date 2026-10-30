package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"cuelang.org/go/cue"
	cerrors "cuelang.org/go/cue/errors"
	"github.com/spf13/cobra"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/lacuna"
	"github.com/sdboyer/thema/vmux"
)

type dataCommand struct {
	format  string
	quiet   bool
	inbytes []byte

	datval cue.Value

	lla *lineageLoadArgs

	err error
}

func setupDataCommand(cmd *cobra.Command) {
	cmd.AddCommand(dataCmd)
	dc := new(dataCommand)
	dc.setup(dataCmd)
}

func (dc *dataCommand) setup(cmd *cobra.Command) {
	dc.lla = new(lineageLoadArgs)
	addLinPathVars(cmd, dc.lla)
	dataCmd.MarkPersistentFlagRequired("lineage")
	dataCmd.PersistentFlags().BoolVar(&dc.lla.tip, "tip", false, "bind a lineage in the authoring form with its tip, as its newest version: provisional, at the version it cuts as, and named \"tip\" by -v")

	dataCmd.AddCommand(validateCmd)
	validateCmd.Flags().StringVarP(&dc.lla.verstr, "version", "v", "", "schema syntactic version to validate data against. defaults to latest")
	validateCmd.Flags().StringVarP(&dc.format, "format", "e", "", "input data format. Autodetected by default, but can be constrained to \"json\" or \"yaml\".")
	validateCmd.Flags().BoolVarP(&dc.quiet, "quiet", "q", false, "emit no output, exit status only")
	validateCmd.PersistentPreRunE = mergeCobraefuncs(dc.lla.validateLineageInput, dc.lla.validateVersionInputOptional, dc.validateDataInput)
	validateCmd.RunE = dc.runValidate

	dataCmd.AddCommand(validateAnyCmd)
	validateAnyCmd.Flags().StringVarP(&dc.lla.verstr, "version", "v", "", "schema syntactic version to validate data against")
	validateAnyCmd.Flags().StringVarP(&dc.format, "format", "e", "", "input data format. Autodetected by default, but can be constrained to \"json\" or \"yaml\".")
	validateAnyCmd.Flags().BoolVarP(&dc.quiet, "quiet", "q", false, "emit no output, exit status only")
	validateAnyCmd.PersistentPreRunE = mergeCobraefuncs(dc.lla.validateLineageInput, dc.lla.validateVersionInputOptional, dc.validateDataInput)
	validateAnyCmd.RunE = dc.runValidateAny

	dataCmd.AddCommand(translateCmd)
	translateCmd.Flags().StringVarP(&dc.lla.verstr, "to", "v", "", "schema version to translate input data to")
	translateCmd.MarkFlagRequired("to")
	translateCmd.Flags().StringVarP(&dc.format, "format", "e", "", "input data format. Autodetected by default, but can be constrained to \"json\" or \"yaml\".")
	translateCmd.PersistentPreRunE = mergeCobraefuncs(dc.lla.validateLineageInput, dc.lla.validateVersionInput, dc.validateDataInput)
	translateCmd.RunE = dc.runTranslate

	dataCmd.AddCommand(hydrateCmd)
	hydrateCmd.Flags().StringVarP(&dc.lla.verstr, "version", "v", "", "schema syntactic version to validate data against")
	hydrateCmd.Flags().StringVarP(&dc.format, "format", "e", "", "input data format. Autodetected by default, but can be constrained to \"json\" or \"yaml\".")
	hydrateCmd.PersistentPreRunE = mergeCobraefuncs(dc.lla.validateLineageInput, dc.lla.validateVersionInputOptional, dc.validateDataInput)
	hydrateCmd.RunE = dc.runHydrate

	dataCmd.AddCommand(dehydrateCmd)
	dehydrateCmd.Flags().StringVarP(&dc.lla.verstr, "version", "v", "", "schema syntactic version to validate data against")
	dehydrateCmd.Flags().StringVarP(&dc.format, "format", "e", "", "input data format. Autodetected by default, but can be constrained to \"json\" or \"yaml\".")
	dehydrateCmd.PersistentPreRunE = mergeCobraefuncs(dc.lla.validateLineageInput, dc.lla.validateVersionInputOptional, dc.validateDataInput)
	dehydrateCmd.RunE = dc.runDehydrate
}

var dataCmd = &cobra.Command{
	Use:   "data <command>",
	Short: "Perform Thema operations on some input data",
	Long: `Perform Thema operations on some input data.
`,
}

var dataReuseText = `
A filesystem path to a Thema lineage must be provided. It may be relative or
absolute. Lineages are necessarily validated prior to validation of the input
data. All data operations are performed in the context of the provided lineage.

Data may be provided on stdin, or by passing a single path to a file as an
argument. Stdin is ignored if a path is provided. JSON and YAML inputs are
supported; the correct format is inferred. Only one object instance may be
validated per command invocation.
`

var validateCmd = &cobra.Command{
	Use:   "validate -l <lineage-fs-path> -v <synver> [-p <cue-path>] [-q] [-e <format>] [<data-fs-path>]",
	Short: "Validate some input data against a particular Thema schema",
	Long: `Validate some input data against a particular Thema schema.
` + dataReuseText + `
Success outputs nothing and exits 0. Failure outputs the validation problem
(unless quieted) and exits 1.
`,
	Args: cobra.MaximumNArgs(1),
}

func (dc *dataCommand) runValidate(cmd *cobra.Command, args []string) error {
	if !dc.datval.Exists() {
		panic("datval does not exist")
	}

	_, err := dc.lla.dl.sch.Validate(dc.datval)
	if err != nil {
		if dc.quiet {
			return silent{}
		}
		return err
	}
	return nil
}

// noSchemaAccepts is the failure of validating data against every schema in
// the lineage.
func noSchemaAccepts() error {
	return diag.New(diag.E0508).Headline("the data is an instance of no schema in the lineage").
		Help("validate it against one version with -v to see what that schema rejects").Err()
}

var validateAnyCmd = &cobra.Command{
	Use:   "validate-any -l <lineage-fs-path> [-p <cue-path>] [-v <synver>] [-q] [-e <format>] [<data-fs-path>]",
	Short: "Search a lineage for a schema that validates some input data",
	Long: `Search a lineage for a schema that validates some input data.
` + dataReuseText + `
Success outputs the schema version that matched and exits 0. Failure exits 1 and
outputs nothing.

If --version is passed, that version is checked first. If validation fails
against all schemas in the lineage, the error against the --version schema will
be printed.
`,
	Args: cobra.MaximumNArgs(1),
}

func (dc *dataCommand) runValidateAny(cmd *cobra.Command, args []string) error {
	if !dc.datval.Exists() {
		panic("datval does not exist")
	}

	var reterr error
	if dc.lla.dl.sch != nil {
		_, reterr = dc.lla.dl.sch.Validate(dc.datval)
		if reterr == nil {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", dc.lla.dl.versionLabel(dc.lla.dl.sch.Version()))
			return nil
		}
	}
	inst := dc.lla.dl.lin.ValidateAny(dc.datval)
	if inst != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", dc.lla.dl.versionLabel(inst.Schema().Version()))
		return nil
	}

	if dc.quiet {
		return silent{}
	}
	if reterr != nil {
		return reterr
	}
	return noSchemaAccepts()
}

var translateCmd = &cobra.Command{
	Use:   "translate -l <lineage-fs-path> [-p <cue-path>] [--to <synver>] [-e <format>] [<data-fs-path>]",
	Short: "Translate some valid input data from one schema to another",
	Long: `Translate some valid input data from one schema to another.
` + dataReuseText + `
Success outputs the translated object instance, the version the input validated
against, any emitted lacuna, and exits 0. Failure exits 1 with an informative
error.

Note that Thema's invariants (once finalized) guarantee that failures can only
arise during data input decoding or validation, never during translation.
`,
	Args: cobra.MaximumNArgs(1),
}

func (dc *dataCommand) runTranslate(cmd *cobra.Command, args []string) error {
	if !dc.datval.Exists() {
		panic("datval does not exist")
	}

	inst := dc.lla.dl.lin.ValidateAny(dc.datval)
	if inst == nil {
		return noSchemaAccepts()
	}

	// Prior validations checked that the schema version exists in the lineage
	tinst, lac, err := inst.Translate(dc.lla.dl.sch.Version())
	if err != nil {
		return err
	}
	if err = dc.validateTranslationResult(tinst, lac); err != nil {
		return err
	}

	// TODO support non-JSON output
	r := translationResult{
		From:    dc.lla.dl.versionLabel(inst.Schema().Version()),
		To:      dc.lla.dl.versionLabel(tinst.Schema().Version()),
		Result:  tinst.Underlying(),
		Lacunas: lac,
	}

	byt, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return diag.InternalErr(err)
	}
	buf := bytes.NewBuffer(byt)
	_, err = io.Copy(cmd.OutOrStdout(), buf)

	return err
}

type translationResult struct {
	From    string                    `json:"from"`
	To      string                    `json:"to,omitempty"`
	Result  cue.Value                 `json:"result"`
	Lacunas lacuna.TranslationLacunas `json:"lacunas"`
}

var hydrateCmd = &cobra.Command{
	Use:   "hydrate -l <lineage-fs-path> [-p <cue-path>] [-e <format>] [-v <synver>] [<data-fs-path>] ",
	Short: "Fill some valid input data with any schema-specified defaults",
	Long: `Fill some valid input data with any schema-specified defaults.
` + dataReuseText + `
Success outputs the input object, but fully hydrated with schema-specified
default values, if any. Input formatting (e.g. indent spaces) and/or object
key ordering are not maintained.

If a syntactic version is not provided (-v), the input data will be checked
for validity against all schemas in the lineage.
`,
	Args: cobra.MaximumNArgs(1),
}

func (dc *dataCommand) runHydrate(cmd *cobra.Command, args []string) error {
	if !dc.datval.Exists() {
		panic("datval does not exist")
	}

	var inst *thema.Instance
	var err error
	if dc.lla.dl.sch != nil {
		inst, err = dc.lla.dl.sch.Validate(dc.datval)
		if err != nil {
			return err
		}
	} else {
		inst = dc.lla.dl.lin.ValidateAny(dc.datval)
		if inst == nil {
			return noSchemaAccepts()
		}
	}

	// TODO support non-JSON output
	byt, err := json.MarshalIndent(inst.Hydrate().Underlying(), "", "  ")
	if err != nil {
		return diag.InternalErr(err)
	}
	_, err = io.Copy(cmd.OutOrStdout(), bytes.NewBuffer(byt))

	return err
}

var dehydrateCmd = &cobra.Command{
	Use:   "dehydrate -l <lineage-fs-path> [-p <cue-path>] [-e <format>] [-v <synver>] [<data-fs-path>] ",
	Short: "Remove all schema-specified defaults from some valid input data",
	Long: `Remove all schema-specified defaults from some valid input data.
` + dataReuseText + `
Success outputs the input data object  with all values removed that are implied
by defaults specified in its validating schema. Input object field orderings
and formatting are not maintained.

If a syntactic version is not provided (-v), the input data will be checked
for validity against all schemas in the lineage.
`,
	Args: cobra.MaximumNArgs(1),
}

func (dc *dataCommand) runDehydrate(cmd *cobra.Command, args []string) error {
	if !dc.datval.Exists() {
		panic("datval does not exist")
	}

	var inst *thema.Instance
	var err error
	if dc.lla.dl.sch != nil {
		inst, err = dc.lla.dl.sch.Validate(dc.datval)
		if err != nil {
			return err
		}
	} else {
		inst = dc.lla.dl.lin.ValidateAny(dc.datval)
		if inst == nil {
			return noSchemaAccepts()
		}
	}

	// TODO support non-JSON output
	byt, err := json.MarshalIndent(inst.Dehydrate().Underlying(), "", "  ")
	if err != nil {
		return diag.InternalErr(err)
	}
	_, err = io.Copy(cmd.OutOrStdout(), bytes.NewBuffer(byt))

	return err
}

// stdinHasInput reports whether a stdin with the given mode should be read
// for input data. Only an interactive terminal (a character device) is ruled
// out; pipes and files redirected onto stdin are both readable.
func stdinHasInput(mode fs.FileMode) bool {
	return mode&fs.ModeCharDevice == 0
}

func pathOrStdin(args []string) ([]byte, error) {
	var byt []byte
	switch len(args) {
	case 0:
		fi, err := os.Stdin.Stat()
		if err != nil {
			panic(err)
		}
		if !stdinHasInput(fi.Mode()) {
			return nil, diag.New(diag.E0613).Headline("no data was given").
				Help("pass the path to a JSON or YAML file, or pipe the data on stdin").Err()
		}

		byt, err = io.ReadAll(os.Stdin)
		if err != nil {
			return nil, diag.New(diag.E0613).Headline("the data on stdin cannot be read").Detail(diag.OSReason(err)).Err()
		}
		return byt, nil
	case 1:
		fi, err := os.Stat(args[0])
		if err != nil {
			return nil, diag.New(diag.E0613).Headlinef("the data path %s cannot be read", args[0]).Detail(diag.OSReason(err)).Err()
		}
		if fi.IsDir() {
			return nil, diag.New(diag.E0613).Headlinef("the data path %s is a directory", args[0]).
				Help("pass the path to one JSON or YAML file").Err()
		}

		f, err := os.Open(args[0])
		if err != nil {
			return nil, diag.New(diag.E0613).Headlinef("the data path %s cannot be opened", args[0]).Detail(diag.OSReason(err)).Err()
		}
		defer f.Close() // nolint: errcheck

		byt, err = io.ReadAll(f)
		if err != nil {
			return nil, diag.New(diag.E0613).Headlinef("the data path %s cannot be read", args[0]).Detail(diag.OSReason(err)).Err()
		}

	default:
		return nil, diag.New(diag.E0620).Headlinef("%d arguments were given, where the data is one path", len(args)).
			Help("pass the path to one JSON or YAML file, or pipe the data on stdin").Err()
	}

	return byt, nil
}

func (dc *dataCommand) validateDataInput(cmd *cobra.Command, args []string) error {
	var ext string

	byt, err := pathOrStdin(args)
	if err != nil {
		return err
	}
	if len(byt) > 0 && len(dc.inbytes) == 0 {
		dc.inbytes = byt
	}

	if len(args) == 1 {
		switch filepath.Ext(args[0]) {
		case ".json", ".ldjson":
			ext = "json"
		case ".yaml", ".yml":
			ext = "yaml"
		}
	}

	// The data's positions name its file, so that diagnostics point into it.
	name := "stdin"
	if len(args) == 1 {
		name = args[0]
	}
	inlineSources[name] = dc.inbytes
	jd := vmux.NewJSONCodec(name)
	yd := vmux.NewYAMLCodec(name)
	parseFailed := func(err error, format string) error {
		b := diag.New(diag.E0507).Headlinef("%s is not valid %s", name, format)
		var ce cerrors.Error
		if errors.As(err, &ce) {
			b.At(ce.Position())
		}
		return b.Detail(err.Error()).Err()
	}

	switch dc.format {
	case "":
		// Figure it out; try JSON first
		dc.datval, err = jd.Decode(rt.Underlying().Context(), dc.inbytes)
		if err == nil {
			dc.format = "json"
			break
		}
		// Nope, try yaml
		dc.datval, err = yd.Decode(rt.Underlying().Context(), dc.inbytes)
		if err == nil {
			dc.format = "yaml"
			break
		}
		// Double nope
		return diag.New(diag.E0611).Headlinef("%s is neither JSON nor YAML", name).
			Help("name the format with -e json or -e yaml to see what its parser reports").Err()

	case "json":
		if ext != "" && ext != "json" {
			return diag.New(diag.E0612).Headlinef("-e json was given, but %s has a %s extension", name, ext).Err()
		}

		dc.datval, err = jd.Decode(rt.Underlying().Context(), dc.inbytes)
		if err != nil {
			return parseFailed(err, "JSON")
		}
	case "yaml":
		if ext != "" && ext != "yaml" {
			return diag.New(diag.E0612).Headlinef("-e yaml was given, but %s has a %s extension", name, ext).Err()
		}

		dc.datval, err = yd.Decode(rt.Underlying().Context(), dc.inbytes)
		if err != nil {
			return parseFailed(err, "YAML")
		}
	default:
		return diag.New(diag.E0614).Headlinef("-e %s names a format thema does not read", dc.format).
			Help("-e takes json or yaml").Err()
	}

	return nil
}

// Everything here should become unnecessary once Thema's key invariants are in
// place
func (dc *dataCommand) validateTranslationResult(tinst *thema.Instance, _ lacuna.TranslationLacunas) error {
	if tinst == nil {
		panic("unreachable, thema.Translate() should never return a nil instance")
	}

	raw := tinst.Underlying()
	if !raw.Exists() {
		return diag.InternalErr(errors.New("a translated instance has no value"))
	}

	if raw.Err() != nil {
		return diag.InternalErr(fmt.Errorf("a translated instance has an error: %w", raw.Err()))
	}

	if !raw.IsConcrete() {
		return diag.InternalErr(errors.New("a translated instance is not concrete"))
	}

	return nil
}
