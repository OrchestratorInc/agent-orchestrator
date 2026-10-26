package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxArtifactShareBytes caps the one file a shared artifact link serves.
	MaxArtifactShareBytes = 10 << 20
	// DefaultArtifactShareLimit is how many active share links a user may hold.
	DefaultArtifactShareLimit = 3
	// maxShareLabel is the DNS label length limit.
	maxShareLabel = 63
	// maxShareEmailPart caps the email part of a share slug.
	maxShareEmailPart = 40
	// shareSuffixReserve keeps room for a "-NN" collision suffix.
	shareSuffixReserve = 3
	// MaxArtifactShareSlugAttempts bounds the collision suffixes tried.
	MaxArtifactShareSlugAttempts = 50
)

// ArtifactShare is one public link to a single file a user published from a
// local session. The bytes live in the artifact bucket at S3Key; VersionID
// names the object version the link serves. A revoked link serves nothing but
// still uses one of the user's link slots.
type ArtifactShare struct {
	ID          string
	UserID      string
	Slug        string
	Name        string
	ContentType string
	Size        int64
	SHA256      string
	S3Key       string
	VersionID   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	RevokedAt   *time.Time
}

// ArtifactShareSlug builds the subdomain label project-feature-email for a
// share: lowercase, every run of characters outside a-z and 0-9 becomes one
// hyphen, and project and feature are shortened so the label, with room for a
// collision suffix, fits in 63 characters.
func ArtifactShareSlug(project, feature, email string) string {
	emailPart := truncateLabel(slugPart(email), maxShareEmailPart)
	projectPart := slugPart(project)
	featurePart := slugPart(feature)
	budget := maxShareLabel - shareSuffixReserve - len(emailPart)
	if emailPart != "" {
		budget--
	}
	if projectPart != "" && featurePart != "" {
		budget--
	}
	projectPart, featurePart = splitBudget(projectPart, featurePart, budget)
	parts := make([]string, 0, 3)
	for _, part := range []string{projectPart, featurePart, emailPart} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return "artifact"
	}
	return strings.Join(parts, "-")
}

// ArtifactShareDocumentKey identifies one of a user's published documents by
// its exact project, feature and file name. Publishing a document with the
// same key replaces the file its existing link serves. It hashes a
// length-prefixed encoding of the three values, so values that only the lossy
// URL slug would merge ("a-b"/"c" and "a"/"b-c", case, punctuation, or names
// past the slug's length budget) stay separate documents.
func ArtifactShareDocumentKey(project, feature, name string) string {
	hash := sha256.New()
	for _, part := range []string{project, feature, name} {
		hash.Write([]byte(strconv.Itoa(len(part))))
		hash.Write([]byte{':'})
		hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// ArtifactShareSlugCandidate returns the slug for one collision attempt:
// attempt 1 is the base slug, attempt n > 1 appends "-n".
func ArtifactShareSlugCandidate(base string, attempt int) string {
	if attempt <= 1 {
		return base
	}
	return base + "-" + strconv.Itoa(attempt)
}

func slugPart(value string) string {
	var builder strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			hyphen = false
			continue
		}
		if !hyphen && builder.Len() > 0 {
			builder.WriteByte('-')
			hyphen = true
		}
	}
	return strings.TrimRight(builder.String(), "-")
}

func truncateLabel(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) > limit {
		value = value[:limit]
	}
	return strings.TrimRight(value, "-")
}

// splitBudget shortens a and b to fit budget together, giving each half and
// handing any unused share to the other.
func splitBudget(a, b string, budget int) (string, string) {
	if len(a)+len(b) <= budget {
		return a, b
	}
	half := budget / 2
	switch {
	case len(a) <= half:
		return a, truncateLabel(b, budget-len(a))
	case len(b) <= budget-half:
		return truncateLabel(a, budget-len(b)), b
	}
	return truncateLabel(a, half), truncateLabel(b, budget-half)
}
