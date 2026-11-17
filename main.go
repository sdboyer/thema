// Command lensmigrate rewrites thema authoring-form lens blocks from the
// version-named shape to the parameter shape:
//
//	ForwardLens: {v0_0: _, v1_0: {...}, lacunas: {...}}
//	BackwardLens: {v1_0: _, v0_0: {...}}
//
// become
//
//	ForwardLens: (prev): {self: {...}, lacunas: {...}}
//	BackwardLens: (self): {prev: {...}}
//
// In a ForwardLens the instance it takes (the field whose value is _) is prev
// and its body is self; in a BackwardLens the instance it takes is self and
// its body is prev. Every reference to either name inside the block is
// renamed with it; labels and selectors are fields of the instances, and are
// left alone. The tip's name, tip, is treated like any other.
//
// Edits are byte ranges spliced into the original text, so layout and
// comments outside the edited tokens are kept as they were.
//
// It reads .thema files, the *.thema sections of .txtar files, and Go raw
// string literals (backquoted) that hold a version file or a txtar archive of
// them. A block it cannot read as the old shape is reported and left alone.
//
// Usage: lensmigrate [-w] path...
//
// Without -w it prints what it would change; with -w it writes the files.
package main

import (
	"bytes"
	"flag"
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/parser"
)

var write = flag.Bool("w", false, "write the files in place")

type report struct {
	changed, skipped int
}

var rep report

