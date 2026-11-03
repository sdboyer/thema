package report

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// WorkingTree names the working tree as a revision: the files on disk,
// committed or not.
const WorkingTree = ""

// Repo reads a corpus from a git repository, at a revision or from the working
// tree, without checking anything out.
type Repo struct {
	// Dir is a directory in the repository's working tree. Git runs there,
	// and corpus directories are relative to it.
	Dir string
}

// Range is the two revisions a report compares. After is WorkingTree when the
// range names one revision.
type Range struct {
	Before, After string
	// Label is the range as it was written.
	Label string
}

// AfterLabel names the after side for display.
func (r Range) AfterLabel() string {
	if r.After == WorkingTree {
		return "working tree"
	}
	return r.After
}

// ParseRange reads a range: "A..B" compares A with B, "A...B" compares the
// merge base of A and B with B (what a pull request from B into A changes),
// and "A" compares A with the working tree.
func (r Repo) ParseRange(s string) (Range, error) {
	rg := Range{Label: s}
	if strings.TrimSpace(s) == "" {
		return rg, errors.New("the range is empty: name a revision")
	}
	if a, b, ok := strings.Cut(s, "..."); ok {
		if a == "" || b == "" {
			return rg, fmt.Errorf("range %q: name both revisions", s)
		}
		base, err := r.git(nil, "merge-base", a, b)
		if err != nil {
			return rg, err
		}
		rg.Before, rg.After = strings.TrimSpace(string(base)), b
	} else if a, b, ok := strings.Cut(s, ".."); ok {
		if a == "" || b == "" {
			return rg, fmt.Errorf("range %q: name both revisions", s)
		}
		rg.Before, rg.After = a, b
	} else {
		rg.Before = s
	}
	for _, rev := range []string{rg.Before, rg.After} {
		if rev == WorkingTree {
			continue
		}
		if _, err := r.git(nil, "rev-parse", "--verify", "--quiet", rev+"^{commit}"); err != nil {
			return rg, fmt.Errorf("range %q: %s is not a commit", s, rev)
		}
	}
	return rg, nil
}

// Compare reads the corpus in dir, relative to r.Dir, at both revisions of
// the range rng (as ParseRange reads it), and compares them. Only the before
// revision may lack the corpus directory, as the revision before the corpus
// was added does; a missing after side is a wrong directory, and would
// compare two empty corpora and report nothing.
func (r Repo) Compare(dir, rng string) (*Report, error) {
	rg, err := r.ParseRange(rng)
	if err != nil {
		return nil, err
	}
	before, err := r.Load(rg.Before, dir)
	if err != nil {
		return nil, err
	}
	after, err := r.Load(rg.After, dir)
	if err != nil {
		return nil, err
	}
	if after.Missing {
		return nil, fmt.Errorf("the corpus directory %s does not exist in %s", dir, rg.AfterLabel())
	}
	return Build(dir, rg, before, after), nil
}

// Load reads the corpus in dir, relative to r.Dir, at rev. A directory that
// does not exist at rev is an empty corpus marked Missing, as it is in the
// revision before the corpus was added.
func (r Repo) Load(rev, dir string) (*Corpus, error) {
	if filepath.IsAbs(dir) {
		return nil, fmt.Errorf("corpus directory %s: give it relative to %s", dir, r.Dir)
	}
	if rev == WorkingTree {
		return r.loadWorkingTree(dir)
	}
	prefix, err := r.git(nil, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	full := path.Join(strings.TrimSpace(string(prefix)), filepath.ToSlash(dir))
	list, err := r.git(nil, "ls-tree", "-z", "--full-tree", "--name-only", rev, "--", full+"/")
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		c := Parse(nil)
		c.Missing = true
		return c, nil
	}
	var names []string
	for _, p := range strings.Split(string(list), "\x00") {
		if base := path.Base(p); p != "" && isCorpusFile(base) {
			names = append(names, p)
		}
	}
	blobs, err := r.catFiles(rev, names)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for i, p := range names {
		files[path.Base(p)] = blobs[i]
	}
	return Parse(files), nil
}

func (r Repo) loadWorkingTree(dir string) (*Corpus, error) {
	entries, err := os.ReadDir(filepath.Join(r.Dir, dir))
	if errors.Is(err, os.ErrNotExist) {
		c := Parse(nil)
		c.Missing = true
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || !isCorpusFile(e.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.Dir, dir, e.Name())) //nolint:gosec // a file of the corpus directory the caller named
		if err != nil {
			return nil, err
		}
		files[e.Name()] = data
	}
	return Parse(files), nil
}

func isCorpusFile(base string) bool {
	return base == SuitesFile || path.Ext(base) == ".txtar"
}

// catFiles reads the blobs at rev:path for each of paths, in one git process.
func (r Repo) catFiles(rev string, paths []string) ([][]byte, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	var in bytes.Buffer
	for _, p := range paths {
		fmt.Fprintf(&in, "%s:%s\n", rev, p)
	}
	out, err := r.git(&in, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	rd := bufio.NewReader(bytes.NewReader(out))
	blobs := make([][]byte, 0, len(paths))
	for _, p := range paths {
		header, err := rd.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("git cat-file %s:%s: %w", rev, p, err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[1] != "blob" {
			return nil, fmt.Errorf("git cat-file %s:%s: %s", rev, p, strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("git cat-file %s:%s: %w", rev, p, err)
		}
		blob := make([]byte, size+1) // the content, then a newline
		if _, err := io.ReadFull(rd, blob); err != nil {
			return nil, fmt.Errorf("git cat-file %s:%s: %w", rev, p, err)
		}
		blobs = append(blobs, blob[:size])
	}
	return blobs, nil
}

func (r Repo) git(stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // git, with revisions and paths the caller named
	cmd.Dir = r.Dir
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
