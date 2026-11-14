package controllers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/artifactversions"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
)

const (
	artifactVersionsPath       = "/api/v1/sessions/{sessionId}/artifact-versions"
	artifactVersionPath        = "/api/v1/sessions/{sessionId}/artifact-versions/{version}"
	artifactVersionRestorePath = "/api/v1/sessions/{sessionId}/artifact-versions/restore"
	artifactVersionSyncPath    = "/api/v1/sessions/{sessionId}/artifact-versions/sync"
)

const artifactVersionQuery = "artifactVersion"

const maxArtifactVersionPreviewBytes = 256 << 10

type ArtifactVersionService interface {
	List(ctx context.Context, id domain.SessionID, relPath string) ([]domain.SessionArtifactVersion, error)
	ReadVersion(sess domain.SessionRecord, v domain.SessionArtifactVersion) ([]byte, error)
	RecordSave(ctx context.Context, sess domain.SessionRecord, relPath string) error
	RecordRestore(ctx context.Context, sess domain.SessionRecord, relPath string, from int64) error
	Sync(ctx context.Context, sess domain.SessionRecord, relPath string) error
	Snapshot(ctx context.Context, sess domain.SessionRecord, relPath string) (string, error)
	DeleteVersions(ctx context.Context, sess domain.SessionRecord, relPath string) error
}

func artifactVersionSummary(v domain.SessionArtifactVersion) ArtifactVersionSummary {
	return ArtifactVersionSummary{
		Version:         v.Version,
		Size:            v.Size,
		Source:          string(v.Source),
		Harness:         string(v.Harness),
		RestoredFrom:    v.RestoredFrom,
		FileFingerprint: v.SHA256,
		CreatedAt:       v.CreatedAt,
	}
}

func (c *SessionsController) listedArtifactPath(w http.ResponseWriter, r *http.Request, rawPath string) (domain.Session, string, bool) {
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return domain.Session{}, "", false
	}
	clean, ok := previewutil.CleanWorkspacePath(rawPath)
	if !ok || !listsArtifactFile(sess.ArtifactFiles, clean) {
		writeArtifactFileNotFound(w, r)
		return domain.Session{}, "", false
	}
	return sess, clean, true
}

func writeArtifactVersionNotFound(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "ARTIFACT_VERSION_NOT_FOUND", "artifact version not found", nil)
}

func (c *SessionsController) listArtifactVersions(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil || c.ArtifactVersions == nil {
		apispec.NotImplemented(w, r, http.MethodGet, artifactVersionsPath)
		return
	}
	sess, clean, ok := c.listedArtifactPath(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	c.writeArtifactVersionList(w, r, sess, clean)
}

func (c *SessionsController) syncArtifactVersions(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil || c.ArtifactVersions == nil {
		apispec.NotImplemented(w, r, http.MethodPost, artifactVersionSyncPath)
		return
	}
	var in SyncArtifactVersionsRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	sess, clean, ok := c.listedArtifactPath(w, r, in.Path)
	if !ok {
		return
	}
	if err := c.ArtifactVersions.Sync(r.Context(), sess.SessionRecord, clean); err != nil {
		slog.Default().Debug("artifact versions: sync failed", "session", sess.ID, "path", clean, "err", err)
	}
	c.writeArtifactVersionList(w, r, sess, clean)
}

func (c *SessionsController) writeArtifactVersionList(w http.ResponseWriter, r *http.Request, sess domain.Session, clean string) {
	rows, err := c.ArtifactVersions.List(r.Context(), sess.ID, clean)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := ListArtifactVersionsResponse{Path: clean, MediaVersions: true, Versions: make([]ArtifactVersionSummary, len(rows))}
	for i, v := range rows {
		out.Versions[i] = artifactVersionSummary(v)
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *SessionsController) findArtifactVersion(w http.ResponseWriter, r *http.Request, sess domain.Session, clean string, version int64) (domain.SessionArtifactVersion, bool) {
	rows, err := c.ArtifactVersions.List(r.Context(), sess.ID, clean)
	if err != nil {
		envelope.WriteError(w, r, err)
		return domain.SessionArtifactVersion{}, false
	}
	i := slices.IndexFunc(rows, func(v domain.SessionArtifactVersion) bool { return v.Version == version })
	if i < 0 {
		writeArtifactVersionNotFound(w, r)
		return domain.SessionArtifactVersion{}, false
	}
	return rows[i], true
}

func (c *SessionsController) readArtifactVersionContent(w http.ResponseWriter, r *http.Request, sess domain.Session, v domain.SessionArtifactVersion) ([]byte, bool) {
	data, err := c.ArtifactVersions.ReadVersion(sess.SessionRecord, v)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeArtifactVersionNotFound(w, r)
		return nil, false
	case err != nil:
		envelope.WriteError(w, r, err)
		return nil, false
	}
	return data, true
}

func (c *SessionsController) readArtifactVersionText(w http.ResponseWriter, r *http.Request, sess domain.Session, v domain.SessionArtifactVersion) ([]byte, bool) {
	data, ok := c.readArtifactVersionContent(w, r, sess, v)
	if !ok {
		return nil, false
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		envelope.WriteError(w, r, apierr.Invalid("ARTIFACT_FILE_BINARY", "Only UTF-8 text versions can be shown", nil))
		return nil, false
	}
	return data, true
}

func (c *SessionsController) serveArtifactVersionFile(w http.ResponseWriter, r *http.Request, sess domain.Session, assetPath, rawVersion string) {
	version, err := strconv.ParseInt(rawVersion, 10, 64)
	clean, listed := previewutil.CleanWorkspacePath(assetPath)
	if c.ArtifactVersions == nil || err != nil || version < 1 || !listed || !listsArtifactFile(sess.ArtifactFiles, clean) {
		writeArtifactVersionNotFound(w, r)
		return
	}
	v, ok := c.findArtifactVersion(w, r, sess, clean, version)
	if !ok {
		return
	}
	data, ok := c.readArtifactVersionContent(w, r, sess, v)
	if !ok {
		return
	}
	sandboxLegacyPreview(w, r)
	w.Header().Set("ETag", `"`+v.SHA256+`"`)
	w.Header().Set("Access-Control-Expose-Headers", "ETag")
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, path.Base(clean), time.Time{}, bytes.NewReader(data))
}

