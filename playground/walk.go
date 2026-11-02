package playground

import (
	"errors"
	"io/fs"
	"path"
	"strings"

	"cuelang.org/go/cue"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
)

// ExampleFile is the file that makes a lineage's directory an example, and
// describes it:
//
//	category: "Pets"            // required: the group the example is listed in
//	title:    "dog"             // optional: the lineage's name if absent
//	summary:  "A dog's record." // optional
//
// It is CUE, and sits beside the version files, which the authoring form's
// loader is the only reader of: it reads nothing but version files.
const ExampleFile = "example.cue"

// WalkExamples finds the examples in root, a directory of fsys, as a request
// for the register-examples operation, so that a host can offer a directory
// of lineages without writing their records by hand.
//
// Every directory beneath root, or root itself, that holds a version file, as
// its tip or under its history/, is a lineage in the authoring form, and is
// one example when it holds an [ExampleFile] too; nothing beneath it is
// another. A lineage without one is not an example, so a tree of lineages
// can hold some that are not offered; when none is an example, the error
// lists them. An example's ID is its directory's path relative to root, or
// root's name when root is the lineage. Whether it binds is found by binding
// it, and its data documents are its schemas' examples.
//
// Each problem found is a diagnostic in the error, and no examples are
// returned with one.
func WalkExamples(fsys fs.FS, root string) (RegisterExamplesRequest, error) {
	req := RegisterExamplesRequest{Examples: []HostExample{}}
	info, err := fs.Stat(fsys, root)
	switch {
	case err != nil:
		b := diag.New(diag.E0601).Headlinef("%s does not exist", root)
		if !errors.Is(err, fs.ErrNotExist) {
			b.Headlinef("%s cannot be read", root).Detail(diag.OSReason(err))
		}
		return req, b.Help("give the directory that holds the examples' lineages").Err()
	case !info.IsDir():
		return req, diag.New(diag.E0622).Headlinef("%s is not a directory", root).
			Help("give the directory that holds the examples' lineages").Err()
	}
	s, err := New()
	if err != nil {
		return req, err
	}

	var errs cerrors.Error
	var bare []string
	walkErr := fs.WalkDir(fsys, root, func(dir string, d fs.DirEntry, err error) error {
		if err != nil {
			errs = cerrors.Append(errs, cerrors.Promote(diag.New(diag.E0601).Headlinef("%s cannot be read", dir).Detail(diag.OSReason(err)).Err(), ""))
			return nil
		}
		if !d.IsDir() || !holdsVersionFile(fsys, dir) {
			return nil
		}
		h, found, err := s.walkExample(fsys, root, dir)
		switch {
		case err != nil:
			errs = cerrors.Append(errs, cerrors.Promote(err, ""))
		case found:
			req.Examples = append(req.Examples, h)
		default:
			bare = append(bare, dir)
		}
		return fs.SkipDir
	})
	if walkErr != nil {
		return req, diag.InternalErr(walkErr)
	}
	if errs != nil {
		return RegisterExamplesRequest{Examples: []HostExample{}}, errs
	}
	if len(req.Examples) == 0 {
		b := diag.New(diag.E0622).Headlinef("%s holds no example", root)
		for _, dir := range bare {
			b.Notef(token.NoPos, "%s holds a lineage but no %s", dir, ExampleFile)
		}
		return req, b.Helpf("an example is a lineage's directory of version files with an %s beside them", ExampleFile).Err()
	}
	return req, nil
}

// holdsVersionFile says whether dir holds a version file of its own: its tip,
// at its top, or a frozen version under its history/.
func holdsVersionFile(fsys fs.FS, dir string) bool {
	for _, d := range []string{dir, path.Join(dir, authoring.HistoryDir)} {
		ents, err := fs.ReadDir(fsys, d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), authoring.Ext) {
				return true
			}
		}
	}
	return false
}

// walkExample reads the example in the lineage directory dir, beneath root.
// found is false when dir has no [ExampleFile].
func (s *Session) walkExample(fsys fs.FS, root, dir string) (h HostExample, found bool, err error) {
	metaPath := path.Join(dir, ExampleFile)
	src, err := fs.ReadFile(fsys, metaPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return h, false, nil
		}
		return h, false, diag.New(diag.E0601).Headlinef("%s cannot be read", metaPath).Detail(diag.OSReason(err)).Err()
	}
	meta, err := s.readExampleFile(metaPath, src)
	if err != nil {
		return h, false, err
	}
	name, files, err := authoring.Load(fsys, dir)
	if err != nil {
		return h, false, err
	}
	in := LineageInput{Name: name}
	for _, f := range files {
		in.Files = append(in.Files, File{Path: f.Path, Text: string(f.Text)})
	}
	id := strings.TrimPrefix(dir, root+"/")
	if dir == root {
		id = path.Base(root)
	}
	if meta.title == "" {
		meta.title = name
	}
	h = HostExample{
		Example: ExampleInfo{ID: id, Title: meta.title, Category: meta.category, Summary: meta.summary, Kind: kindAuthoring},
		Lineage: in,
		Data:    []DataInput{},
	}
	if b := s.lineage(in); b.err == nil {
		h.Example.Binds = true
		h.Data = schemaExamples(b.lin)
	}
	return h, true, nil
}

// exampleMeta is what an [ExampleFile] says.
type exampleMeta struct {
	category, title, summary string
}

// readExampleFile reads the [ExampleFile] at name.
func (s *Session) readExampleFile(name string, src []byte) (exampleMeta, error) {
	var m exampleMeta
	f, err := parser.ParseFile(name, src, parser.ParseComments)
	if err != nil {
		return m, diag.Convert(err, diag.Context{Generic: diag.E0621})
	}
	v := s.rt.Context().BuildFile(f)
	if err = v.Err(); err != nil {
		return m, diag.Convert(err, diag.Context{Generic: diag.E0621})
	}
	fields := map[string]*string{"category": &m.category, "title": &m.title, "summary": &m.summary}
	var errs cerrors.Error
	it, err := v.Fields(cue.All())
	if err != nil {
		return m, diag.Convert(err, diag.Context{Generic: diag.E0621})
	}
	for it.Next() {
		label := it.Selector().String()
		dst, ok := fields[label]
		if !ok {
			errs = cerrors.Append(errs, cerrors.Promote(diag.New(diag.E0621).
				Headlinef("%s has a field %s, which is not category, title or summary", name, label).
				At(it.Value().Pos()).Help("remove the field").Err(), ""))
			continue
		}
		str, err := it.Value().String()
		if err != nil {
			errs = cerrors.Append(errs, cerrors.Promote(diag.New(diag.E0621).
				Headlinef("%s in %s is not a string", label, name).
				At(it.Value().Pos()).Helpf("write %s as a quoted string", label).Err(), ""))
			continue
		}
		*dst = str
	}
	if errs != nil {
		return m, errs
	}
	if m.category == "" {
		return m, diag.New(diag.E0621).Headlinef("%s has no category", name).
			Help(`add a category, such as category: "Pets": the group the playground lists the example in`).Err()
	}
	return m, nil
}
