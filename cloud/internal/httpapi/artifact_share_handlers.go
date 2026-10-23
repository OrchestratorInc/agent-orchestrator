package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/artifactstore"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// artifactShareStore reads and writes the public share links users create
// for single files from local sessions.
type artifactShareStore interface {
	ArtifactSharePublishAllowed(ctx context.Context, userID, documentKey string, limit int) (bool, error)
	RecordArtifactShareUpload(ctx context.Context, userID, s3Key, versionID string, size int64) error
	DiscardArtifactShareUpload(ctx context.Context, userID, s3Key, versionID string) error
	PublishArtifactShare(ctx context.Context, share domain.ArtifactShare, baseSlug, documentKey string, limit int) (domain.ArtifactShare, bool, error)
	UpdateArtifactShare(ctx context.Context, share domain.ArtifactShare) (domain.ArtifactShare, error)
	GetArtifactShare(ctx context.Context, userID, shareID string) (domain.ArtifactShare, error)
	ListArtifactShares(ctx context.Context, userID string) ([]domain.ArtifactShare, error)
	RevokeArtifactShare(ctx context.Context, userID, shareID string) (domain.ArtifactShare, error)
	ArtifactShareBySlug(ctx context.Context, slug string) (domain.ArtifactShare, error)
}

// ArtifactShareOptions turns on artifact share links (AO_CLOUD_ARTIFACT_SHARES).
type ArtifactShareOptions struct {
	// Origin is the scheme, host and optional port every link is a subdomain
	// of, such as http://share.localhost:8080 or https://orchestrator.inc.
	Origin string
	// Limit is how many active links one user may hold.
	Limit int
}

// artifactShareConfig is the parsed form of ArtifactShareOptions.
type artifactShareConfig struct {
	scheme string
	domain string
	port   string
	limit  int
}

var shareSlugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

const maxArtifactShareName = 255

// shareCleanupTimeout bounds the cleanup a share request does after its
// client may have gone.
const shareCleanupTimeout = 10 * time.Second

func parseArtifactShareOptions(options *ArtifactShareOptions) (*artifactShareConfig, error) {
	if options == nil {
		return nil, nil
	}
	origin, err := url.Parse(options.Origin)
	if err != nil || origin.Hostname() == "" || (origin.Scheme != "http" && origin.Scheme != "https") ||
		(origin.Path != "" && origin.Path != "/") || origin.User != nil {
		return nil, errors.New("artifact share origin must be an absolute http or https origin")
	}
	limit := options.Limit
	if limit <= 0 {
		limit = domain.DefaultArtifactShareLimit
	}
	return &artifactShareConfig{
		scheme: origin.Scheme,
		domain: strings.ToLower(origin.Hostname()),
		port:   origin.Port(),
		limit:  limit,
	}, nil
}

// link returns the public URL of a share slug.
func (c *artifactShareConfig) link(slug string) string {
	host := slug + "." + c.domain
	if c.port != "" {
		host = net.JoinHostPort(host, c.port)
	}
	return c.scheme + "://" + host + "/"
}

// slugFromHost returns the share slug a request host names, if the host is a
// single label directly under the share domain.
func (c *artifactShareConfig) slugFromHost(host string) (string, bool) {
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	slug, ok := strings.CutSuffix(host, "."+c.domain)
	if !ok || slug == "" || strings.Contains(slug, ".") {
		return "", false
	}
	return slug, true
}

func (s *Server) artifactSharesEnabled() bool {
	return s.artifactsEnabled() && s.shareRows != nil && s.shareConfig != nil
}