func truncateText(data []byte, limit int) ([]byte, bool) {
	if len(data) <= limit {
		return data, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(data[cut]) {
		cut--
	}
	return data[:cut], true
}

func (c *SessionsController) getArtifactVersion(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil || c.ArtifactVersions == nil {
		apispec.NotImplemented(w, r, http.MethodGet, artifactVersionPath)
		return
	}
	version, err := strconv.ParseInt(chi.URLParam(r, "version"), 10, 64)
	if err != nil || version < 1 {
		writeArtifactVersionNotFound(w, r)
		return
	}
	sess, clean, ok := c.listedArtifactPath(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	v, ok := c.findArtifactVersion(w, r, sess, clean, version)
	if !ok {
		return
	}
	content, ok := c.readArtifactVersionText(w, r, sess, v)
	if !ok {
		return
	}
	shown, truncated := truncateText(content, maxArtifactVersionPreviewBytes)
	envelope.WriteJSON(w, http.StatusOK, ArtifactVersionResponse{ArtifactVersionSummary: artifactVersionSummary(v), Path: clean, Content: string(shown), Truncated: truncated})
}

func (c *SessionsController) restoreArtifactVersion(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil || c.ArtifactVersions == nil {
		apispec.NotImplemented(w, r, http.MethodPost, artifactVersionRestorePath)
		return
	}
	var in RestoreArtifactVersionRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	sess, clean, ok := c.listedArtifactPath(w, r, in.Path)
	if !ok {
		return
	}
	v, ok := c.findArtifactVersion(w, r, sess, clean, in.Version)
	if !ok {
		return
	}
	content, ok := c.readArtifactVersionContent(w, r, sess, v)
	if !ok {
		return
	}
	if err := c.restoreArtifactContent(r.Context(), sess.SessionRecord, clean, content, in.Version); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeArtifactFileNotFound(w, r)
			return
		}
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ArtifactFileResponse{
		Path:            clean,
		Size:            int64(len(content)),
		FileFingerprint: artifactFingerprint(content),
	})
}

func (c *SessionsController) saveArtifactContent(ctx context.Context, sess domain.SessionRecord, relPath, expected string, content []byte) error {
	artifactEditMu.Lock()
	defer artifactEditMu.Unlock()
	if c.ArtifactVersions != nil {
		if err := c.ArtifactVersions.Sync(ctx, sess, relPath); err != nil {
			slog.Default().Debug("artifact versions: keeping the current content before a save failed", "session", sess.ID, "path", relPath, "err", err)
		}
	}
	if err := writeArtifactFile(sess.Metadata.ArtifactDir, relPath, expected, content); err != nil {
		return err
	}
	c.recordArtifactSave(ctx, sess, relPath, 0)
	return nil
}

func (c *SessionsController) restoreArtifactContent(ctx context.Context, sess domain.SessionRecord, relPath string, content []byte, from int64) error {
	artifactEditMu.Lock()
	defer artifactEditMu.Unlock()
	kept, err := c.ArtifactVersions.Snapshot(ctx, sess, relPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return err
	case errors.Is(err, artifactversions.ErrUnsettled):
		return apierr.Conflict("ARTIFACT_FILE_STALE", "The file is being written; try again in a moment", nil)
	case errors.Is(err, artifactversions.ErrTooLarge):
		return apierr.Invalid("ARTIFACT_FILE_TOO_LARGE", "The current file is too large to keep a copy of, so it cannot be replaced", map[string]any{"limitBytes": artifactversions.MaxFileBytes})
	case err != nil:
		return fmt.Errorf("keep the current content before restoring: %w", err)
	}
	if err := writeArtifactFile(sess.Metadata.ArtifactDir, relPath, kept, content); err != nil {
		return err
	}
	c.recordArtifactSave(ctx, sess, relPath, from)
	return nil
}

func (c *SessionsController) recordArtifactSave(ctx context.Context, sess domain.SessionRecord, relPath string, restoredFrom int64) {
	if c.ArtifactVersions == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	var err error
	if restoredFrom > 0 {
		err = c.ArtifactVersions.RecordRestore(ctx, sess, relPath, restoredFrom)
	} else {
		err = c.ArtifactVersions.RecordSave(ctx, sess, relPath)
	}
	if err != nil {
		slog.Default().Warn("artifact versions: record save failed", "session", sess.ID, "path", relPath, "err", err)
	}
}
