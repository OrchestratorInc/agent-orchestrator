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
	"slices"
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
	ArtifactUploadAllowed(ctx context.Context, orgID, sessionID string, size int64) (bool, error)
	RecordArtifactUpload(ctx context.Context, orgID, sessionID, s3Key, versionID string, size int64) error
	CommitSessionArtifacts(ctx context.Context, orgID, sessionID, workerID string, workerEpoch int64, upserts []domain.SessionArtifact, deletes []string) error
	ListSessionArtifacts(ctx context.Context, principal domain.Principal, orgID, sessionID string) ([]domain.SessionArtifact, error)
	RequireSessionArtifactAccess(ctx context.Context, principal domain.Principal, orgID, sessionID string) error
	SessionArtifactsBySessions(ctx context.Context, orgID string, sessionIDs []string) (map[string][]domain.SessionArtifact, error)
	SessionArtifactCounts(ctx context.Context, orgID string, sessionIDs []string) (map[string]domain.SessionArtifactSummary, error)
	SessionArtifact(ctx context.Context, userID, orgID, sessionID, path string) (domain.SessionArtifact, error)
	DeleteSessionArtifact(ctx context.Context, principal domain.Principal, orgID, sessionID, path string) (domain.ArtifactObjectVersion, error)
}

// artifactAccessTTL is how long an artifact file access prefix stays valid.
const artifactAccessTTL = 10 * time.Minute

// artifactUploadReadTimeout is how long one artifact blob upload may take to
// arrive, replacing the server's request read timeout for that route.
const artifactUploadReadTimeout = 5 * time.Minute

// artifactHeadConcurrency bounds the parallel HeadObject calls of one commit.
const artifactHeadConcurrency = 8

// maxArtifactVersionID bounds a client-supplied object version id.
const maxArtifactVersionID = 1024

// artifactFileCSP sandboxes every served artifact file: it runs in an opaque
// origin, can load only its own session's files and inline or data content,
// and cannot fetch, post forms, open sockets or open windows. A page can still
// navigate itself, so it can carry data, including its own URL and access
// token, to another site that way; the desktop holds its frame to the
// session's access prefix.
const artifactFileCSP = "sandbox allow-scripts allow-forms allow-downloads; " +
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
			UpdatedAt:   artifact.UpdatedAt.UTC(),
		})
	}
	return items
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
	var input worker.ArtifactManifestRequest
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
	writeJSON(w, http.StatusOK, worker.ArtifactManifestResponse{Changed: changed})
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
	cleaned, ok := s.validArtifactFile(w, r, raw, r.ContentLength, r.Header.Get(worker.ArtifactSHA256Header))
	if !ok {
		return
	}
	sum, _ := hex.DecodeString(r.Header.Get(worker.ArtifactSHA256Header))
	key, err := artifactstore.Key(claims.OrgID, claims.SessionID, cleaned)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_path", "The artifact path is invalid.")
		return
	}
	allowed, err := s.artifactRows.ArtifactUploadAllowed(r.Context(), claims.OrgID, claims.SessionID, r.ContentLength)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !allowed {
		writeArtifactLimit(w, r)
		return
	}
	if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(artifactUploadReadTimeout)); err != nil {
		s.logger.WarnContext(r.Context(), "extend artifact upload deadline", "error", err, "request_id", requestID(r))
	}
	body := http.MaxBytesReader(w, r.Body, r.ContentLength)
	versionID, ok := s.putArtifactObject(w, r, key, body, r.ContentLength, sum)
	if !ok {
		return
	}
	if err := s.artifactRows.RecordArtifactUpload(r.Context(), claims.OrgID, claims.SessionID, key, versionID, r.ContentLength); err != nil {
		// An unrecorded version would never be collected, so remove it now.
		if deleteErr := s.artifacts.DeleteVersion(context.WithoutCancel(r.Context()), key, versionID); deleteErr != nil {
			s.logger.ErrorContext(r.Context(), "delete unrecorded artifact version", "error", deleteErr, "request_id", requestID(r))
		}
		if errors.Is(err, postgres.ErrArtifactLimit) {
			writeArtifactLimit(w, r)
			return
		}
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, worker.ArtifactBlobResponse{Path: cleaned, VersionID: versionID})
}

// workerListArtifacts returns the committed artifact files of the worker's
// own session, so a new worker can restore them.
func (s *Server) workerListArtifacts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:connect") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:connect scope is required.")
		return
	}
	artifacts, err := s.artifactRows.SessionArtifactsBySessions(r.Context(), claims.OrgID, []string{claims.SessionID})
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	files := make([]worker.ArtifactManifestFile, 0, len(artifacts[claims.SessionID]))
	for _, artifact := range artifacts[claims.SessionID] {
		files = append(files, worker.ArtifactManifestFile{Path: artifact.Path, Size: artifact.Size, SHA256: artifact.SHA256})
	}
	writeJSON(w, http.StatusOK, worker.ArtifactListResponse{Files: files})
}