func main() {
	flag.Parse()
	for _, root := range flag.Args() {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			return file(path)
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Fprintf(os.Stderr, "blocks changed: %d, blocks skipped: %d\n", rep.changed, rep.skipped)
}

func file(path string) error {
	var conv func(string, []byte) ([]byte, bool)
	switch filepath.Ext(path) {
	case ".thema":
		conv = versionFile
	case ".txtar":
		conv = archive
	case ".go":
		conv = goFile
	default:
		return nil
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Contains(src, []byte("Lens")) {
		return nil
	}
	out, changed := conv(path, src)
	if !changed {
		return nil
	}
	fmt.Println("changed:", path)
	if *write {
		return os.WriteFile(path, out, 0o644)
	}
	return nil
}

// archive rewrites the *.thema sections of a txtar archive.
func archive(where string, src []byte) ([]byte, bool) {
	lines := bytes.SplitAfter(src, []byte("\n"))
	var out bytes.Buffer
	var sec []byte
	var name string
	changed := false
	flush := func() {
		if strings.HasSuffix(name, ".thema") {
			n, c := versionFile(where+":"+name, sec)
			changed = changed || c
			out.Write(n)
		} else {
			out.Write(sec)
		}
		sec = nil
	}
	for _, l := range lines {
		t := bytes.TrimRight(l, "\r\n")
		if bytes.HasPrefix(t, []byte("-- ")) && bytes.HasSuffix(t, []byte(" --")) && len(t) >= 6 {
			flush()
			out.Write(l)
			name = strings.TrimSpace(string(t[3 : len(t)-3]))
			continue
		}
		sec = append(sec, l...)
	}
	flush()
	return out.Bytes(), changed
}

// goFile rewrites the raw string literals of a Go file that hold a version
// file, or a txtar archive of them.
func goFile(where string, src []byte) ([]byte, bool) {
	fset := gotoken.NewFileSet()
	f, err := goparser.ParseFile(fset, where, src, goparser.ParseComments)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skip %s: %v\n", where, err)
		return src, false
	}
	// start and end span the literal, quotes included.
	type lit struct {
		start, end int
		quoted     bool
	}
	var lits []lit
	goast.Inspect(f, func(n goast.Node) bool {
		bl, ok := n.(*goast.BasicLit)
		if !ok || bl.Kind != gotoken.STRING || !strings.Contains(bl.Value, "Lens") {
			return true
		}
		off := fset.Position(bl.Pos()).Offset
		lits = append(lits, lit{off, off + len(bl.Value), !strings.HasPrefix(bl.Value, "`")})
		return true
	})
	changed := false
	var out bytes.Buffer
	last := 0
	for _, l := range lits {
		lw := fmt.Sprintf("%s:%d", where, bytes.Count(src[:l.start], []byte("\n"))+1)
		var body []byte
		if l.quoted {
			// An interpreted string is rewritten only where quoting it again
			// reproduces it exactly, so that no escape changes but the edits.
			u, err := strconv.Unquote(string(src[l.start:l.end]))
			if err != nil || strconv.Quote(u) != string(src[l.start:l.end]) {
				if bytes.Contains(src[l.start:l.end], []byte("Lens: {")) {
					fmt.Fprintf(os.Stderr, "skip %s: interpreted string does not requote exactly\n", lw)
					rep.skipped++
				}
				continue
			}
			body = []byte(u)
		} else {
			body = src[l.start+1 : l.end-1]
		}
		var n []byte
		var c bool
		if bytes.Contains(body, []byte("\n-- ")) || bytes.HasPrefix(body, []byte("-- ")) {
			n, c = archive(lw, body)
		} else {
			n, c = versionFile(lw, body)
		}
		if !c {
			continue
		}
		out.Write(src[last:l.start])
		if l.quoted {
			out.WriteString(strconv.Quote(string(n)))
		} else {
			out.WriteString("`")
			out.Write(n)
			out.WriteString("`")
		}
		last = l.end
		changed = true
	}
	out.Write(src[last:])
	return out.Bytes(), changed
}

type edit struct {
	start, end int
	text       string
}

// versionFile rewrites every old-shape lens block at the top of a version
// file's text.
func versionFile(where string, src []byte) ([]byte, bool) {
	f, err := parser.ParseFile(where, src, parser.ParseComments)
	if err != nil {
		if bytes.Contains(src, []byte("Lens")) {
			fmt.Fprintf(os.Stderr, "skip %s: does not parse: %v\n", where, firstLine(err.Error()))
			rep.skipped++
		}
		return src, false
	}
	var edits []edit
	for _, d := range f.Decls {
		fd, ok := d.(*ast.Field)
		if !ok {
			continue
		}
		name, _, _ := ast.LabelName(fd.Label)
		var in, out string
		switch name {
		case "ForwardLens":
			in, out = "prev", "self"
		case "BackwardLens":
			in, out = "self", "prev"
		default:
			continue
		}
		e, why := block(src, fd, in, out)
		if why != "" {
			fmt.Fprintf(os.Stderr, "skip %s: %s %s\n", where, name, why)
			rep.skipped++
			continue
		}
		rep.changed++
		edits = append(edits, e...)
	}
	if len(edits) == 0 {
		return src, false
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var b bytes.Buffer
	last := 0
	for _, e := range edits {
		if e.start < last {
			fmt.Fprintf(os.Stderr, "skip %s: overlapping edits\n", where)
			rep.skipped++
			return src, false
		}
		b.Write(src[last:e.start])
		b.WriteString(e.text)
		last = e.end
	}
	b.Write(src[last:])
	// The result must parse, or the edit was wrong.
	if _, err := parser.ParseFile(where, b.Bytes()); err != nil {
		fmt.Fprintf(os.Stderr, "skip %s: result does not parse: %v\n", where, firstLine(err.Error()))
		rep.skipped++
		return src, false
	}
	return b.Bytes(), true
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// block computes the edits for one lens block, or why it is not the old
// shape.
func block(src []byte, fd *ast.Field, in, out string) ([]edit, string) {
	s, ok := fd.Value.(*ast.StructLit)
	if !ok {
		return nil, "is not a struct"
	}
	if !s.Lbrace.IsValid() {
		return nil, "is not a braced struct (already migrated?)"
	}
	var param, result *ast.Field
	var paramName, resultName string
	for _, d := range s.Elts {
		switch d := d.(type) {
		case *ast.CommentGroup:
		case *ast.Field:
			label, isIdent, err := ast.LabelName(d.Label)
			if err != nil || !isIdent {
				return nil, "has a field whose label is not a name"
			}
			if label == "lacunas" {
				continue
			}
			if id, ok := d.Value.(*ast.Ident); ok && id.Name == "_" {
				if param != nil {
					return nil, "declares two instances it takes"
				}
				param, paramName = d, label
				continue
			}
			if _, ok := d.Value.(*ast.StructLit); ok {
				if result != nil {
					return nil, "has two bodies"
				}
				result, resultName = d, label
				continue
			}
			return nil, fmt.Sprintf("has a field %s that is neither the instance it takes nor its body", label)
		default:
			return nil, "holds a declaration that is not a field"
		}
	}
	if param == nil || result == nil {
		return nil, "lacks the instance it takes or its body"
	}
	if len(ast.Comments(param)) > 0 {
		return nil, "has comments on the instance it takes; migrate by hand"
	}
	var edits []edit
	// (in): before the block's brace.
	lb := s.Lbrace.Offset()
	edits = append(edits, edit{lb, lb, "(" + in + "): "})
	// Remove the parameter field.
	edits = append(edits, removeField(src, s, param))
	// Rename the body's label.
	rl := result.Label.(*ast.Ident)
	edits = append(edits, edit{rl.Pos().Offset(), rl.Pos().Offset() + len(rl.Name), out})
	// Rename every reference to either instance in the block.
	rename := map[string]string{paramName: in, resultName: out}
	skip := map[*ast.Ident]bool{}
	ast.Walk(s, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := n.Sel.(*ast.Ident); ok {
				skip[id] = true
			}
		case *ast.Field:
			if id, ok := n.Label.(*ast.Ident); ok {
				skip[id] = true
			}
		case *ast.Ident:
			if to, ok := rename[n.Name]; ok && !skip[n] && n.Name != to {
				o := n.Pos().Offset()
				if string(src[o:o+len(n.Name)]) != n.Name {
					panic(fmt.Sprintf("offset mismatch at %d", o))
				}
				edits = append(edits, edit{o, o + len(n.Name), to})
			}
		}
		return true
	}, nil)
	return edits, ""
}

// removeField is the edit that removes f from s: its whole line when it is
// alone on it, and otherwise the field up to the next element.
func removeField(src []byte, s *ast.StructLit, f *ast.Field) edit {
	start := f.Pos().Offset()
	end := f.End().Offset()
	ls := start
	for ls > 0 && (src[ls-1] == ' ' || src[ls-1] == '\t') {
		ls--
	}
	le := end
	for le < len(src) && (src[le] == ' ' || src[le] == '\t' || src[le] == ',') {
		le++
	}
	if (ls == 0 || src[ls-1] == '\n') && le < len(src) && src[le] == '\n' {
		return edit{ls, le + 1, ""}
	}
	// Inline: remove up to the next element's start.
	for i, d := range s.Elts {
		if d == f && i+1 < len(s.Elts) {
			return edit{start, s.Elts[i+1].Pos().Offset(), ""}
		}
	}
	return edit{start, le, ""}
}
