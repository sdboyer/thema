package thema

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cjson "cuelang.org/go/encoding/json"
	"cuelang.org/go/pkg/strings"
	"github.com/stretchr/testify/require"

	"github.com/sdboyer/thema/diag"
	terrors "github.com/sdboyer/thema/errors"
	"github.com/sdboyer/thema/internal/diagtest"
	"github.com/sdboyer/thema/internal/txtartest/vanilla"
)

// Validation-related test cases look for `*.data.json` files within
// the txtar archives, describing input data to validate against the lineage.
// The expected results are described in a file matching the input file.
// Example:
// * data file name: `firstfieldAsInt32.data.json`
// * result file name: `firstfieldAsInt32`
func TestValidate(t *testing.T) {
	test := vanilla.TxTarTest{
		Root:    "./testdata/lineage",
		Name:    "validate/TestValidate",
		ThemaFS: CueJointFS,
	}

	ctx := cuecontext.New()
	rt := NewRuntime(ctx)

	test.Run(t, func(tc *vanilla.Test) {
		req := require.New(tc)

		val := ctx.BuildInstance(tc.Instance())
		lineage, err := BindLineage(val, rt)
		req.NoError(err)

		// The archive's files, by the names their positions carry: the lineage
		// is loaded at the virtual root, the data under its own name.
		source := func(file string) ([]byte, bool) {
			for _, f := range tc.Archive.Files {
				if f.Name == file || "/"+f.Name == file {
					return f.Data, true
				}
			}
			return nil, false
		}

		for _, file := range tc.Archive.Files {
			if !strings.HasSuffix(file.Name, ".data.json") {
				continue
			}

			data, err := decodeData(rt, file.Name, string(file.Data))
			req.NoError(err)

			_, err = lineage.Latest().Validate(data)
			req.Error(err, "The data shouldn't be valid for the schema")
			req.ErrorIs(err, terrors.ErrInvalidData)

			ds := diag.Diagnostics(err)
			req.NotEmpty(ds, "a validation failure is made of diagnostics")
			diagtest.Lint(tc, ds, diagtest.Normal)

			outputFileName := filepath.Base(strings.TrimSuffix(file.Name, ".data.json"))
			var b bytes.Buffer
			diag.Render(&b, ds, diag.RenderOptions{Source: source})
			_, err = tc.Writer(outputFileName).Write(b.Bytes())
			req.NoError(err)
		}
	})
}

func decodeData(rt *Runtime, name, inputJSON string) (cue.Value, error) {
	if inputJSON == "" {
		return cue.Value{}, errors.New("test error - data is missing")
	}

	ctx := rt.Underlying().Context()
	expr, err := cjson.Extract(name, []byte(inputJSON))
	if err != nil {
		return cue.Value{}, fmt.Errorf("test error - failed to decode input data: %w", err)
	}

	return ctx.BuildExpr(expr), nil
}
