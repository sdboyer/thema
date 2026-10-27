package spike

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/ext"
	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// Q3: cel-go mechanics; Q4: CUE vs CEL divergence on the #82 op set.
func TestQ3Q4CEL(t *testing.T) {
	var rep strings.Builder
	ctx := cuecontext.New()

	// One CEL variable per referenced input path, no custom type provider.
	env, err := cel.NewEnv(
		cel.Variable("input_n", cel.IntType),
		cel.Variable("input_s", cel.StringType),
		cel.Variable("input_t", cel.StringType),
		cel.Variable("input_b", cel.BoolType),
		ext.Strings(), // for .upperAscii(), etc. Is it needed? see below
	)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&rep, "cel-go module: cel.dev/cel-go v0.33.0 (github.com/google/cel-go is the pre-rename path; v0.32+ declares cel.dev/cel-go)\n\n")

	eval := func(expr string, vars map[string]any) (ref.Val, *cel.Program, error) {
		ast, iss := env.Compile(expr)
		if iss.Err() != nil {
			return nil, nil, fmt.Errorf("compile: %v", iss.Err())
		}
		prg, err := env.Program(ast)
		if err != nil {
			return nil, nil, err
		}
		out, _, err := prg.Eval(vars)
		return out, &prg, err
	}
	cueEval := func(expr string) string {
		src := "x: " + expr
		if i := strings.Index(expr, "; "); i >= 0 && strings.HasPrefix(expr, "import") {
			src = expr[:i] + "\nx: " + expr[i+2:]
		}
		v := ctx.CompileString(src).LookupPath(cue.ParsePath("x"))
		if v.Err() != nil {
			return "ERR: " + firstLine(v.Err().Error())
		}
		if v.Kind() == cue.StringKind {
			s, _ := v.String()
			return fmt.Sprintf("%q", s)
		}
		return fmt.Sprint(v)
	}
	celEval := func(expr string, vars map[string]any) string {
		out, _, err := eval(expr, vars)
		if err != nil {
			return "ERR: " + firstLine(err.Error())
		}
		if out.Type() == types.StringType {
			return fmt.Sprintf("%q", out.Value())
		}
		return fmt.Sprintf("%v", out.Value())
	}
	row := func(what, cueExpr, celExpr string, vars map[string]any) {
		c := cueEval(cueExpr)
		l := celEval(celExpr, vars)
		mark := "same"
		if c != l {
			mark = "DIFFERENT"
		}
		fmt.Fprintf(&rep, "%-34s | CUE %-28s = %-22s | CEL %-30s = %-22s | %s\n", what, cueExpr, c, celExpr, l, mark)
	}

	fmt.Fprintln(&rep, "== Q4: divergence table ==")
	none := map[string]any{}
	row("int division, positive", "7 / 2", "7 / 2", none)
	row("int division, CUE quo", "quo(7, 2)", "7 / 2", none)
	row("int division, CUE div", "div(7, 2)", "7 / 2", none)
	row("int division, negative", "-7 / 2", "-7 / 2", none)
	row("int division, negative quo", "quo(-7, 2)", "-7 / 2", none)
	row("int division, negative div", "div(-7, 2)", "-7 / 2", none)
	row("remainder negative rem", "rem(-7, 2)", "-7 % 2", none)
	row("remainder negative mod", "mod(-7, 2)", "-7 % 2", none)
	row("division by zero", "div(7, 0)", "7 / 0", none)
	row("string len, ASCII", `len("hello")`, `size("hello")`, none)
	row("string len, multibyte", `len("héllo")`, `size("héllo")`, none)
	row("strings.Runes len", `import "strings"; len(strings.Runes("héllo"))`, `size("héllo")`, none)
	row("string len, emoji (4 bytes)", `len("a😀")`, `size("a😀")`, none)
	row("int -> string, interpolation", `"\(42)"`, `string(42)`, none)
	row("neg int -> string", `"\(-42)"`, `string(-42)`, none)
	row("bool -> string", `"\(true)"`, `string(true)`, none)
	row("concat", `"a" + "b"`, `"a" + "b"`, none)
	row("interp with str+int", `"x-\("s")-\(3)"`, `"x-" + "s" + "-" + string(3)`, none)
	row("ToUpper non-ASCII", `import "strings"; strings.ToUpper("héllo")`, `"héllo".upperAscii()`, none)
	row("int64 max + 1", "9223372036854775807 + 1", "9223372036854775807 + 1", none)
	row("int64 min - 1", "-9223372036854775808 - 1", "-9223372036854775808 - 1", none)
	row("int64 mul overflow", "4611686018427387904 * 2", "4611686018427387904 * 2", none)
	row("big literal", "18446744073709551616", "18446744073709551616", none)
	row("string compare", `"a" < "b"`, `"a" < "b"`, none)
	row("eq int", "3 == 3", "3 == 3", none)
	row("ternary", `[if 3 > 2 {"big"}, "small"][0]`, `3 > 2 ? "big" : "small"`, none)
	row("float interp (excluded by #82)", `"\(1.5)"`, `string(1.5)`, none)
	row("float interp 1.0", `"\(1.0)"`, `string(1.0)`, none)
	row("strings.Contains", `import "strings"; strings.Contains("abc","b")`, `"abc".contains("b")`, none)
	row("strings.HasPrefix", `import "strings"; strings.HasPrefix("abc","a")`, `"abc".startsWith("a")`, none)
	row("strings.Replace", `import "strings"; strings.Replace("aXbX","X","-",-1)`, `"aXbX".replace("X","-")`, none)
	row("strings.TrimSpace", `import "strings"; strings.TrimSpace(" a ")`, `" a ".trim()`, none)
	row("strings.Join", `import "strings"; strings.Join(["a","b"],",")`, `["a","b"].join(",")`, none)
	row("strings.Split", `import "strings"; strings.Split("a,b",",")`, `"a,b".split(",")`, none)
	row("strings.Index (bytes vs cp)", `import "strings"; strings.Index("héllo","l")`, `"héllo".indexOf("l")`, none)
	row("substring (bytes vs cp)", `import "strings"; strings.SliceRunes("héllo",1,3)`, `"héllo".substring(1,3)`, none)
	row("strconv.Atoi", `import "strconv"; strconv.Atoi("42")`, `int("42")`, none)
	row("strconv.Atoi bad", `import "strconv"; strconv.Atoi("4x")`, `int("4x")`, none)
	row("var arithmetic", "10 - 1", "input_n - 1", map[string]any{"input_n": int64(10)})
	row("var interpolation", `"\("s")-\(10)"`, `input_s + "-" + string(input_n)`, map[string]any{"input_n": int64(10), "input_s": "s"})
	row("table index", `{a: 1, b: 2}["a"]`, `{"a": 1, "b": 2}[input_t]`, map[string]any{"input_t": "a"})
	row("table index missing key", `{a: 1, b: 2}["z"]`, `{"a": 1, "b": 2}["z"]`, none)

	// Q3: overflow is a runtime error, not a wrap.
	fmt.Fprintln(&rep, "\n== Q3: overflow with variables ==")
	for _, n := range []int64{9223372036854775807, -9223372036854775808} {
		out, _, err := eval("input_n + 1", map[string]any{"input_n": n})
		fmt.Fprintf(&rep, "input_n=%d: input_n + 1 -> out=%v err=%v\n", n, out, err)
		out, _, err = eval("input_n - 1", map[string]any{"input_n": n})
		fmt.Fprintf(&rep, "input_n=%d: input_n - 1 -> out=%v err=%v\n", n, out, err)
	}
	out, _, err := eval("-input_n", map[string]any{"input_n": int64(-9223372036854775808)})
	fmt.Fprintf(&rep, "negate min: out=%v err=%v\n", out, err)

	// Q3: compile-once/eval-many cost.
	fmt.Fprintln(&rep, "\n== Q3: cost ==")
	expr := `input_s + "-" + string(input_n - 1)`
	start := time.Now()
	const N = 2000
	for i := 0; i < N; i++ {
		ast, iss := env.Compile(expr)
		if iss.Err() != nil {
			t.Fatal(iss.Err())
		}
		if _, err := env.Program(ast); err != nil {
			t.Fatal(err)
		}
	}
	compileEach := time.Since(start) / N
	ast, _ := env.Compile(expr)
	prg, _ := env.Program(ast)
	vars := map[string]any{"input_n": int64(10), "input_s": "s"}
	start = time.Now()
	const M = 200000
	for i := 0; i < M; i++ {
		if _, _, err := prg.Eval(vars); err != nil {
			t.Fatal(err)
		}
	}
	evalEach := time.Since(start) / M
	fmt.Fprintf(&rep, "compile+program: %v each; eval: %v each (ratio %.0fx)\n", compileEach, evalEach, float64(compileEach)/float64(evalEach))
	// Activation reuse instead of map: cel.Activation
	act, _ := cel.NewActivation(vars)
	start = time.Now()
	for i := 0; i < M; i++ {
		if _, _, err := prg.Eval(act); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Fprintf(&rep, "eval with prebuilt Activation: %v each\n", time.Since(start)/M)

	// Q3: type-check output type.
	ast, _ = env.Compile(expr)
	fmt.Fprintf(&rep, "\nstatic output type of %q: %v\n", expr, ast.OutputType())
	ast2, iss := env.Compile(`input_n + "x"`)
	fmt.Fprintf(&rep, "type error caught at compile: %v (ast nil=%v)\n", iss.Err() != nil, ast2 == nil)
	_, iss = env.Compile(`"héllo".upperAscii()`)
	fmt.Fprintf(&rep, "ext.Strings needed for upperAscii: compiles=%v\n", iss.Err() == nil)
	env2, _ := cel.NewEnv(cel.Variable("input_s", cel.StringType))
	_, iss = env2.Compile(`input_s.upperAscii()`)
	fmt.Fprintf(&rep, "without ext.Strings, upperAscii compiles=%v (%v)\n", iss.Err() == nil, firstLine(fmt.Sprint(iss.Err())))

	os.WriteFile("q3q4_report.txt", []byte(rep.String()), 0o644)
	t.Log("wrote q3q4_report.txt")
}