// workerGetArtifactBlob streams the committed version of one artifact file of
// the worker's own session, with its SHA-256 in ArtifactSHA256Header.
func (s *Server) workerGetArtifactBlob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:connect") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:connect scope is required.")
		return
	}
	raw, err := wildcardPath(r)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "The requested resource was not found.")
		return
	}
	cleaned, err := domain.CleanArtifactPath(raw)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "The requested resource was not found.")
		return
	}
	artifacts, err := s.artifactRows.SessionArtifactsBySessions(r.Context(), claims.OrgID, []string{claims.SessionID})
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	index := slices.IndexFunc(artifacts[claims.SessionID], func(artifact domain.SessionArtifact) bool { return artifact.Path == cleaned })
	if index < 0 {
		writeError(w, r, http.StatusNotFound, "not_found", "The requested resource was not found.")
		return
	}
	artifact := artifacts[claims.SessionID][index]
	object, err := s.artifacts.Get(r.Context(), artifact.S3Key, artifact.VersionID)
	if !s.artifactObjectReadable(w, r, err) {
		return
	}
	defer object.Body.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(object.Size, 10))
	w.Header().Set(worker.ArtifactSHA256Header, artifact.SHA256)
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, object.Body); err != nil {
		s.logger.WarnContext(r.Context(), "stream artifact blob to worker", "error", err, "request_id", requestID(r))
	}
}

// putArtifactObject streams body to key with S3 verifying sum and returns the
// new object version, writing an error and returning false when the bucket
// refuses it.
func (s *Server) putArtifactObject(w http.ResponseWriter, r *http.Request, key string, body io.Reader, size int64, sum []byte) (string, bool) {
	versionID, err := s.artifacts.Put(r.Context(), key, body, size, sum)
	if errors.Is(err, artifactstore.ErrChecksumMismatch) {
		writeError(w, r, http.StatusUnprocessableEntity, "checksum_mismatch", "The body does not match its SHA-256.")
		return "", false
	}
	if err != nil {
		s.logger.ErrorContext(r.Context(), "store artifact object", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "artifact_store_unavailable", "The artifact could not be stored.")
		return "", false
	}
	return versionID, true
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
	var input worker.ArtifactCommitRequest
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
		cleaned, err := domain.CleanArtifactPath(raw)
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
	err := s.artifactRows.CommitSessionArtifacts(r.Context(), claims.OrgID, claims.SessionID, claims.WorkerID, claims.Epoch, upserts, deletes)
	if errors.Is(err, postgres.ErrArtifactLimit) {
		writeArtifactLimit(w, r)
		return
	}
	if errors.Is(err, postgres.ErrStaleWorker) {
		writeError(w, r, http.StatusUnauthorized, "STALE_WORKER_TOKEN", "The worker credential has been replaced.")
		return
	}
	if errors.Is(err, postgres.ErrArtifactVersionUnknown) {
		writeError(w, r, http.StatusConflict, "artifact_version_missing", "A version is not an open upload of this session; upload it again.")
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
	cleaned, err := domain.CleanArtifactPath(rawPath)
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

// deleteSessionArtifact deletes one of a session's artifact files, named by
// the path query parameter: its row, its stored object version, and, through
// a request to a connected worker, the file on the worker's disk. The stored
// version is deleted now; if that fails, the sweeper deletes it on its next
// pass.
func (s *Server) deleteSessionArtifact(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	sessionID := chi.URLParam(r, "sessionId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and sessionId must be UUIDs.")
		return
	}
	cleaned, err := domain.CleanArtifactPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_path", "The artifact path is invalid.")
		return
	}
	version, err := s.artifactRows.DeleteSessionArtifact(r.Context(), principalFrom(r), orgID, sessionID, cleaned)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if err := s.artifacts.DeleteVersion(context.WithoutCancel(r.Context()), version.S3Key, version.VersionID); err != nil {
		s.logger.WarnContext(r.Context(), "delete artifact version; the sweeper retries", "error", err, "request_id", requestID(r))
	}
	w.WriteHeader(http.StatusNoContent)
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
	principal := principalFrom(r)
	if err := s.artifactRows.RequireSessionArtifactAccess(r.Context(), principal, orgID, sessionID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	expiresAt := time.Now().UTC().Add(artifactAccessTTL).Truncate(time.Second)
	token := s.signArtifactAccess(artifactAccessClaims{OrgID: orgID, SessionID: sessionID, UserID: principal.UserID, Expires: expiresAt.Unix()})
	writeJSON(w, http.StatusOK, artifactAccessResponse{
		FilesPrefix: "/api/cloud/v1/artifact-files/" + token + "/",
		ExpiresAt:   expiresAt,
	})
}

// serveArtifactFile streams the committed version of one artifact file named
// by an access token and a path, while the token's user is still an active
// member of the organization.
func (s *Server) serveArtifactFile(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.verifyArtifactAccess(chi.URLParam(r, "token"), time.Now())
	if !ok {
		// A page opening another file in its frame after its link expired
		// gets a readable page, not the JSON error.
		if dest := r.Header.Get("Sec-Fetch-Dest"); dest == "iframe" || dest == "document" {
			writeExpiredArtifactPage(w)
			return
		}
		writeError(w, r, http.StatusUnauthorized, "artifact_access_invalid", "The artifact link is invalid or has expired.")
		return
	}
	raw, err := wildcardPath(r)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "The requested resource was not found.")
		return
	}
	cleaned, err := domain.CleanArtifactPath(raw)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "The requested resource was not found.")
		return
	}
	artifact, err := s.artifactRows.SessionArtifact(r.Context(), claims.UserID, claims.OrgID, claims.SessionID, cleaned)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.writeArtifactObject(w, r, artifactObject{
		key:          artifact.S3Key,
		versionID:    artifact.VersionID,
		etag:         `"` + artifact.SHA256 + `"`,
		contentType:  artifactstore.ContentType(artifact.Path),
		cacheControl: "private, no-cache",
	})
}

