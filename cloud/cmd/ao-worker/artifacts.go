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
	"path/filepath"
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
// has been hashed. change identifies the file's inode and status change time
// where the platform reports them, so a rewrite that keeps the size and
// modification time is still seen.
type artifactState struct {
	size   int64
	mtime  time.Time
	change string
	sha    string
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
	// committedChanged asks the run loop to check the synced files against
	// the committed ones, after the control plane said they changed without
	// this worker.
	committedChanged chan struct{}
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
		client:           c,
		dir:              dir,
		logger:           logger,
		upload:           &http.Client{},
		trigger:          make(chan struct{}, 1),
		committedChanged: make(chan struct{}, 1),
		previous:         map[string]artifactState{},
		synced:           map[string]artifactState{},
		uploaded:         map[string]uploadedArtifact{},
		skipped:          map[string]bool{},
		unsettled:        map[string]int{},
	}
}

// poke asks for a sync without blocking; pokes coalesce while one is queued.
func (s *artifactSyncer) poke() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

// pokeCommittedChanged asks the run loop, without blocking, to check the
// synced files against the committed ones.
func (s *artifactSyncer) pokeCommittedChanged() {
	select {
	case s.committedChanged <- struct{}{}:
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
		case <-s.committedChanged:
			s.reconcileCommitted(ctx)
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
	// A disk kept from an earlier worker holds the files it synced; one that
	// is no longer committed was deleted while no worker ran.
	if len(s.synced) == 0 {
		committed := make(map[string]bool, len(list.Files))
		for _, file := range list.Files {
			committed[file.Path] = true
		}
		for path, saved := range s.loadSynced() {
			if !committed[path] && removeIfUnchanged(root, path, saved.sha) {
				s.logger.Info("artifacts: removed a file deleted while no worker ran", "path", path)
			}
		}
	}
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
	s.saveSynced()
	return restored, nil
}

// reconcileCommitted removes from the directory each synced file that is no
// longer committed, as when a user deleted it, unless the file changed since
// it was synced; a changed file stays and is synced as a new one.
func (s *artifactSyncer) reconcileCommitted(ctx context.Context) {
	var list worker.ArtifactListResponse
	if _, err := s.client.artifactJSON(ctx, artifactListPath, nil, &list); err != nil {
		s.logger.Warn("artifacts: list committed files", "error", err)
		return
	}
	committed := make(map[string]bool, len(list.Files))
	for _, file := range list.Files {
		committed[file.Path] = true
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		s.logger.Warn("artifacts: open directory", "error", err)
		return
	}
	defer root.Close()
	changed := false
	for path, synced := range s.synced {
		if committed[path] {
			continue
		}
		if removeIfUnchanged(root, path, synced.sha) {
			s.logger.Info("artifacts: removed a deleted file", "path", path)
		}
		delete(s.synced, path)
		delete(s.previous, path)
		delete(s.uploaded, path)
		changed = true
	}
	if changed {
		s.saveSynced()
	}
}

// removeIfUnchanged removes path when its content still has the SHA-256 sha,
// and reports whether it did.
func removeIfUnchanged(root *os.Root, path, sha string) bool {
	file, err := root.Open(path)
	if err != nil {
		return false
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	_ = file.Close()
	if err != nil || hex.EncodeToString(hash.Sum(nil)) != sha {
		return false
	}
	return root.Remove(path) == nil
}

// syncedFile is where the synced files are kept across worker restarts: next
// to the artifact directory, outside it, so a scan never sees it.
func (s *artifactSyncer) syncedFile() string {
	return filepath.Join(filepath.Dir(s.dir), "."+filepath.Base(s.dir)+".synced.json")
}

type savedArtifact struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// saveSynced records the synced files for the next worker on this disk.
func (s *artifactSyncer) saveSynced() {
	saved := make(map[string]savedArtifact, len(s.synced))
	for path, state := range s.synced {
		saved[path] = savedArtifact{Size: state.size, SHA256: state.sha}
	}
	encoded, err := json.Marshal(saved)
	if err == nil {
		temp := s.syncedFile() + ".tmp"
		if err = os.WriteFile(temp, encoded, 0o600); err == nil {
			err = os.Rename(temp, s.syncedFile())
		}
	}
	if err != nil {
		s.logger.Warn("artifacts: save synced files", "error", err)
	}
}

// loadSynced returns the synced files an earlier worker on this disk saved.
func (s *artifactSyncer) loadSynced() map[string]artifactState {
	encoded, err := os.ReadFile(s.syncedFile())
	if err != nil {
		return nil
	}
	var saved map[string]savedArtifact
	if json.Unmarshal(encoded, &saved) != nil {
		return nil
	}
	states := make(map[string]artifactState, len(saved))
	for path, file := range saved {
		states[path] = artifactState{size: file.Size, sha: file.SHA256}
	}
	return states
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
	return artifactState{size: info.Size(), mtime: info.ModTime(), change: fileChangeID(info), sha: file.SHA256}, nil
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
		s.saveSynced()
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

	// The control plane keeps every committed file this worker synced until a
	// commit deletes it, so each one still in the directory holds its slot
	// and its committed bytes first. A synced file that changed is admitted
	// when its size change fits what is left, and a new file when a slot and
	// its bytes do, in path order. A file that does not fit keeps its
	// committed version, so a commit never goes over the session limits.
	held := 0
	var total int64
	for path, synced := range s.synced {
		if scan.isPresent(path) {
			held++
			total += synced.size
		}
	}
	for _, item := range entries {
		size := item.info.Size()
		if size > domain.MaxArtifactFileBytes {
			s.skipOnce(item.path, "file is over the per-file limit")
			continue
		}
		committed, synced := s.synced[item.path]
		grown := size
		if synced {
			grown = size - committed.size
		}
		if (!synced && held >= domain.MaxSessionArtifactFiles) || total+grown > domain.MaxSessionArtifactBytes {
			s.skipOnce(item.path, "session artifact limit reached")
			continue
		}
		admit := func() {
			total += grown
			if !synced {
				held++
			}
		}
		state := artifactState{size: size, mtime: item.info.ModTime(), change: fileChangeID(item.info)}
		previous, seen := s.previous[item.path]
		if !seen || previous.size != state.size || !previous.mtime.Equal(state.mtime) || previous.change != state.change {
			admit()
			scan.current[item.path] = state
			s.noteUnsettled(item.path, &scan)
			continue
		}
		state.sha = previous.sha
		if state.sha == "" {
			sha, err := hashArtifact(root, item.path, state)
			if errors.Is(err, errArtifactFileChanged) {
				admit()
				scan.current[item.path] = state
				s.noteUnsettled(item.path, &scan)
				continue
			}
			if err != nil {
				s.skipOnce(item.path, "file cannot be read")
				continue
			}
			state.sha = sha
		}
		admit()
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
	if info.Size() != expected.size || !info.ModTime().Equal(expected.mtime) || fileChangeID(info) != expected.change {
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
