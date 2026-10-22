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
	"path"
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
	// artifactRestoreTimeout bounds the restore that runs before the agent
	// starts; a restore it cuts short is finished in the background.
	artifactRestoreTimeout = 30 * time.Second
	// artifactFinalFlushTimeout bounds the sync that runs at shutdown.
	artifactFinalFlushTimeout = 20 * time.Second
	// artifactRetryBase and artifactRetryMax bound the backoff after a failed
	// request: it doubles from the base on each failure in a row.
	artifactRetryBase = 10 * time.Second
	artifactRetryMax  = 5 * time.Minute
	// artifactDisabledRecheck is how long the syncer waits before asking again
	// a control plane that answered that it keeps no artifacts.
	artifactDisabledRecheck = 10 * time.Minute
	// artifactMaxUnsettledScans is how many scans in a row may see a file still
	// changing before it stops counting as a reason to rescan soon.
	artifactMaxUnsettledScans = 15
	// maxArtifactPathBytes keeps the bucket key, orgs/<uuid>/sessions/<uuid>/
	// plus the path, within the 1024-byte S3 limit.
	maxArtifactPathBytes = 1024 - len("orgs/00000000-0000-0000-0000-000000000000/sessions/00000000-0000-0000-0000-000000000000/")
)

// skippedArtifactDirs are directories the scan never enters: version history,
// and tool directories an agent may create that are not deliverables.
var skippedArtifactDirs = map[string]bool{
	domain.ArtifactVersionsDir: true,
	".git":                     true,
	"node_modules":             true,
}

// errArtifactFileChanged reports a file that changed while it was hashed.
var errArtifactFileChanged = errors.New("file changed while it was hashed")

const (
	artifactListPath     = "/worker/artifacts"
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
	// unsettled counts the scans in a row that saw each file still changing.
	unsettled map[string]int
	// disabledUntil pauses syncing after the control plane answered that it
	// keeps no artifacts, or for good after the worker was replaced.
	disabledUntil time.Time
	// failures counts failed requests in a row; retryAt is when the next
	// attempt may run.
	failures int
	retryAt  time.Time
	// refusedFiles is the fingerprint of the files a request was refused for
	// with a client error; the syncer waits until the files change.
	refusedFiles string
	// flushing is set during the shutdown flush, which ignores the backoff.
	flushing bool
	// restorePending is set while the committed files have not all been
	// restored; each sync tries again first, from restoreRetryAt.
	restorePending bool
	restoreRetryAt time.Time
}

// artifactScan is one walk of the artifact directory.
type artifactScan struct {
	// current holds the files that can be synced.
	current map[string]artifactState
	// present holds every regular file seen, synced or not.
	present map[string]bool
	// unreadable holds the directories whose contents could not be listed.
	unreadable []string
	// changing reports a file that was new or modified since the last scan.
	changing bool
}

// isPresent reports whether path exists in the directory as the scan saw it,
// including files it could not sync and files under unreadable directories.
func (scan artifactScan) isPresent(path string) bool {
	if scan.present[path] {
		return true
	}
	for _, dir := range scan.unreadable {
		if strings.HasPrefix(path, dir+"/") {
			return true
		}
	}
	return false
}

func newArtifactSyncer(c *client, dir string, logger *slog.Logger) *artifactSyncer {
	return &artifactSyncer{
		client:    c,
		dir:       dir,
		logger:    logger,
		upload:    &http.Client{},
		trigger:   make(chan struct{}, 1),
		previous:  map[string]artifactState{},
		synced:    map[string]artifactState{},
		uploaded:  map[string]uploadedArtifact{},
		skipped:   map[string]bool{},
		unsettled: map[string]int{},
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
		if s.restorePending {
			s.retryRestore(ctx)
		}
		if s.sync(ctx) {
			settle.Reset(artifactSettleDelay)
		}
	}
}

