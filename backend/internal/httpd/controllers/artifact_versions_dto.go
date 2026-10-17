package controllers

import "time"

// ArtifactVersionQuery is the query string of the artifact version list and
// read routes.
type ArtifactVersionQuery struct {
	Path string `query:"path" description:"Artifact-directory-relative file path, as listed in the session's artifactFiles."`
}

// ArtifactVersionParam is the path parameter of one artifact version.
type ArtifactVersionParam struct {
	Version int64 `path:"version" description:"Version number within the file's history."`
}

// ArtifactVersionSummary describes one saved version of an artifact file.
type ArtifactVersionSummary struct {
	Version         int64     `json:"version"`
	Size            int64     `json:"size"`
	Source          string    `json:"source" enum:"agent,user" description:"Who wrote the version: the agent, or the user through a save or restore."`
	RestoredFrom    int64     `json:"restoredFrom,omitempty" description:"The version this one restored, when it is a restore."`
	Harness         string    `json:"harness,omitempty" description:"Best effort: the agent that was running the session when the version was recorded, which after an agent switch can differ from the agent that wrote it. Absent for older versions."`
	FileFingerprint string    `json:"fileFingerprint" description:"Hex SHA-256 of the version's content."`
	CreatedAt       time.Time `json:"createdAt"`
}

// ListArtifactVersionsResponse lists a file's kept versions, newest first. The
// first entry is the current version.
type ListArtifactVersionsResponse struct {
	Path     string                   `json:"path"`
	Versions []ArtifactVersionSummary `json:"versions"`
}

// ArtifactVersionResponse is one version with its text content.
type ArtifactVersionResponse struct {
	ArtifactVersionSummary
	Path      string `json:"path"`
	Content   string `json:"content" description:"The version's UTF-8 text content, cut short when truncated is set."`
	Truncated bool   `json:"truncated,omitempty" description:"The content is only the first 256 KiB of the version. A restore still writes the whole version."`
}

// SyncArtifactVersionsRequest names the artifact file whose unrecorded edit
// should be recorded before its versions are listed.
type SyncArtifactVersionsRequest struct {
	Path string `json:"path" description:"Artifact-directory-relative file path, as listed in the session's artifactFiles."`
}

// RestoreArtifactVersionRequest rewrites an artifact file with an earlier
// version's content. The content it replaces is kept as a version first, and
// the restore fails rather than overwrite content it could not keep.
type RestoreArtifactVersionRequest struct {
	Path    string `json:"path" description:"Artifact-directory-relative file path, as listed in the session's artifactFiles."`
	Version int64  `json:"version" description:"The version whose content becomes the file's content."`
}
