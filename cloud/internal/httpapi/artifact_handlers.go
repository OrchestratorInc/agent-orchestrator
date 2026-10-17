package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/artifactstore"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/errgroup"
)

// sessionArtifactStore reads and writes the committed artifact rows of cloud
// sessions.
type sessionArtifactStore interface {
	ChangedSessionArtifacts(ctx context.Context, orgID, sessionID string, manifest []domain.ArtifactManifestEntry) ([]string, error)
	CommitSessionArtifacts(ctx context.Context, orgID, sessionID string, upserts []domain.SessionArtifact, deletes []string) error
	ListSessionArtifacts(ctx context.Context, principal domain.Principal, orgID, sessionID string) ([]domain.SessionArtifact, error)
	SessionArtifactsBySessions(ctx context.Context, orgID string, sessionIDs []string) (map[string][]domain.SessionArtifact, error)
	SessionArtifact(ctx context.Context, orgID, sessionID, path string) (domain.SessionArtifact, error)
}

// ArtifactSHA256Header carries the lowercase hex SHA-256 of an uploaded
// artifact blob.
const ArtifactSHA256Header = "X-AO-Artifact-SHA256"

// artifactAccessTTL is how long an artifact file access prefix stays valid.
const artifactAccessTTL = 30 * time.Minute

// artifactHeadConcurrency bounds the parallel HeadObject calls of one commit.
const artifactHeadConcurrency = 8

// maxArtifactVersionID bounds a client-supplied object version id.
const maxArtifactVersionID = 1024

// artifactFileCSP sandboxes every served artifact file: it runs in an opaque
// origin, can load only its own session's files and inline or data content,
// and cannot make network requests.
const artifactFileCSP = "sandbox allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads; " +
	"default-src 'self' data: blob:; " +
	"script-src 'self' 'unsafe-inline' 'unsafe-eval' data: blob:; " +
	"style-src 'self' 'unsafe-inline' data:; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self' data:; " +
	"media-src 'self' data: blob:; " +
	"connect-src 'none'; " +
	"form-action 'none'; " +
	"base-uri 'none'; " +
	"object-src 'none'"

// artifactsEnabled reports whether cloud artifacts are configured.
func (s *Server) artifactsEnabled() bool {
	return s.artifacts != nil && s.artifactRows != nil && len(s.artifactAccessKey) > 0
}

// artifactAccessKeyFrom derives the artifact access-token signing key from the
// provider secret key.
func artifactAccessKeyFrom(secret []byte) []byte {
	if len(secret) == 0 {
		return nil
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("ao-cloud artifact access v1"))
	return mac.Sum(nil)
}

type sessionArtifactResponse struct {
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	ContentType string    `json:"contentType"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func toSessionArtifactResponses(artifacts []domain.SessionArtifact) []sessionArtifactResponse {
	items := make([]sessionArtifactResponse, 0, len(artifacts))
	for _, artifact := range artifacts {
		items = append(items, sessionArtifactResponse{
			Path:        artifact.Path,
			Name:        artifact.Name,
			Kind:        artifact.Kind,
			ContentType: artifact.ContentType,
			Size:        artifact.Size,
			SHA256:      artifact.SHA256,
			UpdatedAt:   artifact.MTime.UTC(),
		})
	}
	return items
}

type workerArtifactManifestFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type workerArtifactManifestRequest struct {
	Files []workerArtifactManifestFile `json:"files"`
}

type workerArtifactManifestResponse struct {
	Changed []string `json:"changed"`
}

type workerArtifactBlobResponse struct {
	Path      string `json:"path"`
	VersionID string `json:"versionId"`
}

type workerArtifactCommitFile struct {
	Path      string    `json:"path"`
	VersionID string    `json:"versionId"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	MTime     time.Time `json:"mtime"`
}

type workerArtifactCommitRequest struct {
	Files   []workerArtifactCommitFile `json:"files"`
	Deleted []string                   `json:"deleted"`
}