// restore puts back the session's committed artifact files that are not in
// the directory, so a worker on a new sandbox, after a restore or a
// replacement, starts with them. Each file is written in place, where the
// agent writes. A path that already exists keeps its local file, which counts
// as the committed file until a scan finds it changed. A restored or kept file
// counts as synced: it is not uploaded again unless it changes, and deleting
// it deletes the committed file. A path already synced is left alone, so a
// retry never brings back a file the agent deleted. It returns the number of
// files written; with a control plane that keeps no artifacts it writes none.
// A failed restore is tried again before each sync.
func (s *artifactSyncer) restore(ctx context.Context) (int, error) {
	s.restorePending = true
	var list worker.ArtifactListResponse
	status, err := s.client.artifactJSON(ctx, artifactListPath, nil, &list)
	if status == http.StatusNotFound {
		s.restorePending = false
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	restored := 0
	for _, file := range list.Files {
		if _, done := s.synced[file.Path]; done {
			continue
		}
		if !artifactPathStorable(file.Path) || file.Size > domain.MaxArtifactFileBytes {
			s.skipOnce(file.Path, "committed file cannot be restored")
			continue
		}
		committed := artifactState{size: file.Size, sha: file.SHA256}
		info, err := root.Lstat(file.Path)
		if err == nil {
			if info.Mode().IsRegular() {
				s.synced[file.Path] = committed
			}
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		state, err := s.restoreFile(ctx, root, file)
		if errors.Is(err, fs.ErrExist) {
			// The agent wrote the file while it was downloaded.
			s.synced[file.Path] = committed
			continue
		}
		if err != nil {
			return restored, fmt.Errorf("restore %s: %w", file.Path, err)
		}
		s.previous[file.Path] = state
		s.synced[file.Path] = state
		restored++
	}
	s.restorePending = false
	return restored, nil
}

// retryRestore runs a restore that has not finished, after any backoff.
func (s *artifactSyncer) retryRestore(ctx context.Context) {
	if time.Now().Before(s.restoreRetryAt) {
		return
	}
	restored, err := s.restore(ctx)
	if err != nil {
		s.restoreRetryAt = time.Now().Add(artifactSyncInterval)
		if errors.Is(err, errStaleWorker) {
			s.restorePending = false
		}
		s.logger.Warn("artifacts: restore", "error", err)
		return
	}
	if restored > 0 {
		s.logger.Info("artifacts: restored", "files", restored)
	}
}

// restoreFile downloads one committed file to its path in the directory. The
// file is created only if it does not exist, so it never replaces a file the
// agent wrote (that fails with fs.ErrExist), and a download that fails or
// does not match its size and SHA-256 is removed.
func (s *artifactSyncer) restoreFile(ctx context.Context, root *os.Root, file worker.ArtifactManifestFile) (artifactState, error) {
	ctx, cancel := context.WithTimeout(ctx, artifactUploadTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.client.baseURL+artifactBlobPath+escapeArtifactPath(file.Path), nil)
	if err != nil {
		return artifactState{}, err
	}
	s.client.authorize(request)
	response, err := s.upload.Do(request)
	if err != nil {
		return artifactState{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return artifactState{}, artifactResponseError("download", response.StatusCode, snippet)
	}
	if dir := path.Dir(file.Path); dir != "." {
		if err := root.MkdirAll(dir, 0o700); err != nil {
			return artifactState{}, err
		}
	}
	out, err := root.OpenFile(file.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return artifactState{}, err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(response.Body, file.Size+1))
	if err := out.Close(); copyErr == nil {
		copyErr = err
	}
	if copyErr == nil && (written != file.Size || hex.EncodeToString(hash.Sum(nil)) != file.SHA256) {
		copyErr = errors.New("downloaded file does not match its size and SHA-256")
	}
	if copyErr != nil {
		_ = root.Remove(file.Path)
		return artifactState{}, copyErr
	}
	info, err := root.Lstat(file.Path)
	if err != nil {
		return artifactState{}, err
	}
	return artifactState{size: info.Size(), mtime: info.ModTime(), sha: file.SHA256}, nil
}

// finalFlush syncs once more before the worker exits, waiting one settle delay
// for files that were still changing.
func (s *artifactSyncer) finalFlush() {
	ctx, cancel := context.WithTimeout(context.Background(), artifactFinalFlushTimeout)
	defer cancel()
	// The last chance to save the files: a backoff does not apply, a refusal
	// and a stopped syncer still do.
	s.flushing = true
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
// file was still changing, which schedules a settle rescan. A failed request
// never does: it backs off instead, or, refused with a client error, waits
// until the files change.
func (s *artifactSyncer) sync(ctx context.Context) bool {
	now := time.Now()
	if now.Before(s.disabledUntil) {
		return false
	}
	scan, err := s.scan()
	if err != nil {
		s.logger.Warn("artifacts: scan", "error", err)
		return false
	}
	s.previous = scan.current
	fingerprint := artifactFingerprint(scan.current)
	if s.refusedFiles != "" && fingerprint == s.refusedFiles {
		return scan.changing
	}
	if now.Before(s.retryAt) && !s.flushing {
		return scan.changing
	}

	var candidates []worker.ArtifactManifestFile
	for path, state := range scan.current {
		if state.sha == "" {
			continue
		}
		if synced, ok := s.synced[path]; !ok || synced.sha != state.sha || synced.size != state.size {
			candidates = append(candidates, worker.ArtifactManifestFile{Path: path, Size: state.size, SHA256: state.sha})
		}
	}
	var deleted []string
	for path := range s.synced {
		if _, ok := scan.current[path]; !ok && !scan.isPresent(path) {
			deleted = append(deleted, path)
		}
	}
	if len(candidates) == 0 && len(deleted) == 0 {
		return scan.changing
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
	sort.Strings(deleted)

	changed := map[string]bool{}
	if len(candidates) > 0 {
		var response worker.ArtifactManifestResponse
		status, err := s.client.artifactJSON(ctx, artifactManifestPath, worker.ArtifactManifestRequest{Files: candidates}, &response)
		if status == http.StatusNotFound {
			s.disabledUntil = now.Add(artifactDisabledRecheck)
			s.logger.Info("artifacts: control plane does not keep artifacts; checking again later", "in", artifactDisabledRecheck)
			return false
		}
		if err != nil {
			s.fail("manifest", status, err, fingerprint)
			return scan.changing
		}
		for _, path := range response.Changed {
			changed[path] = true
		}
	}

	commit := worker.ArtifactCommitRequest{Files: []worker.ArtifactCommitFile{}, Deleted: deleted}
	var uploadStatus int
	var uploadErr error
	for _, file := range candidates {
		state := scan.current[file.Path]
		if !changed[file.Path] {
			s.synced[file.Path] = state
			continue
		}
		previous, ok := s.uploaded[file.Path]
		versionID := previous.versionID
		if !ok || previous.sha != state.sha {
			var status int
			versionID, status, err = s.uploadFile(ctx, file)
			if err != nil {
				if status == http.StatusUnprocessableEntity {
					// The body did not match the cached hash: hash it again.
					state.sha = ""
					s.previous[file.Path] = state
				}
				if uploadErr == nil {
					uploadStatus, uploadErr = status, fmt.Errorf("%s: %w", file.Path, err)
				}
				continue
			}
			s.uploaded[file.Path] = uploadedArtifact{versionID: versionID, sha: state.sha}
		}
		commit.Files = append(commit.Files, worker.ArtifactCommitFile{
			Path: file.Path, VersionID: versionID, Size: state.size, SHA256: state.sha, MTime: state.mtime.UTC(),
		})
	}
	if len(commit.Files) > 0 || len(commit.Deleted) > 0 {
		status, err := s.client.artifactJSON(ctx, artifactCommitPath, commit, nil)
		if err != nil {
			if status == http.StatusConflict {
				// A recorded version is no longer an open upload or no longer
				// matches; upload the files again.
				s.uploaded = map[string]uploadedArtifact{}
			}
			s.fail("commit", status, err, fingerprint)
			return scan.changing
		}
		for _, file := range commit.Files {
			s.synced[file.Path] = scan.current[file.Path]
			delete(s.uploaded, file.Path)
		}
		for _, path := range commit.Deleted {
			delete(s.synced, path)
		}
		s.logger.Info("artifacts: synced", "files", len(commit.Files), "deleted", len(commit.Deleted))
	}
	if uploadErr != nil {
		s.fail("upload", uploadStatus, uploadErr, fingerprint)
		return scan.changing
	}
	s.failures, s.retryAt, s.refusedFiles = 0, time.Time{}, ""
	return scan.changing
}

// fail records a failed request. A replaced worker stops syncing. A client
// error that sending again cannot fix waits until the files change; any other
// failure is retried after a backoff that doubles up to artifactRetryMax.
func (s *artifactSyncer) fail(what string, status int, err error, fingerprint string) {
	if errors.Is(err, errStaleWorker) {
		s.disabledUntil = time.Now().Add(100 * 365 * 24 * time.Hour)
		s.logger.Info("artifacts: worker was replaced; sync stopped")
		return
	}
	retryable := status == 0 || status == http.StatusUnauthorized || status == http.StatusConflict ||
		status == http.StatusTooManyRequests || status >= 500 ||
		// An upload over the session's limit on uncommitted versions clears
		// once commits and the sweeper catch up; a rejected checksum is hashed
		// again.
		(what == "upload" && (status == http.StatusRequestEntityTooLarge || status == http.StatusUnprocessableEntity))
	if !retryable {
		s.refusedFiles = fingerprint
		s.logger.Warn("artifacts: "+what+" refused; waiting for the files to change", "status", status, "error", err)
		return
	}
	s.failures++
	delay := artifactRetryMax
	if s.failures <= 6 {
		delay = min(artifactRetryBase<<(s.failures-1), artifactRetryMax)
	}
	s.retryAt = time.Now().Add(delay)
	s.logger.Warn("artifacts: "+what, "status", status, "error", err, "retry_in", delay)
}

// artifactFingerprint identifies the settled files by path, size and
// modification time. Files still changing are left out, so a file that keeps
// growing does not count as a change after a refusal.
func artifactFingerprint(files map[string]artifactState) string {
	paths := make([]string, 0, len(files))
	for path, state := range files {
		if state.sha != "" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	var b strings.Builder
	for _, path := range paths {
		state := files[path]
		fmt.Fprintf(&b, "%s\x00%d\x00%d\n", path, state.size, state.mtime.UnixNano())
	}
	return b.String()
}

// scan walks the artifact directory through an os.Root, skipping symlinks,
// non-regular files and skippedArtifactDirs, and applying the path length,
// file count, file size and total size limits in path order. It hashes each
// file a previous scan saw with the same size and modification time.
func (s *artifactSyncer) scan() (artifactScan, error) {
	scan := artifactScan{current: map[string]artifactState{}, present: map[string]bool{}}
	root, err := os.OpenRoot(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return scan, nil
	}
	if err != nil {
		return artifactScan{}, err
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
			if d == nil || d.IsDir() {
				scan.unreadable = append(scan.unreadable, path)
				s.skipOnce(path, "directory cannot be read")
				return fs.SkipDir
			}
			scan.present[path] = true
			return nil
		}
		if path == "." {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if skippedArtifactDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			scan.present[path] = true
			return nil //nolint:nilerr // an entry that cannot be stat'd is skipped
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		scan.present[path] = true
		if !artifactPathStorable(path) {
			s.skipOnce(path, "path cannot be stored")
			return nil
		}
		entries = append(entries, entry{path: path, info: info})
		return nil
	})
	if err != nil {
		return artifactScan{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	// Files this worker synced keep their slots: the control plane holds
	// them, so new files fill only what is left, in path order. A synced file
	// that can no longer be sent still occupies its slot and bytes.
	sizes := make(map[string]int64, len(entries))
	for _, item := range entries {
		sizes[item.path] = item.info.Size()
	}
	held := 0
	var total int64
	for path, synced := range s.synced {
		size, listed := sizes[path]
		if (!listed || size > domain.MaxArtifactFileBytes) && scan.isPresent(path) {
			held++
			total += synced.size
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		_, iSynced := s.synced[entries[i].path]
		_, jSynced := s.synced[entries[j].path]
		return iSynced && !jSynced
	})
	for _, item := range entries {
		size := item.info.Size()
		if size > domain.MaxArtifactFileBytes {
			s.skipOnce(item.path, "file is over the per-file limit")
			continue
		}
		if held+len(scan.current) >= domain.MaxSessionArtifactFiles || total+size > domain.MaxSessionArtifactBytes {
			s.skipOnce(item.path, "session artifact limit reached")
			continue
		}
		total += size
		state := artifactState{size: size, mtime: item.info.ModTime()}
		previous, seen := s.previous[item.path]
		if !seen || previous.size != state.size || !previous.mtime.Equal(state.mtime) {
			scan.current[item.path] = state
			s.noteUnsettled(item.path, &scan)
			continue
		}
		state.sha = previous.sha
		if state.sha == "" {
			sha, err := hashArtifact(root, item.path, state)
			if errors.Is(err, errArtifactFileChanged) {
				scan.current[item.path] = state
				s.noteUnsettled(item.path, &scan)
				continue
			}
			if err != nil {
				delete(scan.current, item.path)
				if _, synced := s.synced[item.path]; synced {
					held++
				}
				s.skipOnce(item.path, "file cannot be read")
				continue
			}
			state.sha = sha
		}
		delete(s.unsettled, item.path)
		scan.current[item.path] = state
	}
	return scan, nil
}

// artifactPathStorable reports whether path is a valid artifact path that
// fits in a bucket key.
func artifactPathStorable(path string) bool {
	cleaned, err := domain.CleanArtifactPath(path)
	return err == nil && cleaned == path && len(path) <= maxArtifactPathBytes
}

// noteUnsettled counts a scan that saw path still changing. A file that keeps
// changing stops scheduling settle rescans after artifactMaxUnsettledScans;
// it is synced once it holds still.
func (s *artifactSyncer) noteUnsettled(path string, scan *artifactScan) {
	s.unsettled[path]++
	if s.unsettled[path] > artifactMaxUnsettledScans {
		s.skipOnce(path, "file keeps changing")
		return
	}
	scan.changing = true
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
		return "", errArtifactFileChanged
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// uploadFile streams one file to the control plane and returns its stored
// version, and the response status with any error.
func (s *artifactSyncer) uploadFile(ctx context.Context, file worker.ArtifactManifestFile) (string, int, error) {
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return "", 0, err
	}
	defer root.Close()
	opened, err := root.Open(file.Path)
	if err != nil {
		return "", 0, err
	}
	defer opened.Close()
	ctx, cancel := context.WithTimeout(ctx, artifactUploadTimeout)
	defer cancel()
	// An empty body must be http.NoBody: any other reader with a zero length
	// is sent chunked, and the control plane requires Content-Length.
	var body io.Reader = http.NoBody
	if file.Size > 0 {
		body = io.LimitReader(opened, file.Size)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut,
		s.client.baseURL+artifactBlobPath+escapeArtifactPath(file.Path), body)
	if err != nil {
		return "", 0, err
	}
	request.ContentLength = file.Size
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set(worker.ArtifactSHA256Header, file.SHA256)
	s.client.authorize(request)
	response, err := s.upload.Do(request)
	if err != nil {
		return "", 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", response.StatusCode, artifactResponseError("upload", response.StatusCode, snippet)
	}
	var blob worker.ArtifactBlobResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBody)).Decode(&blob); err != nil {
		return "", response.StatusCode, fmt.Errorf("decode upload response: %w", err)
	}
	if blob.VersionID == "" {
		return "", response.StatusCode, errors.New("upload response has no version")
	}
	return blob.VersionID, response.StatusCode, nil
}

// artifactResponseError describes a failed artifact request, as
// errStaleWorker when the control plane replaced this worker.
func artifactResponseError(what string, status int, snippet []byte) error {
	if status == http.StatusUnauthorized && bytes.Contains(snippet, []byte("STALE_WORKER_TOKEN")) {
		return fmt.Errorf("%s: %w", what, errStaleWorker)
	}
	return fmt.Errorf("%s returned %d: %s", what, status, strings.TrimSpace(string(snippet)))
}

// escapeArtifactPath escapes each segment of a slash-separated path.
func escapeArtifactPath(path string) string {
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// artifactJSON posts body to a worker artifact route, or gets the route when
// body is nil, and decodes the response into out when out is not nil. It returns the response status with any error.
func (c *client) artifactJSON(ctx context.Context, path string, body any, out any) (int, error) {
	method, payload := http.MethodGet, io.Reader(http.NoBody)
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		method, payload = http.MethodPost, bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return 0, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	c.authorize(request)
	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return response.StatusCode, artifactResponseError(path, response.StatusCode, snippet)
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
