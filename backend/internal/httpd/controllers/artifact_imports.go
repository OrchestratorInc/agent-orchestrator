package controllers

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/artifactversions"
	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

// artifactImportsPath copies files from outside a session into its artifact
// directory: a page someone shared with the user, and a newer version of it.
const artifactImportsPath = "/api/v1/sessions/{sessionId}/artifact-imports"

// decodeArtifactImport validates an import and decodes its files. A nil
// request is no import. Every path must be clean and distinct, the entry must
// be one of them, and the files are held to the attachment size caps.
func decodeArtifactImport(in *ArtifactImportRequest) ([]domain.ArtifactImportFile, string, error) {
	if in == nil {
		return nil, "", nil
	}
	if len(in.Files) == 0 || len(in.Files) > domain.MaxArtifactImportFiles {
		return nil, "", apierr.Invalid("ARTIFACT_IMPORT_INVALID", fmt.Sprintf("files must list 1 to %d files", domain.MaxArtifactImportFiles), nil)
	}
	entry, ok := sessionartifacts.CleanImportPath(strings.TrimSpace(in.Entry))
	if !ok {
		return nil, "", apierr.Invalid("ARTIFACT_IMPORT_INVALID", "entry is not a valid path", nil)
	}
	files := make([]domain.ArtifactImportFile, 0, len(in.Files))
	seen := make(map[string]bool, len(in.Files))
	total := 0
	for _, file := range in.Files {
		clean, ok := sessionartifacts.CleanImportPath(file.Path)
		if !ok || seen[strings.ToLower(clean)] {
			return nil, "", apierr.Invalid("ARTIFACT_IMPORT_INVALID", "a file path is invalid or repeated", map[string]any{"path": file.Path})
		}
		seen[strings.ToLower(clean)] = true
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(file.Data))
		if err != nil {
			return nil, "", apierr.Invalid("ARTIFACT_IMPORT_INVALID", "file data is not valid base64", map[string]any{"path": clean})
		}
		if len(data) > attachmentstore.MaxFileBytes {
			return nil, "", apierr.Invalid("ARTIFACT_IMPORT_TOO_LARGE", "a file is too large", map[string]any{"path": clean, "limitBytes": attachmentstore.MaxFileBytes})
		}
		total += len(data)
		if total > maxArtifactImportBytes {
			return nil, "", apierr.Invalid("ARTIFACT_IMPORT_TOO_LARGE", "the files are too large", map[string]any{"limitBytes": maxArtifactImportBytes})
		}
		files = append(files, domain.ArtifactImportFile{Path: clean, Data: data})
	}
	// A path that is also another's folder ("a" and "a/b") cannot be written.
	for _, file := range files {
		for dir := path.Dir(file.Path); dir != "."; dir = path.Dir(dir) {
			if seen[strings.ToLower(dir)] {
				return nil, "", apierr.Invalid("ARTIFACT_IMPORT_INVALID", "a file path is also a folder", map[string]any{"path": dir})
			}
		}
	}
	// A copy lives in its own folder under shared/, which is what gets marked as a copy.
	if parts := strings.Split(entry, "/"); len(parts) < 3 || parts[0] != "shared" {
		return nil, "", apierr.Invalid("ARTIFACT_IMPORT_INVALID", "entry must be in a folder under shared/", nil)
	}
	if !seen[strings.ToLower(entry)] {
		return nil, "", apierr.Invalid("ARTIFACT_IMPORT_INVALID", "entry must be one of the files", nil)
	}
	return files, entry, nil
}

// importArtifacts writes files into an existing session's artifact directory,
// keeping the current content of any file it replaces as a version first, and
// registers the entry as a deliverable.
func (c *SessionsController) importArtifacts(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, artifactImportsPath)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxArtifactImportBytes*4/3+(2<<20))
	var in ArtifactImportRequest
	if err := decodeJSON(r, &in); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			envelope.WriteError(w, r, apierr.Invalid("ARTIFACT_IMPORT_TOO_LARGE", "the files are too large", nil))
			return
		}
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	files, entry, err := decodeArtifactImport(&in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	// The agent may be writing these files; the guard below catches what slips past this.
	if sess.Status == domain.StatusWorking {
		envelope.WriteError(w, r, apierr.Conflict("SESSION_BUSY", "The session is working; try again when it is idle", nil))
		return
	}
	if err := c.writeArtifactImport(r.Context(), sess.SessionRecord, files); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	// Already marked when the session started from it; marked again in case it was not.
	if err := sessionartifacts.MarkSharedCopy(sess.Metadata.ArtifactDir, path.Dir(entry)); err != nil {
		slog.Default().Warn("mark shared artifact copy", "session", sess.ID, "path", entry, "err", err)
	}
	if _, err := c.Svc.RegisterArtifacts(r.Context(), sess.ID, "", []string{entry}); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ArtifactImportResponse{Entry: entry, Files: len(files)})
}

