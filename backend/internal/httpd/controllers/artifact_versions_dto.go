package controllers

import "time"

type ArtifactVersionQuery struct {
	Path string `query:"path" description:"Artifact-directory-relative file path, as listed in the session's artifactFiles."`
}

type ArtifactVersionParam struct {
	Version int64 `path:"version" description:"Version number within the file's history."`
}

type ArtifactVersionSummary struct {
	Version         int64     `json:"version"`
	Size            int64     `json:"size"`
	Source          string    `json:"source" enum:"agent,user" description:"Who wrote the version: the agent, or the user through a save or restore."`
	RestoredFrom    int64     `json:"restoredFrom,omitempty" description:"The version this one restored, when it is a restore."`
	Harness         string    `json:"harness,omitempty" description:"Best effort: the agent that was running the session when the version was recorded, which after an agent switch can differ from the agent that wrote it. Absent for older versions."`
	FileFingerprint string    `json:"fileFingerprint" description:"Hex SHA-256 of the version's content."`
	CreatedAt       time.Time `json:"createdAt"`
}

type ListArtifactVersionsResponse struct {
	Path          string                   `json:"path"`
	MediaVersions bool                     `json:"mediaVersions,omitempty" description:"This daemon serves a version's bytes on the preview route and restores any file type. Absent from an older daemon."`
	Versions      []ArtifactVersionSummary `json:"versions"`
}

type ArtifactVersionResponse struct {
	ArtifactVersionSummary
	Path      string `json:"path"`
	Content   string `json:"content" description:"The version's UTF-8 text content, cut short when truncated is set."`
	Truncated bool   `json:"truncated,omitempty" description:"The content is only the first 256 KiB of the version. A restore still writes the whole version."`
}

type SyncArtifactVersionsRequest struct {
	Path string `json:"path" description:"Artifact-directory-relative file path, as listed in the session's artifactFiles."`
}

type RestoreArtifactVersionRequest struct {
	Path    string `json:"path" description:"Artifact-directory-relative file path, as listed in the session's artifactFiles."`
	Version int64  `json:"version" description:"The version whose content becomes the file's content."`
}
