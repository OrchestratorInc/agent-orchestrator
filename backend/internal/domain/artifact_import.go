package domain

// ArtifactImportFile is one file copied into a session's artifact directory
// from outside the session, such as a page someone shared with the user and
// the files that page uses.
type ArtifactImportFile struct {
	// Path is slash-separated and relative to the artifact directory.
	Path string
	Data []byte
	// Guard has the file replaced only while it still holds Kept: the sha256
	// of the content kept as its latest version just before, or "" when there
	// was no file. A file the agent wrote in between is left alone.
	Guard bool
	Kept  string
}

// MaxArtifactImportFiles bounds one import: a shared page and the most files
// its link serves beside it (200).
const MaxArtifactImportFiles = 201