// writeArtifactImport writes files under the edit lock. Each file already
// there is kept as a version before it is replaced, so an import never
// destroys the only copy of an edit; one changing as it is read refuses the
// whole import before anything is written. WriteImport swaps the files in all
// or nothing, and only while each still holds what was kept, so an agent edit
// landing in between leaves the copy as it was instead of being lost.
func (c *SessionsController) writeArtifactImport(ctx context.Context, sess domain.SessionRecord, files []domain.ArtifactImportFile) error {
	artifactEditMu.Lock()
	defer artifactEditMu.Unlock()
	// An import the daemon stopped mid-swap is put right first, so the content
	// kept below is the file's own and the guard does not refuse it.
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.Path
	}
	sessionartifacts.RecoverImport(sess.Metadata.ArtifactDir, paths)
	if c.ArtifactVersions != nil {
		for i, file := range files {
			kept, err := c.ArtifactVersions.Snapshot(ctx, sess, file.Path)
			// Replaced only while it still holds what was just kept.
			files[i].Guard, files[i].Kept = true, kept
			switch {
			case errors.Is(err, fs.ErrNotExist):
				files[i].Kept = ""
			case err == nil:
			case errors.Is(err, artifactversions.ErrUnsettled):
				return apierr.Conflict("ARTIFACT_FILE_STALE", "A file is being written; try again in a moment", map[string]any{"path": file.Path})
			case errors.Is(err, artifactversions.ErrTooLarge):
				return apierr.Invalid("ARTIFACT_FILE_TOO_LARGE", "A current file is too large to keep a copy of, so it cannot be replaced", map[string]any{"path": file.Path, "limitBytes": artifactversions.MaxFileBytes})
			default:
				return fmt.Errorf("keep %s before importing: %w", file.Path, err)
			}
		}
	}
	if err := sessionartifacts.WriteImport(sess.Metadata.ArtifactDir, files); err != nil {
		if errors.Is(err, sessionartifacts.ErrImportIncomplete) {
			// Some files could not be put back: say which, and where their originals are.
			slog.Default().Warn("artifact import left incomplete", "session", sess.ID, "err", err)
			return apierr.Conflict("ARTIFACT_IMPORT_INCOMPLETE", "The update failed part way and could not be fully undone", map[string]any{"detail": err.Error()})
		}
		if errors.Is(err, sessionartifacts.ErrImportFileChanged) {
			return apierr.Conflict("ARTIFACT_FILE_CHANGED", "A file changed while it was being replaced; nothing was changed", nil)
		}
		if errors.Is(err, fs.ErrNotExist) {
			return apierr.NotFound("ARTIFACT_DIR_NOT_FOUND", "The session has no artifact directory")
		}
		return err
	}
	for _, file := range files {
		c.recordArtifactSave(ctx, sess, file.Path, 0)
	}
	return nil
}

// recordImportedVersions keeps each file a spawn imported as its first
// version, as it arrived. Spawn calls it once the files are written and
// before the agent starts, so the agent's first edit never takes its place.
func (c *SessionsController) recordImportedVersions(ctx context.Context, files []domain.ArtifactImportFile) func(domain.SessionRecord) {
	return func(sess domain.SessionRecord) {
		for _, file := range files {
			c.recordArtifactSave(ctx, sess, file.Path, 0)
		}
	}
}

// registerSpawnedImport registers a spawn's imported entry. The files are
// already written, so a failure only leaves the entry unlisted until the
// agent or the user registers it; the spawn still succeeds.
func (c *SessionsController) registerSpawnedImport(ctx context.Context, sess domain.SessionRecord, entry string) {
	if entry == "" {
		return
	}
	if _, err := c.Svc.RegisterArtifacts(ctx, sess.ID, "", []string{entry}); err != nil {
		slog.Default().Warn("register imported artifact", "session", sess.ID, "path", entry, "err", err)
	}
}
