package compile

import (
	"fmt"
	"strings"
	"testing"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/parser"
)

// TestFormatComments pins how Format lays out comments. Blank lines between
// declarations are layout and are replaced by thema's, but a blank line next
// to a comment decides what the comment is attached to: a comment group
// directly above a declaration is its doc comment, and one a blank line away
// is not. Each case is formatted, formatted again to check that Format is
// idempotent, and parsed back to check that every comment group is attached
// as it was in the input.
func TestFormatComments(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "detached comment above a doc comment",
			in:   "// Internal note, detached.\n\n// The user-facing description.\nSchema: {\n\ta?: string\n}\n",
			want: "// Internal note, detached.\n\n// The user-facing description.\nSchema: {\n\ta?: string\n}\n",
		},
		{
			name: "detached comment after the last block",
			in:   "// note\n\n// doc\nSchema: {\n\ta!: string\n}\n\n// note2\n",
			want: "// note\n\n// doc\nSchema: {\n\ta!: string\n}\n\n// note2\n",
		},
		{
			name: "comment on its own line after the last block",
			in:   "Schema: {\n\ta: string\n}\n// end\n",
			want: "Schema: {\n\ta: string\n}\n// end\n",
		},
		{
			name: "comment after the last field",
			in:   "Schema: {\n\ta: string\n\t// tight\n}\n",
			want: "Schema: {\n\ta: string\n\t// tight\n}\n",
		},
		{
			name: "groups above a field",
			in:   "Schema: {\n\t// det\n\n\t// doc a\n\ta: string // line\n\n\t// trailing\n}\n",
			want: "Schema: {\n\t// det\n\n\t// doc a\n\ta: string // line\n\n\t// trailing\n}\n",
		},
		{
			name: "detached comment between fields",
			in:   "Schema: {\n\ta: 1\n\t// t\n\n\tb: 2\n}\n",
			want: "Schema: {\n\ta: 1\n\t// t\n\n\tb: 2\n}\n",
		},
		{
			name: "detached comment above the first field",
			in:   "Schema: {\n\n\t// det\n\n\ta: 1\n}\n",
			want: "Schema: {\n\t// det\n\n\ta: 1\n}\n",
		},
		{
			name: "detached comment between blocks",
			in:   "Schema: {\n\ta: 1\n}\n\n// between\n\n// doc\nForwardLens: {\n\tv0_0: _\n}\n",
			want: "Schema: {\n\ta: 1\n}\n\n// between\n\n// doc\nForwardLens: {\n\tv0_0: _\n}\n",
		},
		{
			// Layout, not a comment, decides the blank lines between
			// declarations, doc comments or not.
			name: "doc comments follow the layout",
			in:   "Schema: {\n\ta: string\n\n\t// doc b\n\tb: int\n}\n// doc\nForwardLens: {\n\tv0_0: _\n}\n",
			want: "Schema: {\n\ta: string\n\t// doc b\n\tb: int\n}\n\n// doc\nForwardLens: {\n\tv0_0: _\n}\n",
		},
		{
			name: "comment ending the line of the opening brace",
			in:   "Schema: { // after brace\n\ta: 1\n}\n",
			want: "Schema: { // after brace\n\ta: 1\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatted(t, "in", []byte(tc.in))
			if got != tc.want {
				t.Errorf("formatted as\n%s\nwant\n%s", got, tc.want)
			}
			if again := formatted(t, "formatted", []byte(got)); again != got {
				t.Errorf("formatting again gives\n%s\nwant\n%s", again, got)
			}
			if in, out := commentAttachments(t, tc.in), commentAttachments(t, got); in != out {
				t.Errorf("comments attached as\n%s\nwant\n%s", out, in)
			}
		})
	}
}

// commentAttachments describes, one line per comment group in order, what
// the parser attaches each group of src to and how.
func commentAttachments(t *testing.T, src string) string {
	t.Helper()
	f, err := parser.ParseFile("", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	ast.Walk(f, func(n ast.Node) bool {
		owner := fmt.Sprintf("%T", n)
		if x, ok := n.(*ast.Field); ok {
			owner = fmt.Sprintf("field %s", x.Label)
		}
		for _, cg := range ast.Comments(n) {
			lines = append(lines, fmt.Sprintf("%s: doc=%t line=%t position=%d %q", owner, cg.Doc, cg.Line, cg.Position, cg.Text()))
		}
		return true
	}, nil)
	return strings.Join(lines, "\n")
}
