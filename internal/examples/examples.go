// Package examples collects the lineages the playground offers as examples:
// the exemplars, the lineage the docs walk through, a selection of the
// authoring compiler's round-trip fixtures, and a few lineages that fail to
// bind, each to show one diagnostic.
//
// The examples are drawn from fixtures that tests elsewhere own. The docs'
// lineage and the invalid lineages are copies (a package cannot embed files
// outside its directory), kept identical to their originals by this package's
// test.
package examples

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema/exemplars"
	"github.com/sdboyer/thema/internal/compile"
)

//go:embed ship.cue invalid/*.txtar
var copies embed.FS

// Example is one lineage, in one of the two forms, with what is known about
// it.
type Example struct {
	ID       string
	Title    string
	Category string
	Summary  string
	// Authoring says the lineage is in the authoring form: Name and Files.
	// Otherwise it is one normal-form file, Normal, with the lineage at Path.
	Authoring bool
	Name      string
	Files     []File
	Normal    File
	Path      string
	// Binds says whether the lineage binds.
	Binds bool
}

// File is one named text.
type File struct {
	Path string
	Text string
}

// Categories.
const (
	CategoryExemplar  = "Exemplars"
	CategoryAuthoring = "Authoring form"
	CategoryErrors    = "Errors"
)

// categories lists the categories in the order the playground lists them.
var categories = []string{CategoryExemplar, CategoryAuthoring, CategoryErrors}

// The exemplars that bind only with thema.SkipBuggyChecks: the lens totality
// check correctly refuses one of their lenses (#87). The playground binds
// without it, so they are examples of that diagnostic.
var exemplarsFailing = map[string]bool{
	"disjunct": true,
}

// The round-trip fixtures offered: the ones that bind, and a few that show
// one of the compiler's diagnostics.
var roundtrips = []string{
	"ship", "rename", "nested", "orelse", "computed", "each", "lacunas", "minor", "when-optional",
	"err-required-unproduced", "err-match-not-total", "err-optional-to-required",
}

// All returns every example, ordered by category and then ID.
func All() []Example {
	var all []Example
	all = append(all, exemplarExamples()...)
	all = append(all, ship())
	all = append(all, roundtripExamples()...)
	all = append(all, invalidExamples()...)
	sort.SliceStable(all, func(i, j int) bool {
		ci, cj := slices.Index(categories, all[i].Category), slices.Index(categories, all[j].Category)
		if ci != cj {
			return ci < cj
		}
		return all[i].ID < all[j].ID
	})
	return all
}

// ByID returns the example with id.
func ByID(id string) (Example, bool) {
	for _, e := range All() {
		if e.ID == id {
			return e, true
		}
	}
	return Example{}, false
}

var descriptionRE = regexp.MustCompile(`(?m)^\s*description:\s*"((?:[^"\\]|\\.)*)"`)

func exemplarExamples() []Example {
	fsys := exemplars.CueFS()
	names, err := fs.Glob(fsys, "*.cue")
	if err != nil {
		panic(err)
	}
	var out []Example
	for _, file := range names {
		b, err := fs.ReadFile(fsys, file)
		if err != nil {
			panic(err)
		}
		m := descriptionRE.FindSubmatch(b)
		if m == nil {
			// constraint.cue declares the exemplars' shape, not a lineage.
			continue
		}
		name := strings.TrimSuffix(file, ".cue")
		// The exemplars package names each lineage after its field, by a
		// pattern in constraint.cue; one file on its own names itself.
		text := string(b) + fmt.Sprintf("\n// In the exemplars package, constraint.cue names each lineage after its field.\n%s: l: name: %q\n", name, name)
		e := Example{
			ID:       "exemplar/" + name,
			Title:    name,
			Category: CategoryExemplar,
			Summary:  string(m[1]),
			Normal:   File{Path: file, Text: text},
			Path:     name + ".l",
			Binds:    !exemplarsFailing[name],
		}
		if !e.Binds {
			e.Category = CategoryErrors
		}
		out = append(out, e)
	}
	return out
}

func ship() Example {
	b, err := copies.ReadFile("ship.cue")
	if err != nil {
		panic(err)
	}
	return Example{
		ID:       "docs/ship",
		Title:    "ship",
		Category: CategoryExemplar,
		Summary:  "The lineage the docs walk through: a major version adding a required field, with lenses both ways.",
		Normal:   File{Path: "ship.cue", Text: string(b)},
		Path:     "lin",
		Binds:    true,
	}
}

func roundtripExamples() []Example {
	fsys := compile.RoundtripFixtures()
	var out []Example
	for _, name := range roundtrips {
		b, err := fs.ReadFile(fsys, name+".txtar")
		if err != nil {
			panic(fmt.Sprintf("round-trip fixture %s: %v", name, err))
		}
		ar := txtar.Parse(b)
		e := Example{
			ID:        "authoring/" + name,
			Title:     strings.TrimPrefix(name, "err-"),
			Category:  CategoryAuthoring,
			Summary:   summary(string(ar.Comment)),
			Authoring: true,
			Binds:     !strings.HasPrefix(name, "err-"),
		}
		if !e.Binds {
			e.Category = CategoryErrors
		}
		for _, f := range ar.Files {
			dir, rest, ok := strings.Cut(f.Name, "/")
			if !ok || strings.HasPrefix(f.Name, "out/") || path.Ext(rest) != compile.Ext {
				continue
			}
			e.Name = dir
			e.Files = append(e.Files, File{Path: rest, Text: string(f.Data)})
		}
		out = append(out, e)
	}
	return out
}

// invalidSummaries say what each invalid lineage shows; their fixtures have
// no prose of their own.
var invalidSummaries = map[string]string{
	"construct-cycle-mutual": "Two schema fields that refer to each other: a reference cycle no check can describe.",
	"empty-schemas":          "A lineage that declares no schemas.",
	"typo-lens-field":        "A misspelled field of a lens, and the field it was likely meant to be.",
	"typo-lineage-field":     "A misspelled field of a lineage.",
}

func invalidExamples() []Example {
	names, err := fs.Glob(copies, "invalid/*.txtar")
	if err != nil {
		panic(err)
	}
	var out []Example
	for _, file := range names {
		b, err := copies.ReadFile(file)
		if err != nil {
			panic(err)
		}
		ar := txtar.Parse(b)
		name := strings.TrimSuffix(path.Base(file), ".txtar")
		e := Example{
			ID:       "invalid/" + name,
			Title:    name,
			Category: CategoryErrors,
			Summary:  invalidSummaries[name],
		}
		for _, f := range ar.Files {
			if f.Name == "in.cue" {
				e.Normal = File{Path: "lineage.cue", Text: string(f.Data)}
			}
		}
		out = append(out, e)
	}
	return out
}

// summary is the first paragraph of a fixture's comment, on one line.
func summary(comment string) string {
	para, _, _ := strings.Cut(strings.TrimSpace(comment), "\n\n")
	var lines []string
	for _, l := range strings.Split(para, "\n") {
		if strings.HasPrefix(l, "#") {
			continue
		}
		lines = append(lines, strings.TrimSpace(l))
	}
	return strings.Join(lines, " ")
}
