// Package hoistre rewrites Go code that go-jsonschema generates so that each
// regular expression is compiled once, not on every call: the best case for
// the code it generates, which the benchmarks compare beside the code as
// generated.
package hoistre

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strconv"

	"golang.org/x/tools/go/ast/astutil"
)

// Hoist rewrites every regexp.MatchString(`pattern`, s) in src, a Go file,
// to a call of the MatchString method of a package-level regexp.MustCompile
// of the pattern, and an assignment of its result and error to the result
// alone. The file's package is renamed to pkg.
func Hoist(src []byte, pkg string) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	f.Name.Name = pkg

	var patterns []string
	index := map[string]int{}
	var failed error
	astutil.Apply(f, func(c *astutil.Cursor) bool {
		as, ok := c.Node().(*ast.AssignStmt)
		if !ok || len(as.Rhs) != 1 {
			return true
		}
		call, ok := as.Rhs[0].(*ast.CallExpr)
		if !ok || !isMatchString(call) {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || len(as.Lhs) != 2 {
			failed = fmt.Errorf("%s: regexp.MatchString is not of a pattern literal, assigned to a result and an error", fset.Position(call.Pos()))
			return false
		}
		pattern, err := strconv.Unquote(lit.Value)
		if err != nil {
			failed = err
			return false
		}
		i, seen := index[pattern]
		if !seen {
			i = len(patterns)
			index[pattern] = i
			patterns = append(patterns, lit.Value)
		}
		call.Fun = &ast.SelectorExpr{X: ast.NewIdent(fmt.Sprintf("re%d", i)), Sel: ast.NewIdent("MatchString")}
		call.Args = call.Args[1:]
		as.Lhs = as.Lhs[:1]
		return true
	}, nil)
	if failed != nil {
		return nil, failed
	}
	if len(patterns) == 0 {
		return nil, fmt.Errorf("no regexp.MatchString to hoist")
	}

	var b bytes.Buffer
	if err = format.Node(&b, fset, f); err != nil {
		return nil, err
	}
	b.WriteString("\n// Compiled once, by hoistre, where go-jsonschema compiles each on every call.\nvar (\n")
	for i, p := range patterns {
		fmt.Fprintf(&b, "\tre%d = regexp.MustCompile(%s)\n", i, p)
	}
	b.WriteString(")\n")
	return format.Source(b.Bytes())
}

func isMatchString(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "MatchString" || len(call.Args) != 2 {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == "regexp"
}
