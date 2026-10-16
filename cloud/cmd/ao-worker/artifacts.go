package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

const (
	// artifactSyncInterval is how often the syncer rescans while the agent
	// runs, matching the checkpoint safety net. A scan that finds no change
	// walks the directory and stats files; it makes no request.
	artifactSyncInterval = 30 * time.Second
	// artifactSettleDelay is how soon a scan that saw a file still changing
	// runs again, so a file written at the end of a turn appears in seconds.
	artifactSettleDelay = 2 * time.Second
	// artifactUploadTimeout bounds one blob upload.
	artifactUploadTimeout = 5 * time.Minute
	// artifactFinalFlushTimeout bounds the sync that runs at shutdown.
	artifactFinalFlushTimeout = 20 * time.Second
)

const (
	artifactManifestPath = "/worker/artifacts/manifest"
	artifactBlobPath     = "/worker/artifacts/blob/"
	artifactCommitPath   = "/worker/artifacts/commit"
)

// artifactState is one file as a scan saw it. sha is empty until the file
// has been hashed.
type artifactState struct {
	size  int64
	mtime time.Time
	sha   string
}

type uploadedArtifact struct {
	versionID string
	sha       string
}

// artifactSyncer copies a session's artifact directory to the control plane:
// it scans the directory, sends the settled files' manifest, uploads the
// changed files and commits them. A file is settled when two consecutive
// scans see the same size and modification time; every trigger, including the
// agent's Stop hook, applies the same rule. Failures are logged and retried on
// the next scan; the syncer never blocks the agent.
type artifactSyncer struct {
	client *client
	dir    string
	logger *slog.Logger
	upload *http.Client

	trigger chan struct{}

	// The fields below are used only by the run goroutine.
	previous map[string]artifactState
	// synced holds the files this worker process has confirmed with the
	// control plane. Only these are ever reported as deleted.
	synced   map[string]artifactState
	uploaded map[string]uploadedArtifact
	skipped  map[string]bool
	disabled bool
}

func newArtifactSyncer(c *client, dir string, logger *slog.Logger) *artifactSyncer {
	return &artifactSyncer{
		client:   c,
		dir:      dir,
		logger:   logger,
		upload:   &http.Client{},
		trigger:  make(chan struct{}, 1),
		previous: map[string]artifactState{},
		synced:   map[string]artifactState{},
		uploaded: map[string]uploadedArtifact{},
		skipped:  map[string]bool{},
	}
}

// poke asks for a sync without blocking; pokes coalesce while one is queued.
func (s *artifactSyncer) poke() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

// run syncs on every poke and every artifactSyncInterval until ctx is done,
// then runs a final bounded sync.
func (s *artifactSyncer) run(ctx context.Context) error {
	ticker := time.NewTicker(artifactSyncInterval)
	defer ticker.Stop()
	settle := time.NewTimer(0)
	if !settle.Stop() {
		<-settle.C
	}
	for {
		select {
		case <-ctx.Done():
			s.finalFlush()
			return nil
		case <-s.trigger:
		case <-ticker.C:
		case <-settle.C:
		}
		if s.sync(ctx) {
			settle.Reset(artifactSettleDelay)
		}
	}
}

// finalFlush syncs once more before the worker exits, waiting one settle delay
// for files that were still changing.
func (s *artifactSyncer) finalFlush() {
	ctx, cancel := context.WithTimeout(context.Background(), artifactFinalFlushTimeout)
	defer cancel()
	if !s.sync(ctx) {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(artifactSettleDelay):
	}
	s.sync(ctx)
}

