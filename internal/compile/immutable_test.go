package compile

import (
	"fmt"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
)

// An error in a file under history/ is an immutability violation: the file's
// errors are led by one E0209, naming its version and saying to restore the
// file, and none of them says how to edit it. That holds on every path that
// reads the history, and for what binding finds as well as what compiling
// does. An error in the tip is not one, and an error already worded as one,
// E0207, is not led again.
func TestImmutableHistory(t *testing.T) {
	const (
		h00, h01, h10 = HistoryDir + "/0.0" + Ext, HistoryDir + "/0.1" + Ext, HistoryDir + "/1.0" + Ext
		// A ForwardLens that maps crew from a field v00 does not declare.
		readsNope = v10 + "\nForwardLens: {\n\tv0_0: _\n\tv1_0: {\n\t\tcrew: v0_0.nope\n\t}\n}\n"
		// The same lens, also mapping a field v10 does not declare.
		readsNopeWritesX = v10 + "\nForwardLens: {\n\tv0_0: _\n\tv1_0: {\n\t\tcrew: v0_0.nope\n\t\tx:    1\n\t}\n}\n"
		// A ForwardLens in the tip that produces nothing for crew.
		tipNoCrew = v10 + "\nForwardLens: {\n\tv0_0: _\n\ttip: {\n\t\tname: v0_0.name\n\t}\n}\n"
		unclosed  = "Schema: {\n\tname: string\n"
		pkg       = "package ship\n\n" + v10
		// v0.1 numbered minor, and not backward compatible with v00.
		incompatible = "Schema: {\n\tname: int\n}\n"
		// A lens that compiles, and that binding finds may compute a value its
		// target rejects.
		narrow00 = "Schema: {\n\ta: int32\n}\n"
		narrow10 = "Schema: {\n\ta: int32 & >=0\n}\n\nForwardLens: {\n\tv0_0: _\n\tv1_0: {\n\t\ta: v0_0.a * 2\n\t}\n}\n\nBackwardLens: {\n\tv1_0: _\n\tv0_0: {\n\t\ta: v1_0.a\n\t}\n}\n"
	)
	const leadOne = "immutability violation: v1.0 is under history/, and has an error"
	// One diagnostic: its code, the file it is in, and whether it gives help.
	type want struct {
		code diag.Code
		file string
		help bool
	}
	type run func(fs []File, opts ...Option) error
	ctx := cuecontext.New()
	var (
		compile run = func(fs []File, opts ...Option) error {
			_, err := Compile(ctx, "ship", fs, opts...)
			return err
		}
		develop run = func(fs []File, opts ...Option) error {
			_, err := Development(ctx, "ship", fs, opts...)
			return err
		}
		readsTip run = func(fs []File, opts ...Option) error {
			_, err := ReadTip(ctx, fs, opts...)
			return err
		}
		checkClean run = func(fs []File, opts ...Option) error {
			return CheckClean(ctx, fs, opts...)
		}
		bind run = func(fs []File, opts ...Option) error {
			c, err := Compile(ctx, "ship", fs, opts...)
			if err != nil {
				return fmt.Errorf("compiling: %w", err)
			}
			_, err = thema.BindLineage(ctx.BuildFile(c.File), cue.Path{}, thema.NewRuntime(ctx), thema.WithVocabulary(Vocabulary(c)))
			return err
		}
	)
	cases := []struct {
		name  string
		run   run
		files []File
		at    string
		want  []want
		// lead is E0209's headline, when one is wanted.
		lead string
	}{
		{
			name: "a lens error", run: compile,
			files: files(h00, v00, h10, readsNope),
			want:  []want{{diag.E0209, h10, true}, {diag.E0340, h10, false}},
			lead:  leadOne,
		},
		{
			name: "two errors in one file", run: compile,
			files: files(h00, v00, h10, readsNopeWritesX),
			want:  []want{{diag.E0209, h10, true}, {diag.E0340, h10, false}, {diag.E0341, h10, false}},
			lead:  "immutability violation: v1.0 is under history/, and has 2 errors",
		},
		{
			name: "an error in each of two files", run: compile,
			files: files(h00, unclosed, h10, pkg),
			want:  []want{{diag.E0209, h00, true}, {diag.E0102, h00, false}, {diag.E0209, h10, true}, {diag.E0103, h10, false}},
		},
		{
			name: "a lens error on the development path", run: develop,
			files: files(h00, v00, h10, readsNope, TipFile, v10),
			want:  []want{{diag.E0209, h10, true}, {diag.E0340, h10, false}},
			lead:  leadOne,
		},
		{
			name: "a lens error in the tip", run: develop,
			files: files(h00, v00, TipFile, tipNoCrew),
			want:  []want{{diag.E0346, TipFile, true}},
		},
		{
			name: "a lens error, positioned under a directory", run: compile, at: "ops/ship",
			files: files(h00, v00, h10, readsNope),
			want:  []want{{diag.E0209, "ops/ship/" + h10, true}, {diag.E0340, "ops/ship/" + h10, false}},
		},
		{
			name: "a number the schemas contradict", run: compile,
			files: files(h00, v00, h01, incompatible),
			want:  []want{{diag.E0207, h01, true}},
		},
		{
			name: "a syntax error, reading the tip", run: readsTip,
			files: files(h00, unclosed, TipFile, v00),
			want:  []want{{diag.E0209, h00, true}, {diag.E0102, h00, false}},
		},
		{
			name: "a syntax error, checking the tip is clean", run: checkClean,
			files: files(h00, unclosed, TipFile, v00),
			want:  []want{{diag.E0209, h00, true}, {diag.E0102, h00, false}},
		},
		{
			name: "a lens binding refuses", run: bind,
			files: files(h00, narrow00, h10, narrow10),
			want:  []want{{diag.E0209, h10, true}, {diag.E0367, h10, false}},
			lead:  leadOne,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var opts []Option
			if c.at != "" {
				opts = append(opts, At(c.at))
			}
			err := c.run(c.files, opts...)
			ds := diag.Diagnostics(err)
			var got []want
			for _, d := range ds {
				file := ""
				if d.Primary != nil {
					file = d.Primary.File
				}
				got = append(got, want{d.Code, file, len(d.Help) > 0})
			}
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Fatalf("diagnostics = %v, want %v; err: %v", got, c.want, err)
			}
			for _, d := range ds {
				if d.Code != diag.E0209 {
					continue
				}
				if c.lead != "" && d.Headline != c.lead {
					t.Errorf("E0209 headline = %q, want %q", d.Headline, c.lead)
				}
				if d.Primary.Line != 1 || d.Primary.Col != 1 {
					t.Errorf("E0209 is at %d:%d, want the start of the file", d.Primary.Line, d.Primary.Col)
				}
				if h := strings.Join(d.Help, "\n"); !strings.Contains(h, "restore it, and make the change in "+TipFile) {
					t.Errorf("E0209 help = %q, want it to say to restore the file", h)
				}
			}
		})
	}
}
