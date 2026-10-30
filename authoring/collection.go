package authoring

import (
	"errors"
	"io/fs"
	"path"
	"strings"

	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/diag"
)

// A Member is one lineage of a collection, as [LoadCollection] reads it: its
// name, which is its directory's, and its files, as [Load] reads them. Err is
// set, and Files is nil, when its files cannot be read.
type Member struct {
	Name  string
	Files []File
	Err   error
}

// LoadCollection reads the lineages of the collection in root, a directory
// of fsys. A collection is a directory whose immediate subdirectories are
// lineages' directories, each named for its lineage:
//
//	<root>/<name>/tip.thema
//	<root>/<name>/history/<major>.<minor>.thema
//
// It is flat. A subdirectory of root is a lineage when it holds a version
// file, at its top or under its history/. Any other subdirectory, and
// everything beneath it, is not part of the collection, and neither is a
// file at the top of root, other than a version file: root itself holding
// one is a lineage's directory. Within a lineage's directory, [Load] reads
// only version files, so other files, such as the playground's example.cue,
// may sit beside them.
//
// The members are sorted by name, and each is read on its own: a lineage
// whose files cannot be read is a Member with Err set, and the others are
// read. The error is for root itself: a root that does not exist or cannot
// be read (E0601), is not a directory (E0623), or is a lineage's directory
// rather than a collection's (E0624).
//
// Links between the lineages of a collection are not part of it (#40): each
// lineage is self-contained, and is compiled and bound alone.
func LoadCollection(fsys fs.FS, root string) ([]Member, error) {
	info, err := fs.Stat(fsys, root)
	switch {
	case err != nil:
		b := diag.New(diag.E0601).Headlinef("%s does not exist", root)
		if !errors.Is(err, fs.ErrNotExist) {
			b.Headlinef("%s cannot be read", root).Detail(diag.OSReason(err))
		}
		return nil, b.Help("give the directory that holds the lineages' directories").Err()
	case !info.IsDir():
		return nil, diag.New(diag.E0623).Headlinef("%s is not a directory", root).
			Help("give the directory that holds the lineages' directories").Err()
	}
	if lin, _ := holdsVersionFile(fsys, root); lin {
		return nil, diag.New(diag.E0624).Headlinef("%s is a lineage's directory, not a collection's", root).
			Notef(token.NoPos, "a collection's lineages are the directories beneath it, each holding %s and %s/", TipFile, HistoryDir).
			Helpf("give the directory that holds %s, or load it as one lineage", root).Err()
	}
	ents, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, diag.New(diag.E0601).Headlinef("%s cannot be read", root).Detail(diag.OSReason(err)).Err()
	}
	var members []Member
	for _, e := range ents {
		dir := path.Join(root, e.Name())
		if !isDir(fsys, dir, e) {
			continue
		}
		lin, err := holdsVersionFile(fsys, dir)
		switch {
		case err != nil:
			members = append(members, Member{Name: e.Name(), Err: diag.New(diag.E0601).
				Headlinef("%s cannot be read", dir).Detail(diag.OSReason(err)).Err()})
		case lin:
			m := Member{Name: e.Name()}
			_, m.Files, m.Err = Load(fsys, dir)
			members = append(members, m)
		}
	}
	return members, nil
}

// isDir reports whether the entry e, at dir, is a directory, following a
// symbolic link.
func isDir(fsys fs.FS, dir string, e fs.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&fs.ModeSymlink == 0 {
		return false
	}
	info, err := fs.Stat(fsys, dir)
	return err == nil && info.IsDir()
}

// holdsVersionFile reports whether dir holds a version file of its own: its
// tip, at its top, or a frozen version under its history/. The error is set
// when dir, or its history/, exists but cannot be read, so that whether it is
// a lineage cannot be told.
func holdsVersionFile(fsys fs.FS, dir string) (bool, error) {
	for _, d := range []string{dir, path.Join(dir, HistoryDir)} {
		ents, err := fs.ReadDir(fsys, d)
		if err != nil {
			if d != dir && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return false, err
		}
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), Ext) {
				return true, nil
			}
		}
	}
	return false, nil
}
