package worker

import (
	"path/filepath"
	"time"
)

// CapabilityArtifacts, in a worker's capabilities, means it syncs the
// session's artifact directory when the launch context sets Artifacts.
const CapabilityArtifacts = "session.artifacts"

// ArtifactsDirEnv names the session's artifact directory in the coding
// agent's environment.
const ArtifactsDirEnv = "AO_SESSION_ARTIFACTS_DIR"

// ArtifactSHA256Header carries the lowercase hex SHA-256 of an uploaded
// artifact blob.
const ArtifactSHA256Header = "X-AO-Artifact-SHA256"

// ArtifactsDir is a session's artifact directory under the worker data
// directory, outside the git checkout.
func ArtifactsDir(dataDir, sessionID string) string {
	return filepath.Join(dataDir, "artifacts", sessionID)
}

// ArtifactManifestFile is one file in a worker's artifact manifest.
type ArtifactManifestFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ArtifactManifestRequest lists the worker's settled artifact files.
type ArtifactManifestRequest struct {
	Files []ArtifactManifestFile `json:"files"`
}

// ArtifactManifestResponse names the paths whose size or SHA-256 differ from
// the committed files.
type ArtifactManifestResponse struct {
	Changed []string `json:"changed"`
}

// ArtifactListResponse lists the committed artifact files of the worker's
// session.
type ArtifactListResponse struct {
	Files []ArtifactManifestFile `json:"files"`
}

// ArtifactBlobResponse is the stored version of one uploaded file.
type ArtifactBlobResponse struct {
	Path      string `json:"path"`
	VersionID string `json:"versionId"`
}

// ArtifactCommitFile is one uploaded file version to commit.
type ArtifactCommitFile struct {
	Path      string    `json:"path"`
	VersionID string    `json:"versionId"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	MTime     time.Time `json:"mtime"`
}

// ArtifactCommitRequest records uploaded versions and deleted paths.
type ArtifactCommitRequest struct {
	Files   []ArtifactCommitFile `json:"files"`
	Deleted []string             `json:"deleted"`
}
