package main

import (
	"fmt"

	upcue "cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"github.com/sdboyer/thema/diag"
	tastutil "github.com/sdboyer/thema/internal/astutil"
	"github.com/spf13/cobra"
)

var linCmd = &cobra.Command{
	Use:   "lineage <command>",
	Short: "Perform operations directly on lineages and schema",
	Long: `Perform operations directly on lineages and schema.

Create, modify, and validate Thema lineages. Generate representations
of Thema lineages and schema in other schema languages.
`,
}

func setupLineageCommand(cmd *cobra.Command) {
	cmd.AddCommand(linCmd)
	ic := new(initCommand)
	ic.setup(linCmd)

	bc := new(bumpCommand)
	bc.setup(linCmd)

	gc := new(genCommand)
	gc.setup(linCmd)

	fc := new(fmtCommand)
	fc.setup(linCmd)
}

func toSubpath(subpath string, f *ast.File) (*ast.File, error) {
	if subpath == "" {
		return f, nil
	}

	p := upcue.ParsePath(subpath)
	if p.Err() != nil {
		return nil, diag.New(diag.E0620).Headlinef("--cue-path %q is not a CUE path", subpath).Detail(p.Err().Error()).Err()
	}

	err := astutil.Sanitize(f)
	if err != nil {
		return nil, diag.InternalErr(fmt.Errorf("sanitizing the generated file: %w", err))
	}

	// in := ctx.BuildFile(f)
	// if in.Err() != nil {
	// 	return nil, fmt.Errorf("error when building value: %w", in.Err())
	// }

	out := empt().FillPath(p, empt())
	if out.Err() != nil {
		return nil, diag.InternalErr(fmt.Errorf("placing the lineage at --cue-path %s: %w", subpath, out.Err()))
	}

	var nf *ast.File
	switch x := tastutil.Format(out).(type) {
	case *ast.File:
		nf = x
	case ast.Expr:
		nf, err = astutil.ToFile(x)
		if err != nil {
			return nil, diag.InternalErr(fmt.Errorf("converting the generated expression to a file: %w", err))
		}
	}

	var done bool
	astutil.Apply(nf, func(c astutil.Cursor) bool {
		if x, ok := c.Node().(*ast.StructLit); ok {
			if len(x.Elts) == 0 {
				x.Elts = f.Decls
				done = true
			}
		}
		return !done
	}, nil)

	f.Decls = nf.Decls
	return f, nil
}