type artifactShareResponse struct {
	ID          string     `json:"id"`
	Slug        string     `json:"slug"`
	URL         string     `json:"url"`
	Name        string     `json:"name"`
	ContentType string     `json:"contentType"`
	Size        int64      `json:"size"`
	SHA256      string     `json:"sha256"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
}

func (s *Server) toArtifactShareResponse(share domain.ArtifactShare) artifactShareResponse {
	var revokedAt *time.Time
	if share.RevokedAt != nil {
		at := share.RevokedAt.UTC()
		revokedAt = &at
	}
	return artifactShareResponse{
		ID:          share.ID,
		Slug:        share.Slug,
		URL:         s.shareConfig.link(share.Slug),
		Name:        share.Name,
		ContentType: share.ContentType,
		Size:        share.Size,
		SHA256:      share.SHA256,
		CreatedAt:   share.CreatedAt.UTC(),
		UpdatedAt:   share.UpdatedAt.UTC(),
		RevokedAt:   revokedAt,
	}
}

// listArtifactShares returns the signed-in user's links and their limit.
// Revoked links are listed with revokedAt because they still use a slot.
func (s *Server) listArtifactShares(w http.ResponseWriter, r *http.Request) {
	shares, err := s.shareRows.ListArtifactShares(r.Context(), principalFrom(r).UserID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	items := make([]artifactShareResponse, 0, len(shares))
	for _, share := range shares {
		items = append(items, s.toArtifactShareResponse(share))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"shares": items,
		"limit":  s.shareConfig.limit,
		"used":   len(items),
	})
}

// createArtifactShare publishes the request body as the share link of a
// document named by the query's project, feature and name. A new document
// gets a new link named project-feature-email; publishing a document that
// already has a link replaces the version that link serves, so the earlier
// version is no longer published and no new slot is used.
func (s *Server) createArtifactShare(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal := principalFrom(r)
	query := r.URL.Query()
	upload, ok := s.readShareUpload(w, r)
	if !ok {
		return
	}
	baseSlug := domain.ArtifactShareSlug(query.Get("project"), query.Get("feature"), principal.Email)
	documentKey := domain.ArtifactShareDocumentKey(query.Get("project"), query.Get("feature"), upload.name)
	allowed, err := s.shareRows.ArtifactSharePublishAllowed(r.Context(), principal.UserID, documentKey, s.shareConfig.limit)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !allowed {
		s.writeShareLimit(w, r)
		return
	}
	shareID := uuid.NewString()
	key := "shares/" + shareID
	versionID, ok := s.putShareObject(w, r, principal.UserID, key, upload)
	if !ok {
		return
	}
	share, replaced, err := s.shareRows.PublishArtifactShare(r.Context(), domain.ArtifactShare{
		ID:          shareID,
		UserID:      principal.UserID,
		Name:        upload.name,
		ContentType: upload.contentType,
		Size:        upload.size,
		SHA256:      upload.sha256,
		S3Key:       key,
		VersionID:   versionID,
	}, baseSlug, documentKey, s.shareConfig.limit)
	if err != nil {
		s.discardShareUpload(r.Context(), principal.UserID, key, versionID)
		if errors.Is(err, postgres.ErrArtifactShareLimit) {
			s.writeShareLimit(w, r)
			return
		}
		s.writeStoreError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replaced {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"share": s.toArtifactShareResponse(share), "replaced": replaced})
}

// updateArtifactShare replaces the file an existing link serves. The link
// keeps its URL and does not count against the limit again; the sweeper
// deletes the replaced version after a grace period. The file name must match
// the link's, since it is part of the link's document: a renamed file is
// published as a new link.
func (s *Server) updateArtifactShare(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal := principalFrom(r)
	shareID := chi.URLParam(r, "shareId")
	if requireUUID(shareID, "shareId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "shareId must be a UUID.")
		return
	}
	upload, ok := s.readShareUpload(w, r)
	if !ok {
		return
	}
	current, err := s.shareRows.GetArtifactShare(r.Context(), principal.UserID, shareID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if upload.name != current.Name {
		writeShareNameChanged(w, r)
		return
	}
	versionID, ok := s.putShareObject(w, r, principal.UserID, current.S3Key, upload)
	if !ok {
		return
	}
	share, err := s.shareRows.UpdateArtifactShare(r.Context(), domain.ArtifactShare{
		ID:          shareID,
		UserID:      principal.UserID,
		Name:        upload.name,
		ContentType: upload.contentType,
		Size:        upload.size,
		SHA256:      upload.sha256,
		VersionID:   versionID,
	})
	if err != nil {
		s.discardShareUpload(r.Context(), principal.UserID, current.S3Key, versionID)
		if errors.Is(err, postgres.ErrArtifactShareNameChanged) {
			writeShareNameChanged(w, r)
			return
		}
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"share": s.toArtifactShareResponse(share)})
}

// revokeArtifactShare takes a link down. It still uses its slot under the
// limit, and publishing the same document again restores it. The sweeper
// deletes its stored file after a grace period.
func (s *Server) revokeArtifactShare(w http.ResponseWriter, r *http.Request) {
	shareID := chi.URLParam(r, "shareId")
	if requireUUID(shareID, "shareId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "shareId must be a UUID.")
		return
	}
	if _, err := s.shareRows.RevokeArtifactShare(r.Context(), principalFrom(r).UserID, shareID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type shareUpload struct {
	name        string
	contentType string
	size        int64
	sha256      string
	body        io.Reader
}

// readShareUpload validates the file name, length and SHA-256 of a share
// upload, writing a 4xx and returning false when they are invalid.
func (s *Server) readShareUpload(w http.ResponseWriter, r *http.Request) (shareUpload, bool) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" || len(name) > maxArtifactShareName || strings.ContainsAny(name, `/\`) ||
		name == "." || name == ".." || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "name must be a file name without a directory.")
		return shareUpload{}, false
	}
	if r.ContentLength < 0 {
		writeError(w, r, http.StatusLengthRequired, "length_required", "Content-Length is required.")
		return shareUpload{}, false
	}
	if r.ContentLength > domain.MaxArtifactShareBytes {
		writeError(w, r, http.StatusRequestEntityTooLarge, "artifact_share_too_large",
			"A shared artifact can be at most "+strconv.Itoa(domain.MaxArtifactShareBytes>>20)+" MiB.")
		return shareUpload{}, false
	}
	sha := r.Header.Get(worker.ArtifactSHA256Header)
	if !validSHA256Hex(sha) {
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			worker.ArtifactSHA256Header+" must be 64 lowercase hex characters.")
		return shareUpload{}, false
	}
	if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(artifactUploadReadTimeout)); err != nil {
		s.logger.WarnContext(r.Context(), "extend artifact share upload deadline", "error", err, "request_id", requestID(r))
	}
	// The body is read whole before it goes to the bucket, so a slow client
	// cannot hold the S3 request open; a share is at most
	// MaxArtifactShareBytes.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, r.ContentLength))
	if err != nil || int64(len(body)) != r.ContentLength {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The body ended before Content-Length.")
		return shareUpload{}, false
	}
	return shareUpload{
		name:        name,
		contentType: shareContentType(name),
		size:        r.ContentLength,
		sha256:      sha,
		body:        bytes.NewReader(body),
	}, true
}

// putShareObject stores an upload at key and records its version in the
// sweeper's queue, writing an error and returning false when either fails.
func (s *Server) putShareObject(w http.ResponseWriter, r *http.Request, userID, key string, upload shareUpload) (string, bool) {
	sum, _ := hex.DecodeString(upload.sha256)
	versionID, ok := s.putArtifactObject(w, r, key, upload.body, upload.size, sum)
	if !ok {
		return "", false
	}
	if err := s.shareRows.RecordArtifactShareUpload(r.Context(), userID, key, versionID, upload.size); err != nil {
		// An unrecorded version would never be collected, so remove it now.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), shareCleanupTimeout)
		defer cancel()
		if deleteErr := s.artifacts.DeleteVersion(ctx, key, versionID); deleteErr != nil {
			s.logger.ErrorContext(r.Context(), "delete unrecorded artifact share version", "error", deleteErr, "request_id", requestID(r))
		}
		s.writeStoreError(w, r, err)
		return "", false
	}
	return versionID, true
}

// discardShareUpload makes an upload no share claimed due for the sweeper
// now. When that fails, the sweeper still deletes it after ArtifactUploadTTL.
func (s *Server) discardShareUpload(ctx context.Context, userID, key, versionID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shareCleanupTimeout)
	defer cancel()
	if err := s.shareRows.DiscardArtifactShareUpload(ctx, userID, key, versionID); err != nil {
		s.logger.WarnContext(ctx, "discard artifact share upload", "error", err, "key", key)
	}
}

func writeShareNameChanged(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusConflict, "artifact_share_name_changed",
		"A share link keeps its file name. Publish the renamed file as a new link.")
}

func (s *Server) writeShareLimit(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusConflict, "artifact_share_limit_reached",
		"You can have up to "+strconv.Itoa(s.shareConfig.limit)+
			" shared artifact links. Upgrade to AO Cloud to share more.")
}

// shareContentType is the Content-Type a link serves for a file name.
// Markdown is served as plain text so browsers show it instead of
// downloading it.
func shareContentType(name string) string {
	if artifactstore.KindOf(name) == artifactstore.KindMarkdown {
		return "text/plain; charset=utf-8"
	}
	return artifactstore.ContentType(name)
}

// shareETag validates a share's bytes and its Content-Type together, because
// republishing can change the file name, and so the type, of identical bytes
// at the same URL.
func shareETag(share domain.ArtifactShare) string {
	sum := sha256.Sum256([]byte(share.ContentType))
	return `"` + share.SHA256 + "-" + hex.EncodeToString(sum[:4]) + `"`
}

// artifactShareHost serves requests whose host is a share link subdomain and
// passes every other request on.
func (s *Server) artifactShareHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug, ok := s.shareConfig.slugFromHost(r.Host)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		s.serveArtifactShare(w, r, slug)
	})
}

// serveArtifactShare streams the file a share link points at. A link serves
// one file, at its root path only.
func (s *Server) serveArtifactShare(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Only GET and HEAD are allowed.")
		return
	}
	if r.URL.Path != "/" || !shareSlugPattern.MatchString(slug) {
		writeError(w, r, http.StatusNotFound, "not_found", "The requested resource was not found.")
		return
	}
	share, err := s.shareRows.ArtifactShareBySlug(r.Context(), slug)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.writeArtifactObject(w, r, artifactObject{
		key:          share.S3Key,
		versionID:    share.VersionID,
		etag:         shareETag(share),
		contentType:  share.ContentType,
		cacheControl: "public, no-cache",
	})
}
