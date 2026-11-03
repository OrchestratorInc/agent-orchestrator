package controllers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
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

// maxArtifactVersionPreviewBytes caps the content a version read returns. A
// longer version is cut short and marked truncated; a restore still writes it
// whole.
const maxArtifactVersionPreviewBytes = 256 << 10

// ArtifactVersionService is the version history of session artifact files.
type ArtifactVersionService interface {
	List(ctx context.Context, id domain.SessionID, relPath string) ([]domain.SessionArtifactVersion, error)
	ReadVersion(sess domain.SessionRecord, v domain.SessionArtifactVersion) ([]byte, error)
	RecordSave(ctx context.Context, sess domain.SessionRecord, relPath string) error
	RecordRestore(ctx context.Context, sess domain.SessionRecord, relPath string, from int64) error
	Sync(ctx context.Context, sess domain.SessionRecord, relPath string) error
	// Snapshot keeps the file's current content as a version and returns its
	// sha256, failing when it cannot be kept.
	Snapshot(ctx context.Context, sess domain.SessionRecord, relPath string) (string, error)
	// DeleteVersions deletes the kept versions of a file the user deleted.
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

// listedArtifactPath resolves the request's path to a file the session lists,
// writing the 404 itself when it is not one.
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

// listArtifactVersions returns the kept versions of one listed artifact file,
// newest first. It only reads; syncArtifactVersions records an edit first.
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

// syncArtifactVersions records an edit of one listed artifact file that the
// observer has not noticed yet, then returns the file's versions like
// listArtifactVersions. The history menu calls it when it opens. A failure only
// leaves the edit for the observer.
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
	out := ListArtifactVersionsResponse{Path: clean, Versions: make([]ArtifactVersionSummary, len(rows))}
	for i, v := range rows {
		out.Versions[i] = artifactVersionSummary(v)
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

// findArtifactVersion looks up one version of a file, writing the 404 itself.
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

// readArtifactVersionText reads a version's content, which must be UTF-8 text.
func (c *SessionsController) readArtifactVersionText(w http.ResponseWriter, r *http.Request, sess domain.Session, v domain.SessionArtifactVersion) ([]byte, bool) {
	data, err := c.ArtifactVersions.ReadVersion(sess.SessionRecord, v)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeArtifactVersionNotFound(w, r)
		return nil, false
	case err != nil:
		envelope.WriteError(w, r, err)
		return nil, false
	case !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0:
		envelope.WriteError(w, r, apierr.Invalid("ARTIFACT_FILE_BINARY", "Only UTF-8 text versions can be shown", nil))
		return nil, false
	}
	return data, true
}

// truncateText cuts valid UTF-8 text to at most limit bytes without splitting
// a character, reporting whether it cut anything.
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

// getArtifactVersion returns one version of a listed artifact file with its
// text content.
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

// restoreArtifactVersion rewrites a listed artifact file with an earlier
// version's content. The restore lands as the file's newest version, so the
// history is never rewritten and a restore can itself be undone.
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
	content, ok := c.readArtifactVersionText(w, r, sess, v)
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

// saveArtifactContent replaces a listed artifact file the way a save does: only
// while it still hashes to expected, the content the edit started from. Under
// the edit lock it first records any settled content the observer has not
// versioned yet, and afterwards records the save as a user version, so no other
// write lands between the save and its version.
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

// restoreArtifactContent rewrites a listed artifact file with the content of
// version from. Under the edit lock it keeps the file's current content as a
// version and writes only while the file still holds what was kept, so a
// restore never destroys the only copy of an agent edit: when the content
// cannot be kept, or the agent writes in between, nothing is written.
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

// recordArtifactSave notes a save as a user version, or a restore of version
// restoredFrom when that is above zero. The write already happened and its
// response must not depend on the client staying connected, so this runs on a
// context that outlives the request. A failure only delays the version: the
// observer records the content as an agent version on a later tick.
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
