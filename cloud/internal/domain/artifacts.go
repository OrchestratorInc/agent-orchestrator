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

// Limits on a session's artifact versions waiting in the bucket: uploads not
// yet committed and versions a commit replaced or deleted, until the sweeper
// removes them.
const (
	MaxPendingArtifactUploads = 2 * MaxSessionArtifactFiles
	MaxPendingArtifactBytes   = 2 * MaxSessionArtifactBytes
)

// ArtifactUploadTTL is how long an uncommitted artifact upload can still be
// committed before the sweeper deletes it.
const ArtifactUploadTTL = 24 * time.Hour

// ArtifactReplacedGrace is how long a version a commit replaced or deleted
// stays in the bucket, so a read that started before the commit can finish.
const ArtifactReplacedGrace = time.Hour

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

// reservedArtifactNames are file names Windows reserves for devices, with or
// without an extension.
var reservedArtifactNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// CleanArtifactPath validates a slash-separated path relative to the session's
// artifact directory and returns it in Unicode NFC form. It rejects control
// and invisible format characters, dot segments, Windows device names, and
// any path under the versions directory in any letter case.
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
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", ErrInvalidArtifactPath
		}
	}
	segments := strings.Split(cleaned, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", ErrInvalidArtifactPath
		}
		base, _, _ := strings.Cut(segment, ".")
		if reservedArtifactNames[strings.ToUpper(base)] {
			return "", ErrInvalidArtifactPath
		}
	}
	if strings.EqualFold(segments[0], ArtifactVersionsDir) {
		return "", ErrInvalidArtifactPath
	}
	return cleaned, nil
}

// SessionArtifactSummary is how many committed artifact files a session has
// and when the newest of them was committed.
type SessionArtifactSummary struct {
	Count     int
	UpdatedAt time.Time
}

// ArtifactObjectVersion is one object version in the artifact bucket.
type ArtifactObjectVersion struct {
	S3Key     string
	VersionID string
}
