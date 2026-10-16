package domain

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// ArtifactVersionsDir is the artifact history directory inside a session's
// artifact directory. Paths inside it are never stored.
const ArtifactVersionsDir = ".versions"

// ErrInvalidArtifactPath reports an artifact path that cannot be stored.
var ErrInvalidArtifactPath = errors.New("invalid artifact path")

// RequestedOutputArtifact asks a session to deliver a file in its artifact
// directory instead of a pull request.
const RequestedOutputArtifact = "artifact"

// ValidRequestedOutput reports whether value is empty or a known output kind.
func ValidRequestedOutput(value string) bool {
	return value == "" || value == RequestedOutputArtifact
}

// Artifact limits: one file, and every file of one session.
const (
	MaxArtifactFileBytes    = 25 << 20
	MaxSessionArtifactFiles = 200
	MaxSessionArtifactBytes = 500 << 20
)

// Session output types.
const (
	SessionOutputNone          = "none"
	SessionOutputPR            = "pr"
	SessionOutputArtifact      = "artifact"
	SessionOutputPRAndArtifact = "pr_artifact"
)

// DeriveSessionOutputType classifies a session's output from whether it has a
// pull request and whether it has artifact files.
func DeriveSessionOutputType(hasPR, hasArtifact bool) string {
	switch {
	case hasPR && hasArtifact:
		return SessionOutputPRAndArtifact
	case hasPR:
		return SessionOutputPR
	case hasArtifact:
		return SessionOutputArtifact
	default:
		return SessionOutputNone
	}
}

// SessionArtifact is one committed artifact file of a cloud session. Its bytes
// are the S3 object version VersionID at S3Key.
type SessionArtifact struct {
	Path        string
	Name        string
	Kind        string
	ContentType string
	Size        int64
	// SHA256 is the lowercase hex SHA-256 of the file.
	SHA256    string
	S3Key     string
	VersionID string
	MTime     time.Time
	UpdatedAt time.Time
}

// ArtifactManifestEntry is one file a worker reports in its artifact
// directory.
type ArtifactManifestEntry struct {
	Path   string
	Size   int64
	SHA256 string
}

// CleanArtifactPath validates a slash-separated path relative to the session's
// artifact directory and returns it in Unicode NFC form.
func CleanArtifactPath(raw string) (string, error) {
	if raw == "" || !utf8.ValidString(raw) {
		return "", ErrInvalidArtifactPath
	}
	cleaned := norm.NFC.String(raw)
	if strings.HasPrefix(cleaned, "/") || strings.Contains(cleaned, "\\") {
		return "", ErrInvalidArtifactPath
	}
	if len(cleaned) >= 2 && cleaned[1] == ':' {
		return "", ErrInvalidArtifactPath
	}
	for _, r := range cleaned {
		if unicode.IsControl(r) {
			return "", ErrInvalidArtifactPath
		}
	}
	segments := strings.Split(cleaned, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", ErrInvalidArtifactPath
		}
	}
	if segments[0] == ArtifactVersionsDir {
		return "", ErrInvalidArtifactPath
	}
	return cleaned, nil
}
