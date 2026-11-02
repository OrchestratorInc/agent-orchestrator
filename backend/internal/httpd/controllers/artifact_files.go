package controllers

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	"sync"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
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
	asset, _, err := wildcardPath(r)
	if err != nil {
		writeArtifactFileNotFound(w, r)
		return
	}
	serveArtifactFile(w, r, renderContentSecurityPolicy, sess.Metadata.ArtifactDir, asset)
}

// wildcardPath returns the route's "*" tail unescaped. chi matches the escaped
// path whenever the request carried a non-default escape, as a page's link to
// "chart (1).png" or Turbopack's "%40swc" chunk names do; escaped is that
// tail as sent then, and empty otherwise.
func wildcardPath(r *http.Request) (tail, escaped string, err error) {
	tail = chi.URLParam(r, "*")
	if r.URL.RawPath == "" {
		return tail, "", nil
	}
	escaped = tail
	tail, err = url.PathUnescape(escaped)
	return tail, escaped, err
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
// directory along with its kept versions, and returns the session with its refreshed artifact list.
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
	if c.ArtifactVersions != nil {
		if err := c.ArtifactVersions.DeleteVersions(r.Context(), sess.SessionRecord, clean); err != nil {
			envelope.WriteError(w, r, fmt.Errorf("delete artifact versions: %w", err))
			return
		}
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

// artifactEditMu serialises artifact writes so two saves cannot both pass the
// fingerprint check against the same content. saveArtifactContent and
// restoreArtifactContent hold it.
var artifactEditMu sync.Mutex

// updateArtifactFile replaces the content of one listed artifact file when
// the file still holds the content the edit started from.
func (c *SessionsController) updateArtifactFile(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPut, artifactFilesPath)
		return
	}
	var in UpdateArtifactFileRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	expected := strings.TrimSpace(in.ExpectedFileFingerprint)
	if expected == "" {
		envelope.WriteError(w, r, apierr.Invalid("ARTIFACT_FILE_FINGERPRINT_REQUIRED", "expectedFileFingerprint is required", nil))
		return
	}
	if len(in.Content) > attachmentstore.MaxFileBytes {
		envelope.WriteError(w, r, apierr.Invalid("ARTIFACT_FILE_TOO_LARGE", "File is too large to edit", map[string]any{"limitBytes": attachmentstore.MaxFileBytes}))
		return
	}
	if !utf8.ValidString(in.Content) || strings.ContainsRune(in.Content, 0) {
		envelope.WriteError(w, r, apierr.Invalid("ARTIFACT_FILE_BINARY", "Only UTF-8 text files can be edited", nil))
		return
	}
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	clean, ok := previewutil.CleanWorkspacePath(in.Path)
	if !ok || !listsArtifactFile(sess.ArtifactFiles, clean) {
		writeArtifactFileNotFound(w, r)
		return
	}
	if err := c.saveArtifactContent(r.Context(), sess.SessionRecord, clean, expected, []byte(in.Content)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeArtifactFileNotFound(w, r)
			return
		}
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ArtifactFileResponse{
		Path:            clean,
		Size:            int64(len(in.Content)),
		FileFingerprint: artifactFingerprint([]byte(in.Content)),
	})
}

// artifactFingerprint is the hex SHA-256 of an artifact's content.
func artifactFingerprint(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// writeArtifactFile replaces a regular file beneath dir with content when its
// current content hashes to expected. The content goes to a temporary file
// beside it that is then renamed over it, so a failed save leaves the
// original intact. os.Root refuses ".." and symlinks that leave dir; a
// symlink itself is never written.
func writeArtifactFile(dir, relPath, expected string, content []byte) error {
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
	current, err := readArtifactFile(root, name)
	if err != nil {
		return fmt.Errorf("read artifact file: %w", err)
	}
	if artifactFingerprint(current) != expected {
		return apierr.Conflict("ARTIFACT_FILE_STALE", "File changed while it was being edited", map[string]any{"fileFingerprint": artifactFingerprint(current)})
	}
	if bytes.Equal(current, content) {
		return nil
	}
	temp, err := writeArtifactTemp(root, name, info.Mode().Perm(), content)
	if err != nil {
		return fmt.Errorf("write artifact file: %w", err)
	}
	if err := root.Rename(temp, name); err != nil {
		_ = root.Remove(temp)
		return fmt.Errorf("write artifact file: %w", err)
	}
	return nil
}

func readArtifactFile(root *os.Root, name string) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, attachmentstore.MaxFileBytes+1))
}

// writeArtifactTemp writes content to a new hidden file beside name, with
// name's permissions, and returns its root-relative path. The file is
// removed again if the write fails.
func writeArtifactTemp(root *os.Root, name string, perm fs.FileMode, content []byte) (string, error) {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	temp := filepath.Join(filepath.Dir(name), "."+filepath.Base(name)+".ao-save-"+hex.EncodeToString(suffix))
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return "", err
	}
	_, err = file.Write(content)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = root.Remove(temp)
		return "", err
	}
	return temp, nil
}
