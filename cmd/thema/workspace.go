package main

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sdboyer/thema/diag"
)

// workspace is the CLI's one view of the filesystem. Every file a command
// reads goes through fsys, which is os.DirFS of root, opened once per run by
// [openWorkspace]; nothing else reads the host's filesystem for a command's
// input, and nothing reads it for an excerpt. A file is named by its path
// within fsys, which is the name its positions carry: the library loaders
// are given fsys and name files that way, and [readSource] reads an excerpt
// from fsys by that name. Two files are never confused, however alike their
// paths below their lineages' directories.
//
// root is the directory nearest the working directory that holds it and the
// directory holding each path the command was given: every input has a name
// in fsys, with a base name of its own (a lineage's name is its directory's),
// and names stay short where they appear in prose.
type workspace struct {
	root string // absolute, cleaned
	fsys fs.FS
}

// ws is the run's workspace, once [openWorkspace] has opened it.
var ws *workspace

// openWorkspace opens the run's workspace, rooted where it holds the working
// directory and the directory of every one of paths, as given on the command
// line. It is
// called once, by each command before it reads anything; a second call
// within the root is a no-op, and one outside it is a bug in the command.
func openWorkspace(paths ...string) error {
	cwd, err := workingDir()
	if err != nil {
		return err
	}
	root := cwd
	for _, p := range paths {
		if p == "" || p == "-" {
			continue
		}
		abs, err := hostPath(p)
		if err != nil {
			return err
		}
		root = commonDir(root, filepath.Dir(abs))
	}
	if ws != nil {
		if _, ok := within(ws.root, root); ok {
			return nil
		}
		return diag.InternalErr(fmt.Errorf("the workspace at %s was opened again, for %s", ws.root, root))
	}
	ws = &workspace{root: root, fsys: os.DirFS(root)}
	return nil
}

// workingDir is the working directory, with symbolic links resolved, as
// [hostPath] resolves the directories of paths.
func workingDir() (string, error) {
	cwd, err := os.Getwd()
	if err == nil {
		cwd, err = filepath.EvalSymlinks(cwd)
	}
	if err != nil {
		return "", diag.New(diag.E0601).Headline("the working directory cannot be read").Detail(diag.OSReason(err)).Err()
	}
	return cwd, nil
}

// hostPath is p, as given on the command line, as an absolute path whose
// directory has its symbolic links resolved, so that it is found beneath the
// working directory whichever way either was reached (a temporary directory
// on macOS is reached through /var, a link to /private/var). p's own base is
// kept: a lineage's name is its directory's, even when that is a link. Of a
// directory that does not exist, the part that does is resolved.
func hostPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", diag.New(diag.E0601).Headlinef("%s cannot be resolved to a path", p).Detail(diag.OSReason(err)).Err()
	}
	dir, base := filepath.Dir(abs), filepath.Base(abs)
	var rest []string
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(append(append([]string{resolved}, rest...), base)...), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return abs, nil
		}
		rest = append([]string{filepath.Base(dir)}, rest...)
		dir = parent
	}
}

// commonDir returns the deepest directory holding both a and b, which are
// absolute and clean.
func commonDir(a, b string) string {
	for {
		if _, ok := within(a, b); ok {
			return a
		}
		parent := filepath.Dir(a)
		if parent == a {
			return a
		}
		a = parent
	}
}

// within returns p's path relative to dir, slash-separated, when p is dir or
// beneath it. Both are absolute and clean.
func within(dir, p string) (string, bool) {
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// name returns the workspace's name for p, a path as given on the command
// line: its path within fsys. p must be within the workspace, which
// [openWorkspace] made sure of for the paths it was given.
func (w *workspace) name(p string) (string, error) {
	abs, err := hostPath(p)
	if err != nil {
		return "", err
	}
	rel, ok := within(w.root, abs)
	if !ok {
		return "", diag.InternalErr(fmt.Errorf("%s is outside the workspace at %s", p, w.root))
	}
	return rel, nil
}

// osPath returns the host's path of the file named name, for writing it:
// fsys is read-only. Reading goes through fsys.
func (w *workspace) osPath(name string) string {
	return filepath.Join(w.root, filepath.FromSlash(name))
}

// display returns how a diagnostic shows the file named name: relative to the
// working directory when it is beneath it, and otherwise the host's absolute
// path. It is for showing only; the file is read by name.
func (w *workspace) display(name string) string {
	p := w.osPath(name)
	if cwd, err := workingDir(); err == nil {
		if rel, ok := within(cwd, p); ok {
			return filepath.FromSlash(rel)
		}
	}
	return p
}

// isName reports whether file is a name in the workspace, as opposed to a
// position in thema's own CUE or text read from stdin.
func isName(file string) bool {
	return ws != nil && fs.ValidPath(file) && file != stdinName && file != "."
}

// stdinName names the data read from stdin in positions.
const stdinName = "stdin"

// fsAbs is name as the CUE loader takes a path within its FS: rooted at "/".
func fsAbs(name string) string {
	return path.Join("/", name)
}