// sync runs one scan and pushes its settled changes. It reports whether some
// file was still changing.
func (s *artifactSyncer) sync(ctx context.Context) bool {
	if s.disabled {
		return false
	}
	current, changing, err := s.scan()
	if err != nil {
		s.logger.Warn("artifacts: scan", "error", err)
		return false
	}
	s.previous = current

	var candidates []worker.ArtifactManifestFile
	for path, state := range current {
		if state.sha == "" {
			continue
		}
		if synced, ok := s.synced[path]; !ok || synced.sha != state.sha || synced.size != state.size {
			candidates = append(candidates, worker.ArtifactManifestFile{Path: path, Size: state.size, SHA256: state.sha})
		}
	}
	var deleted []string
	for path := range s.synced {
		if _, ok := current[path]; !ok {
			deleted = append(deleted, path)
		}
	}
	if len(candidates) == 0 && len(deleted) == 0 {
		return changing
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
	sort.Strings(deleted)

	changed := map[string]bool{}
	if len(candidates) > 0 {
		var response worker.ArtifactManifestResponse
		status, err := s.client.artifactJSON(ctx, artifactManifestPath, worker.ArtifactManifestRequest{Files: candidates}, &response)
		if status == http.StatusNotFound {
			s.disabled = true
			s.logger.Info("artifacts: control plane does not keep artifacts; sync stopped")
			return false
		}
		if err != nil {
			s.logger.Warn("artifacts: manifest", "error", err)
			return changing
		}
		for _, path := range response.Changed {
			changed[path] = true
		}
	}

	commit := worker.ArtifactCommitRequest{Files: []worker.ArtifactCommitFile{}, Deleted: deleted}
	for _, file := range candidates {
		state := current[file.Path]
		if !changed[file.Path] {
			s.synced[file.Path] = state
			continue
		}
		previous, ok := s.uploaded[file.Path]
		versionID := previous.versionID
		if !ok || previous.sha != state.sha {
			versionID, err = s.uploadFile(ctx, file)
			if err != nil {
				s.logger.Warn("artifacts: upload", "path", file.Path, "error", err)
				continue
			}
			s.uploaded[file.Path] = uploadedArtifact{versionID: versionID, sha: state.sha}
		}
		commit.Files = append(commit.Files, worker.ArtifactCommitFile{
			Path: file.Path, VersionID: versionID, Size: state.size, SHA256: state.sha, MTime: state.mtime.UTC(),
		})
	}
	if len(commit.Files) == 0 && len(commit.Deleted) == 0 {
		return changing
	}
	status, err := s.client.artifactJSON(ctx, artifactCommitPath, commit, nil)
	if err != nil {
		if status == http.StatusConflict {
			// A recorded version no longer exists or no longer matches; upload
			// the files again on the next scan.
			s.uploaded = map[string]uploadedArtifact{}
		}
		s.logger.Warn("artifacts: commit", "error", err)
		return true
	}
	for _, file := range commit.Files {
		s.synced[file.Path] = current[file.Path]
		delete(s.uploaded, file.Path)
	}
	for _, path := range commit.Deleted {
		delete(s.synced, path)
	}
	s.logger.Info("artifacts: synced", "files", len(commit.Files), "deleted", len(commit.Deleted))
	return changing
}

// scan walks the artifact directory through an os.Root, skipping symlinks,
// non-regular files and any .versions directory, and applying the file count,
// file size and total size limits in path order. It hashes each file a
// previous scan saw with the same size and modification time. changing
// reports a file that was new or modified since that scan.
func (s *artifactSyncer) scan() (map[string]artifactState, bool, error) {
	current := map[string]artifactState{}
	root, err := os.OpenRoot(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return current, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer root.Close()

	type entry struct {
		path string
		info fs.FileInfo
	}
	var entries []entry
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == "." {
				return walkErr
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == "." {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if d.Name() == domain.ArtifactVersionsDir {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil //nolint:nilerr // an entry that cannot be stat'd is skipped
		}
		if cleaned, err := domain.CleanArtifactPath(path); err != nil || cleaned != path {
			s.skipOnce(path, "path cannot be stored")
			return nil
		}
		entries = append(entries, entry{path: path, info: info})
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	changing := false
	var total int64
	for _, item := range entries {
		size := item.info.Size()
		if size > domain.MaxArtifactFileBytes {
			s.skipOnce(item.path, "file is over the per-file limit")
			continue
		}
		if len(current) >= domain.MaxSessionArtifactFiles || total+size > domain.MaxSessionArtifactBytes {
			s.skipOnce(item.path, "session artifact limit reached")
			continue
		}
		total += size
		state := artifactState{size: size, mtime: item.info.ModTime()}
		previous, seen := s.previous[item.path]
		if !seen || previous.size != state.size || !previous.mtime.Equal(state.mtime) {
			changing = true
			current[item.path] = state
			continue
		}
		state.sha = previous.sha
		if state.sha == "" {
			sha, err := hashArtifact(root, item.path, state)
			if err != nil {
				changing = true
				current[item.path] = state
				continue
			}
			state.sha = sha
		}
		current[item.path] = state
	}
	return current, changing, nil
}

func (s *artifactSyncer) skipOnce(path, reason string) {
	if s.skipped[path] {
		return
	}
	s.skipped[path] = true
	s.logger.Warn("artifacts: file not synced", "path", path, "reason", reason)
}

// hashArtifact returns the file's SHA-256, or an error when the file changed
// while it was read.
func hashArtifact(root *os.Root, path string, expected artifactState) (string, error) {
	file, err := root.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() != expected.size || !info.ModTime().Equal(expected.mtime) {
		return "", errors.New("file changed while it was hashed")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// uploadFile streams one file to the control plane and returns its stored
// version.
func (s *artifactSyncer) uploadFile(ctx context.Context, file worker.ArtifactManifestFile) (string, error) {
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	body, err := root.Open(file.Path)
	if err != nil {
		return "", err
	}
	defer body.Close()
	ctx, cancel := context.WithTimeout(ctx, artifactUploadTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPut,
		s.client.baseURL+artifactBlobPath+escapeArtifactPath(file.Path), io.LimitReader(body, file.Size))
	if err != nil {
		return "", err
	}
	request.ContentLength = file.Size
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set(worker.ArtifactSHA256Header, file.SHA256)
	s.client.authorize(request)
	response, err := s.upload.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("upload returned %d: %s", response.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var blob worker.ArtifactBlobResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBody)).Decode(&blob); err != nil {
		return "", fmt.Errorf("decode upload response: %w", err)
	}
	if blob.VersionID == "" {
		return "", errors.New("upload response has no version")
	}
	return blob.VersionID, nil
}

// escapeArtifactPath escapes each segment of a slash-separated path.
func escapeArtifactPath(path string) string {
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// artifactJSON posts body to a worker artifact route and decodes the response
// into out when out is not nil. It returns the response status with any error.
func (c *client) artifactJSON(ctx context.Context, path string, body any, out any) (int, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	c.authorize(request)
	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return response.StatusCode, fmt.Errorf("%s returned %d: %s", path, response.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBody)).Decode(out); err != nil {
			return response.StatusCode, fmt.Errorf("decode %s response: %w", path, err)
		}
	}
	return response.StatusCode, nil
}

// authorize sets the worker credential headers on a request.
func (c *client) authorize(request *http.Request) {
	if token := c.currentToken(); token != "" {
		request.Header.Set("Authorization", "Worker "+token)
		request.Header.Set(worker.CapabilitiesHeader, strings.Join(workerCapabilities, ","))
	}
}
