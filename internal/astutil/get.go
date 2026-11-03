package astutil

import (
	"fmt"
	"strconv"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"
)

// GetFieldByLabel returns the ast.Field with a given label from a struct-ish input.
func GetFieldByLabel(n ast.Node, label string) (*ast.Field, error) {
	var d []ast.Decl
	switch x := n.(type) {
	case *ast.File:
		d = x.Decls
	case *ast.StructLit:
		d = x.Elts
	default:
		return nil, fmt.Errorf("not an *ast.File or *ast.StructLit")
	}

	for _, el := range d {
		if isFieldWithLabel(el, label) {
			return el.(*ast.Field), nil
		}
	}

	return nil, fmt.Errorf("no field with label %q", label)
}

func strEq(lit *ast.BasicLit, str string) bool {
	if lit.Kind != token.STRING {
		return false
	}
	ls, _ := strconv.Unquote(lit.Value)
	return str == ls || str == lit.Value
}

func identStrEq(id *ast.Ident, str string) bool {
	if str == id.Name {
		return true
	}
	ls, _ := strconv.Unquote(id.Name)
	return str == ls
}

func isFieldWithLabel(n ast.Node, label string) bool {
	if x, is := n.(*ast.Field); is {
		if l, is := x.Label.(*ast.BasicLit); is {
			return strEq(l, label)
		}
		if l, is := x.Label.(*ast.Ident); is {
			return identStrEq(l, label)
		}
	}
	return false
}