// artifactObject names one stored object version and how to serve it.
type artifactObject struct {
	key          string
	versionID    string
	etag         string
	contentType  string
	cacheControl string
}

// writeArtifactObject serves one object version inside the artifact sandbox
// CSP. It answers If-None-Match from the object's ETag and HEAD from its
// metadata without reading its body.
func (s *Server) writeArtifactObject(w http.ResponseWriter, r *http.Request, object artifactObject) {
	etag := object.etag
	if r.Header.Get("If-None-Match") == etag {
		setArtifactFileHeaders(w, etag, object.cacheControl)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method == http.MethodHead {
		head, err := s.artifacts.Head(r.Context(), object.key, object.versionID)
		if !s.artifactObjectReadable(w, r, err) {
			return
		}
		setArtifactFileHeaders(w, etag, object.cacheControl)
		w.Header().Set("Content-Type", object.contentType)
		w.Header().Set("Content-Length", strconv.FormatInt(head.Size, 10))
		w.WriteHeader(http.StatusOK)
		return
	}
	stored, err := s.artifacts.Get(r.Context(), object.key, object.versionID)
	if !s.artifactObjectReadable(w, r, err) {
		return
	}
	defer stored.Body.Close()
	setArtifactFileHeaders(w, etag, object.cacheControl)
	w.Header().Set("Content-Type", object.contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(stored.Size, 10))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, stored.Body); err != nil {
		s.logger.WarnContext(r.Context(), "stream artifact object", "error", err, "request_id", requestID(r))
	}
}

// artifactObjectReadable writes the error response for a failed object read
// and returns false, or returns true when err is nil.
func (s *Server) artifactObjectReadable(w http.ResponseWriter, r *http.Request, err error) bool {
	if errors.Is(err, artifactstore.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "artifact_unavailable", "This artifact file is no longer available.")
		return false
	}
	if err != nil {
		s.logger.ErrorContext(r.Context(), "read artifact object", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "artifact_store_unavailable", "The artifact could not be read.")
		return false
	}
	return true
}

// expiredArtifactPage is shown in a frame that navigated with an expired or
// invalid artifact link.
const expiredArtifactPage = `<!doctype html><meta charset="utf-8"><title>Link expired</title>` +
	`<body style="font:14px system-ui,sans-serif;color:#555;margin:24px">` +
	`This artifact link has expired. Open the file again from the session's Artifacts tab.</body>`

func writeExpiredArtifactPage(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, expiredArtifactPage)
}

// setArtifactFileHeaders sets the headers of a served artifact file.
func setArtifactFileHeaders(w http.ResponseWriter, etag, cacheControl string) {
	w.Header().Set("Content-Security-Policy", artifactFileCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("ETag", etag)
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
	UserID    string `json:"u"`
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
		claims.UserID == "" || now.Unix() >= claims.Expires {
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

// sessionArtifactCounts returns the committed artifact count and newest commit
// time of each session, or nil when artifacts are off.
func (s *Server) sessionArtifactCounts(ctx context.Context, orgID string, sessionIDs []string) (map[string]domain.SessionArtifactSummary, error) {
	if !s.artifactsEnabled() {
		return nil, nil
	}
	return s.artifactRows.SessionArtifactCounts(ctx, orgID, sessionIDs)
}
