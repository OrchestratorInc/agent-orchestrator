package controllers

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
	"github.com/aoagents/agent-orchestrator/backend/internal/renderpage"
)

const (
	artifactFilePath  = "/api/v1/sessions/{sessionId}/artifact-files/*"
	artifactFilesPath = "/api/v1/sessions/{sessionId}/artifact-files"
)

// artifactFile serves a file from the session's artifact directory in the
// render sandbox, so the chat thread can frame an HTML artifact the way it
// frames a render. A page gets the theme bootstrap; the files next to it
// (images, CSS) load through this same route. Only regular files inside the
// directory are served: os.Root refuses ".." and symlinks that leave it.
func (c *SessionsController) artifactFile(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, artifactFilePath)
		return
	}
	// The session read fills in the default artifact directory for a row
	// stored without one.
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	asset := chi.URLParam(r, "*")
	// chi matches the escaped path whenever the request carried one, as a
	// page's link to "chart (1).png" does.
	if r.URL.RawPath != "" {
		if asset, err = url.PathUnescape(asset); err != nil {
			writeArtifactFileNotFound(w, r)
			return
		}
	}
	serveArtifactFile(w, r, renderContentSecurityPolicy, sess.Metadata.ArtifactDir, asset)
}

// serveArtifactFile serves asset from the artifact directory root in the
// sandbox csp names: a page with the theme bootstrap, anything else as it is.
// Only regular files inside root are served: os.Root refuses ".." and
// symlinks that leave it.
func serveArtifactFile(w http.ResponseWriter, r *http.Request, csp, root, asset string) {
	file, info, clean, err := previewutil.OpenWorkspaceFile(root, asset)
	if err != nil {
		writeArtifactFileNotFound(w, r)
		return
	}
	if info.Size() > attachmentstore.MaxFileBytes {
		_ = file.Close()
		writeArtifactFileNotFound(w, r)
		return
	}
	// An artifact can change, so its tag is its size and modification time
	// (and, for a page, the bootstrap version), known before any read.
	ext := strings.ToLower(path.Ext(clean))
	isPage := ext == ".html" || ext == ".htm"
	tag := fmt.Sprintf("%d-%d", info.Size(), info.ModTime().UnixNano())
	etag := tag
	if isPage {
		tag = renderpage.Version + "-" + tag
		etag = pageTag(r, tag)
	}
	if notModified(w, r, csp, etag) {
		_ = file.Close()
		return
	}
	// Capped again in the read: the file can grow after the stat.
	data, err := io.ReadAll(io.LimitReader(file, attachmentstore.MaxFileBytes+1))
	_ = file.Close()
	if err != nil {
		envelope.WriteError(w, r, fmt.Errorf("read artifact file: %w", err))
		return
	}
	if len(data) > attachmentstore.MaxFileBytes {
		writeArtifactFileNotFound(w, r)
		return
	}
	if isPage {
		serveSandboxedPage(w, r, csp, data, tag)
		return
	}
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	serveSandboxed(w, r, csp, data, contentType, tag)
}

func writeArtifactFileNotFound(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "ARTIFACT_FILE_NOT_FOUND", "artifact file not found", nil)
}

// deleteArtifactFile removes one listed file from the session's artifact
// directory and returns the session with its refreshed artifact list.
func (c *SessionsController) deleteArtifactFile(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodDelete, artifactFilesPath)
		return
	}
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	clean, ok := previewutil.CleanWorkspacePath(r.URL.Query().Get("path"))
	if !ok || !listsArtifactFile(sess.ArtifactFiles, clean) {
		writeArtifactFileNotFound(w, r)
		return
	}
	if err := removeArtifactFile(sess.Metadata.ArtifactDir, clean); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeArtifactFileNotFound(w, r)
			return
		}
		envelope.WriteError(w, r, fmt.Errorf("delete artifact file: %w", err))
		return
	}
	updated, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: sessionView(r, updated)})
}

func listsArtifactFile(files []domain.SessionArtifactFile, relPath string) bool {
	for _, file := range files {
		if file.Path == relPath {
			return true
		}
	}
	return false
}

// removeArtifactFile removes a regular file beneath dir. os.Root refuses ".."
// and symlinks that leave dir; a symlink itself is never removed.
func removeArtifactFile(dir, relPath string) error {
	if dir == "" {
		return fs.ErrNotExist
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	name := filepath.FromSlash(relPath)
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fs.ErrNotExist
	}
	return root.Remove(name)
}
