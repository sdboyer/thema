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

// TestValidateDataConstruction checks that a validation failure is described
// the same however the data reached Validate: read from a file, compiled from
// a string or bytes with no file name, or built in Go with no position at all.
// CUE orders a conflict's values by position, so a diagnostic that decided
// which value is the data's from positions swapped them for some of these.
func TestValidateDataConstruction(t *testing.T) {
	const lin = `name: "construction"
schemas: "0": "0": schema: {
	c?: bool
	s?: "a"
	m?: "x"
	n?: int
	l?: [...{x: int}]
}
`
	lineages := map[string]func(*cue.Context) cue.Value{
		"lineage file":   func(ctx *cue.Context) cue.Value { return ctx.CompileString(lin, cue.Filename("lineage.cue")) },
		"lineage string": func(ctx *cue.Context) cue.Value { return ctx.CompileString(lin) },
	}

	// Each construction gets the data as text and as a Go value; nil means
	// the data has no form that construction can build.
	constructions := map[string]func(t *testing.T, ctx *cue.Context, text string, gov any) cue.Value{
		"json file": func(t *testing.T, ctx *cue.Context, text string, gov any) cue.Value {
			if gov == nil {
				t.Skip("the data is not JSON")
			}
			expr, err := cjson.Extract("data.json", []byte(text))
			require.NoError(t, err)
			return ctx.BuildExpr(expr)
		},
		"CompileString": func(_ *testing.T, ctx *cue.Context, text string, _ any) cue.Value {
			return ctx.CompileString(text)
		},
		"CompileBytes": func(_ *testing.T, ctx *cue.Context, text string, _ any) cue.Value {
			return ctx.CompileBytes([]byte(text))
		},
		"Encode": func(t *testing.T, ctx *cue.Context, _ string, gov any) cue.Value {
			if gov == nil {
				t.Skip("the data cannot be built in Go")
			}
			return ctx.Encode(gov)
		},
	}

	cases := []struct {
		name     string
		text     string
		gov      any
		code     diag.Code
		headline string
	}{
		{
			name:     "kind conflict",
			text:     `{"c": "yes"}`,
			gov:      map[string]any{"c": "yes"},
			code:     diag.E0503,
			headline: `c in the data is "yes", but schema 0.0 declares it as bool`,
		},
		{
			name:     "kind conflict in a list",
			text:     `{"l": [{"x": "q"}]}`,
			gov:      map[string]any{"l": []any{map[string]any{"x": "q"}}},
			code:     diag.E0503,
			headline: `l.0.x in the data is "q", but schema 0.0 declares it as int`,
		},
		{
			// A struct is named by its kind; its contents rarely fit.
			name:     "kind conflict with a struct",
			text:     `{"n": {"p": 1, "q": [1, 2, 3]}}`,
			gov:      map[string]any{"n": map[string]any{"p": 1, "q": []any{1, 2, 3}}},
			code:     diag.E0503,
			headline: "n in the data is a struct, but schema 0.0 declares it as int",
		},
		{
			name:     "kind conflict with a list",
			text:     `{"c": [true, false]}`,
			gov:      map[string]any{"c": []any{true, false}},
			code:     diag.E0503,
			headline: "c in the data is a list, but schema 0.0 declares it as bool",
		},
		{
			// A long value is shortened to fit the headline.
			name:     "kind conflict with a long string",
			text:     `{"n": "` + fmt.Sprintf("%0100d", 0) + `"}`,
			gov:      map[string]any{"n": fmt.Sprintf("%0100d", 0)},
			code:     diag.E0503,
			headline: `n in the data is "` + fmt.Sprintf("%056d", 0) + `..., but schema 0.0 declares it as int`,
		},
		{
			name:     "value conflict",
			text:     `{"s": "b"}`,
			gov:      map[string]any{"s": "b"},
			code:     diag.E0504,
			headline: `s in the data is "b", but schema 0.0 requires "a"`,
		},
		{
			// CUE's message writes the value as "a\nb", but formats the
			// data's value as a multi-line string: the sides must be compared
			// as values, not as text.
			name:     "value conflict, multi-line",
			text:     `{"m": "a\nb"}`,
			gov:      map[string]any{"m": "a\nb"},
			code:     diag.E0504,
			headline: `m in the data is "a\nb", but schema 0.0 requires "x"`,
		},
		{
			name:     "not concrete",
			text:     `{n: int}`,
			code:     diag.E0506,
			headline: "n in the data is not concrete: it is still int",
		},
	}

	for lname, mklin := range lineages {
		for cname, mkdata := range constructions {
			for _, tc := range cases {
				t.Run(lname+"/"+cname+"/"+tc.name, func(t *testing.T) {
					ctx := cuecontext.New()
					l, err := BindLineage(mklin(ctx), NewRuntime(ctx))
					require.NoError(t, err)

					_, err = l.Latest().Validate(mkdata(t, ctx, tc.text, tc.gov))
					require.Error(t, err)
					ds := diag.Diagnostics(err)
					require.Len(t, ds, 1, "%v", ds)
					require.Equal(t, tc.code, ds[0].Code)
					require.Equal(t, tc.headline, ds[0].Headline)
				})
			}
		}
	}
}
