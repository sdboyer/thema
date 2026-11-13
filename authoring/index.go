package authoring

// IndexFile is the name of a lineage's version index, at the top of its
// directory beside history/. See [VersionIndex].
const IndexFile = "versions.json"

// Index is a lineage's version index, as [VersionIndex] writes it to
// IndexFile in JSON.
type Index struct {
	Lineage string `json:"lineage"`
	// Versions are the versions that were cut, oldest first.
	Versions []IndexedVersion `json:"versions"`
}

// IndexedVersion is one cut version in an [Index]. Version is for looking it
// up; its ID, whose halves are Schema and Lens, is what identifies it. Each
// is in its String form: ID as <schema>:<lens>, and each half as a
// [github.com/sdboyer/thema.Digest].
type IndexedVersion struct {
	Version string `json:"version"`
	ID      string `json:"id"`
	Schema  string `json:"schema"`
	Lens    string `json:"lens"`
}
