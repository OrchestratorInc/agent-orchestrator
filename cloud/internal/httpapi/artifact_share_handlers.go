package httpapi

import (
	"context"
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
	CountArtifactShares(ctx context.Context, userID string) (int, error)
	CreateArtifactShare(ctx context.Context, share domain.ArtifactShare, baseSlug string, limit int) (domain.ArtifactShare, error)
	UpdateArtifactShare(ctx context.Context, share domain.ArtifactShare) (domain.ArtifactShare, string, error)
	GetArtifactShare(ctx context.Context, userID, shareID string) (domain.ArtifactShare, error)
	ListArtifactShares(ctx context.Context, userID string) ([]domain.ArtifactShare, error)
	DeleteArtifactShare(ctx context.Context, userID, shareID string) (domain.ArtifactShare, error)
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
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	URL         string    `json:"url"`
	Name        string    `json:"name"`
	ContentType string    `json:"contentType"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (s *Server) toArtifactShareResponse(share domain.ArtifactShare) artifactShareResponse {
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
	}
}

// listArtifactShares returns the signed-in user's links and their limit.
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

// createArtifactShare publishes the request body as a new share link named
// project-feature-email. The query carries name, project and feature.
func (s *Server) createArtifactShare(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal := principalFrom(r)
	query := r.URL.Query()
	upload, ok := s.readShareUpload(w, r)
	if !ok {
		return
	}
	used, err := s.shareRows.CountArtifactShares(r.Context(), principal.UserID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if used >= s.shareConfig.limit {
		s.writeShareLimit(w, r)
		return
	}
	shareID := uuid.NewString()
	key := "shares/" + shareID
	versionID, ok := s.putShareObject(w, r, key, upload)
	if !ok {
		return
	}
	share, err := s.shareRows.CreateArtifactShare(r.Context(), domain.ArtifactShare{
		ID:          shareID,
		UserID:      principal.UserID,
		Name:        upload.name,
		ContentType: upload.contentType,
		Size:        upload.size,
		SHA256:      upload.sha256,
		S3Key:       key,
		VersionID:   versionID,
	}, domain.ArtifactShareSlug(query.Get("project"), query.Get("feature"), principal.Email), s.shareConfig.limit)
	if err != nil {
		s.dropShareVersion(r.Context(), key, versionID)
		if errors.Is(err, postgres.ErrArtifactShareLimit) {
			s.writeShareLimit(w, r)
			return
		}
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"share": s.toArtifactShareResponse(share)})
}

// updateArtifactShare replaces the file an existing link serves. The link
// keeps its URL and does not count against the limit again.
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
	versionID, ok := s.putShareObject(w, r, current.S3Key, upload)
	if !ok {
		return
	}
	share, previousVersion, err := s.shareRows.UpdateArtifactShare(r.Context(), domain.ArtifactShare{
		ID:          shareID,
		UserID:      principal.UserID,
		Name:        upload.name,
		ContentType: upload.contentType,
		Size:        upload.size,
		SHA256:      upload.sha256,
		VersionID:   versionID,
	})
	if err != nil {
		s.dropShareVersion(r.Context(), current.S3Key, versionID)
		s.writeStoreError(w, r, err)
		return
	}
	if previousVersion != versionID {
		s.dropShareVersion(r.Context(), share.S3Key, previousVersion)
	}
	writeJSON(w, http.StatusOK, map[string]any{"share": s.toArtifactShareResponse(share)})
}

// deleteArtifactShare revokes a link and removes its stored file, freeing a
// slot under the limit.
func (s *Server) deleteArtifactShare(w http.ResponseWriter, r *http.Request) {
	shareID := chi.URLParam(r, "shareId")
	if requireUUID(shareID, "shareId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "shareId must be a UUID.")
		return
	}
	share, err := s.shareRows.DeleteArtifactShare(r.Context(), principalFrom(r).UserID, shareID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.dropShareVersion(r.Context(), share.S3Key, share.VersionID)
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
	return shareUpload{
		name:        name,
		contentType: shareContentType(name),
		size:        r.ContentLength,
		sha256:      sha,
		body:        http.MaxBytesReader(w, r.Body, r.ContentLength),
	}, true
}

// putShareObject stores an upload at key and returns its new version,
// writing an error and returning false when the bucket refuses it.
func (s *Server) putShareObject(w http.ResponseWriter, r *http.Request, key string, upload shareUpload) (string, bool) {
	sum, _ := hex.DecodeString(upload.sha256)
	versionID, err := s.artifacts.Put(r.Context(), key, upload.body, upload.size, sum)
	if errors.Is(err, artifactstore.ErrChecksumMismatch) {
		writeError(w, r, http.StatusUnprocessableEntity, "checksum_mismatch", "The body does not match its SHA-256.")
		return "", false
	}
	if err != nil {
		s.logger.ErrorContext(r.Context(), "store artifact share", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "artifact_store_unavailable", "The artifact could not be stored.")
		return "", false
	}
	return versionID, true
}

func (s *Server) dropShareVersion(ctx context.Context, key, versionID string) {
	if err := s.artifacts.DeleteVersion(context.WithoutCancel(ctx), key, versionID); err != nil {
		s.logger.WarnContext(ctx, "delete artifact share version", "error", err, "key", key)
	}
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
	etag := `"` + share.SHA256 + `"`
	w.Header().Set("Content-Security-Policy", artifactFileCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "public, no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	object, err := s.artifacts.Get(r.Context(), share.S3Key, share.VersionID)
	if errors.Is(err, artifactstore.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "artifact_unavailable", "This artifact is no longer available.")
		return
	}
	if err != nil {
		s.logger.ErrorContext(r.Context(), "read artifact share", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "artifact_store_unavailable", "The artifact could not be read.")
		return
	}
	defer object.Body.Close()
	w.Header().Set("Content-Type", share.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(object.Size, 10))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, object.Body); err != nil {
		s.logger.WarnContext(r.Context(), "stream artifact share", "error", err, "request_id", requestID(r))
	}
}