type artifactAccessResponse struct {
	FilesPrefix string    `json:"filesPrefix"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// workerArtifactManifest returns which of the worker's artifact files differ
// from the committed rows of its own session.
func (s *Server) workerArtifactManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:connect") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:connect scope is required.")
		return
	}
	var input workerArtifactManifestRequest
	if err := decodeJSONTolerant(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
		return
	}
	if len(input.Files) > domain.MaxSessionArtifactFiles {
		writeArtifactLimit(w, r)
		return
	}
	manifest := make([]domain.ArtifactManifestEntry, 0, len(input.Files))
	seen := make(map[string]bool, len(input.Files))
	var total int64
	for _, file := range input.Files {
		cleaned, ok := s.validArtifactFile(w, r, file.Path, file.Size, file.SHA256)
		if !ok {
			return
		}
		if seen[cleaned] {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "A path is listed twice.")
			return
		}
		seen[cleaned] = true
		total += file.Size
		manifest = append(manifest, domain.ArtifactManifestEntry{Path: cleaned, Size: file.Size, SHA256: file.SHA256})
	}
	if total > domain.MaxSessionArtifactBytes {
		writeArtifactLimit(w, r)
		return
	}
	changed, err := s.artifactRows.ChangedSessionArtifacts(r.Context(), claims.OrgID, claims.SessionID, manifest)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, workerArtifactManifestResponse{Changed: changed})
}

// workerPutArtifactBlob streams one artifact file to the bucket under the
// worker's own session, with S3 verifying the claimed SHA-256.
func (s *Server) workerPutArtifactBlob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:connect") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:connect scope is required.")
		return
	}
	raw, err := wildcardPath(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_path", "The artifact path is invalid.")
		return
	}
	if r.ContentLength < 0 {
		writeError(w, r, http.StatusLengthRequired, "length_required", "Content-Length is required.")
		return
	}
	cleaned, ok := s.validArtifactFile(w, r, raw, r.ContentLength, r.Header.Get(ArtifactSHA256Header))
	if !ok {
		return
	}
	sum, _ := hex.DecodeString(r.Header.Get(ArtifactSHA256Header))
	key, err := artifactstore.Key(claims.OrgID, claims.SessionID, cleaned)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_path", "The artifact path is invalid.")
		return
	}
	body := http.MaxBytesReader(w, r.Body, r.ContentLength)
	versionID, err := s.artifacts.Put(r.Context(), key, body, r.ContentLength, sum)
	if errors.Is(err, artifactstore.ErrChecksumMismatch) {
		writeError(w, r, http.StatusUnprocessableEntity, "checksum_mismatch", "The body does not match its SHA-256.")
		return
	}
	if err != nil {
		s.logger.ErrorContext(r.Context(), "store artifact blob", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "artifact_store_unavailable", "The artifact could not be stored.")
		return
	}
	writeJSON(w, http.StatusOK, workerArtifactBlobResponse{Path: cleaned, VersionID: versionID})
}

// workerCommitArtifacts verifies each uploaded object version and then
// records the worker's artifact changes in one transaction.
func (s *Server) workerCommitArtifacts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:connect") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:connect scope is required.")
		return
	}
	var input workerArtifactCommitRequest
	if err := decodeJSONTolerant(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
		return
	}
	if len(input.Files) > domain.MaxSessionArtifactFiles || len(input.Deleted) > domain.MaxSessionArtifactFiles {
		writeArtifactLimit(w, r)
		return
	}
	seen := make(map[string]bool, len(input.Files)+len(input.Deleted))
	upserts := make([]domain.SessionArtifact, 0, len(input.Files))
	now := time.Now().UTC()
	for _, file := range input.Files {
		cleaned, ok := s.validArtifactFile(w, r, file.Path, file.Size, file.SHA256)
		if !ok {
			return
		}
		if file.VersionID == "" || len(file.VersionID) > maxArtifactVersionID {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "Each file needs a versionId.")
			return
		}
		if seen[cleaned] {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "A path is listed twice.")
			return
		}
		seen[cleaned] = true
		key, err := artifactstore.Key(claims.OrgID, claims.SessionID, cleaned)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_path", "The artifact path is invalid.")
			return
		}
		mtime := file.MTime.UTC()
		if mtime.IsZero() {
			mtime = now
		}
		upserts = append(upserts, domain.SessionArtifact{
			Path:        cleaned,
			Name:        path.Base(cleaned),
			Kind:        artifactstore.KindOf(cleaned),
			ContentType: artifactstore.ContentType(cleaned),
			Size:        file.Size,
			SHA256:      file.SHA256,
			S3Key:       key,
			VersionID:   file.VersionID,
			MTime:       mtime,
		})
	}
	deletes := make([]string, 0, len(input.Deleted))
	for _, raw := range input.Deleted {
		cleaned, err := artifactstore.CleanPath(raw)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_path", "An artifact path is invalid.")
			return
		}
		if seen[cleaned] {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "A path is listed twice.")
			return
		}
		seen[cleaned] = true
		deletes = append(deletes, cleaned)
	}
	if code, err := s.verifyArtifactVersions(r.Context(), upserts); err != nil {
		if code == "" {
			s.logger.ErrorContext(r.Context(), "verify artifact versions", "error", err, "request_id", requestID(r))
			writeError(w, r, http.StatusBadGateway, "artifact_store_unavailable", "The artifacts could not be verified.")
			return
		}
		writeError(w, r, http.StatusConflict, code, err.Error())
		return
	}
	err := s.artifactRows.CommitSessionArtifacts(r.Context(), claims.OrgID, claims.SessionID, upserts, deletes)
	if errors.Is(err, postgres.ErrArtifactLimit) {
		writeArtifactLimit(w, r)
		return
	}
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// verifyArtifactVersions checks that every upsert's object version exists with
// the claimed size and SHA-256. A non-empty code names a client error.
func (s *Server) verifyArtifactVersions(ctx context.Context, upserts []domain.SessionArtifact) (string, error) {
	type failure struct{ code, message string }
	failures := make([]failure, len(upserts))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(artifactHeadConcurrency)
	for index, artifact := range upserts {
		group.Go(func() error {
			head, err := s.artifacts.Head(groupCtx, artifact.S3Key, artifact.VersionID)
			if errors.Is(err, artifactstore.ErrNotFound) {
				failures[index] = failure{"artifact_version_missing", "No uploaded version " + artifact.VersionID + " exists for " + artifact.Path + "."}
				return nil
			}
			if err != nil {
				return err
			}
			if head.Size != artifact.Size || hex.EncodeToString(head.SHA256) != artifact.SHA256 {
				failures[index] = failure{"artifact_version_mismatch", "Version " + artifact.VersionID + " of " + artifact.Path + " does not match its size and SHA-256."}
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return "", err
	}
	for _, failed := range failures {
		if failed.code != "" {
			return failed.code, errors.New(failed.message)
		}
	}
	return "", nil
}

// validArtifactFile validates one reported artifact file and returns its
// cleaned path, writing a 4xx and returning false when it is invalid.
func (s *Server) validArtifactFile(w http.ResponseWriter, r *http.Request, rawPath string, size int64, sha string) (string, bool) {
	cleaned, err := artifactstore.CleanPath(rawPath)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_path", "An artifact path is invalid.")
		return "", false
	}
	if _, err := artifactstore.Key(workerFrom(r).OrgID, workerFrom(r).SessionID, cleaned); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_path", "An artifact path is too long.")
		return "", false
	}
	if size < 0 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "An artifact size is invalid.")
		return "", false
	}
	if size > domain.MaxArtifactFileBytes {
		writeArtifactLimit(w, r)
		return "", false
	}
	if !validSHA256Hex(sha) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "An artifact SHA-256 must be 64 lowercase hex characters.")
		return "", false
	}
	return cleaned, true
}

func validSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func writeArtifactLimit(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusRequestEntityTooLarge, "artifact_limit_exceeded",
		"Artifacts are limited to "+strconv.Itoa(domain.MaxArtifactFileBytes>>20)+" MiB per file, "+
			strconv.Itoa(domain.MaxSessionArtifactFiles)+" files and "+
			strconv.Itoa(domain.MaxSessionArtifactBytes>>20)+" MiB per session.")
}

// listSessionArtifacts returns a session's committed artifacts from Postgres.
// It never touches the session's sandbox.
func (s *Server) listSessionArtifacts(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	sessionID := chi.URLParam(r, "sessionId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and sessionId must be UUIDs.")
		return
	}
	artifacts, err := s.artifactRows.ListSessionArtifacts(r.Context(), principalFrom(r), orgID, sessionID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": toSessionArtifactResponses(artifacts)})
}

// createArtifactAccess returns a short-lived path prefix that serves the
// session's artifact files without a bearer header.
func (s *Server) createArtifactAccess(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	sessionID := chi.URLParam(r, "sessionId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and sessionId must be UUIDs.")
		return
	}
	if _, err := s.artifactRows.ListSessionArtifacts(r.Context(), principalFrom(r), orgID, sessionID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	expiresAt := time.Now().UTC().Add(artifactAccessTTL).Truncate(time.Second)
	token := s.signArtifactAccess(artifactAccessClaims{OrgID: orgID, SessionID: sessionID, Expires: expiresAt.Unix()})
	writeJSON(w, http.StatusOK, artifactAccessResponse{
		FilesPrefix: "/api/cloud/v1/artifact-files/" + token + "/",
		ExpiresAt:   expiresAt,
	})
}

// serveArtifactFile streams the committed version of one artifact file named
// by an access token and a path.
func (s *Server) serveArtifactFile(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.verifyArtifactAccess(chi.URLParam(r, "token"), time.Now())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "artifact_access_invalid", "The artifact link is invalid or has expired.")
		return
	}
	raw, err := wildcardPath(r)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "The requested resource was not found.")
		return
	}
	cleaned, err := artifactstore.CleanPath(raw)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "The requested resource was not found.")
		return
	}
	artifact, err := s.artifactRows.SessionArtifact(r.Context(), claims.OrgID, claims.SessionID, cleaned)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	etag := `"` + artifact.SHA256 + `"`
	w.Header().Set("Content-Security-Policy", artifactFileCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	object, err := s.artifacts.Get(r.Context(), artifact.S3Key, artifact.VersionID)
	if errors.Is(err, artifactstore.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "artifact_unavailable", "This artifact file is no longer available.")
		return
	}
	if err != nil {
		s.logger.ErrorContext(r.Context(), "read artifact blob", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "artifact_store_unavailable", "The artifact could not be read.")
		return
	}
	defer object.Body.Close()
	w.Header().Set("Content-Type", artifactstore.ContentType(artifact.Path))
	w.Header().Set("Content-Length", strconv.FormatInt(object.Size, 10))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, object.Body); err != nil {
		s.logger.WarnContext(r.Context(), "stream artifact blob", "error", err, "request_id", requestID(r))
	}
}

// wildcardPath returns the decoded remainder of a route ending in "/*".
func wildcardPath(r *http.Request) (string, error) {
	raw := chi.URLParam(r, "*")
	if r.URL.RawPath == "" {
		return raw, nil
	}
	return url.PathUnescape(raw)
}

type artifactAccessClaims struct {
	OrgID     string `json:"o"`
	SessionID string `json:"s"`
	Expires   int64  `json:"e"`
}

func (s *Server) signArtifactAccess(claims artifactAccessClaims) string {
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(s.artifactAccessMAC(encoded))
}

func (s *Server) verifyArtifactAccess(token string, now time.Time) (artifactAccessClaims, bool) {
	encoded, signature, ok := strings.Cut(token, ".")
	if !ok {
		return artifactAccessClaims{}, false
	}
	given, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !hmac.Equal(given, s.artifactAccessMAC(encoded)) {
		return artifactAccessClaims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return artifactAccessClaims{}, false
	}
	var claims artifactAccessClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return artifactAccessClaims{}, false
	}
	if requireUUID(claims.OrgID, "o") != nil || requireUUID(claims.SessionID, "s") != nil ||
		now.Unix() >= claims.Expires {
		return artifactAccessClaims{}, false
	}
	return claims, true
}

func (s *Server) artifactAccessMAC(encoded string) []byte {
	mac := hmac.New(sha256.New, s.artifactAccessKey)
	mac.Write([]byte(encoded))
	return mac.Sum(nil)
}

// sessionArtifactFiles returns the committed artifacts of each session, or nil
// when artifacts are off.
func (s *Server) sessionArtifactFiles(ctx context.Context, orgID string, sessionIDs []string) (map[string][]domain.SessionArtifact, error) {
	if !s.artifactsEnabled() {
		return nil, nil
	}
	return s.artifactRows.SessionArtifactsBySessions(ctx, orgID, sessionIDs)
}
