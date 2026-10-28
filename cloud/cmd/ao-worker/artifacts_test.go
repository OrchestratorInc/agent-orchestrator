package main

import (
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
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

type fakeArtifactRow struct {
	size      int64
	sha       string
	versionID string
}

// fakeArtifactPlane implements the three worker artifact routes in memory.
type fakeArtifactPlane struct {
	mu          sync.Mutex
	rows        map[string]fakeArtifactRow
	versions    map[string]string
	bodies      map[string][]byte
	manifests   int
	uploads     int
	commits     int
	failCommits int
	// refuseCommits answers every commit with this status when set.
	refuseCommits int
	// staleCommits answers every commit as from a replaced worker.
	staleCommits bool
	// rejectUploads answers that many uploads with 422.
	rejectUploads int
	disabled      bool
	// corruptDownloads serves downloads with a changed body.
	corruptDownloads bool
	// failDownload answers downloads of this path with 503.
	failDownload string
	deleted      []string
	nextVersion  int
}

func newFakeArtifactPlane(t *testing.T) (*fakeArtifactPlane, *client) {
	t.Helper()
	plane := &fakeArtifactPlane{rows: map[string]fakeArtifactRow{}, versions: map[string]string{}, bodies: map[string][]byte{}}
	server := httptest.NewServer(plane)
	t.Cleanup(server.Close)
	return plane, &client{baseURL: server.URL, http: server.Client(), token: "worker-token"}
}

func (p *fakeArtifactPlane) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.disabled {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Worker worker-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == artifactManifestPath:
		p.manifests++
		var input worker.ArtifactManifestRequest
		_ = json.NewDecoder(r.Body).Decode(&input)
		changed := []string{}
		for _, file := range input.Files {
			row, ok := p.rows[file.Path]
			if !ok || row.size != file.Size || row.sha != file.SHA256 {
				changed = append(changed, file.Path)
			}
		}
		_ = json.NewEncoder(w).Encode(worker.ArtifactManifestResponse{Changed: changed})
	case r.Method == http.MethodGet && r.URL.Path == artifactListPath:
		files := []worker.ArtifactManifestFile{}
		for path, row := range p.rows {
			files = append(files, worker.ArtifactManifestFile{Path: path, Size: row.size, SHA256: row.sha})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		_ = json.NewEncoder(w).Encode(worker.ArtifactListResponse{Files: files})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, artifactBlobPath):
		path, _ := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), artifactBlobPath))
		row, ok := p.rows[path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if path == p.failDownload {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		body := p.bodies[row.versionID]
		if p.corruptDownloads {
			body = append([]byte("x"), body[1:]...)
		}
		_, _ = w.Write(body)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, artifactBlobPath):
		path, _ := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), artifactBlobPath))
		body, _ := io.ReadAll(r.Body)
		if p.rejectUploads > 0 {
			p.rejectUploads--
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != r.Header.Get(worker.ArtifactSHA256Header) || int64(len(body)) != r.ContentLength {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		p.uploads++
		p.nextVersion++
		version := "v" + string(rune('0'+p.nextVersion))
		p.versions[version] = hex.EncodeToString(sum[:])
		p.bodies[version] = body
		_ = json.NewEncoder(w).Encode(worker.ArtifactBlobResponse{Path: path, VersionID: version})
	case r.Method == http.MethodPost && r.URL.Path == artifactCommitPath:
		var input worker.ArtifactCommitRequest
		_ = json.NewDecoder(r.Body).Decode(&input)
		if p.failCommits > 0 {
			p.failCommits--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if p.refuseCommits != 0 {
			w.WriteHeader(p.refuseCommits)
			return
		}
		if p.staleCommits {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"STALE_WORKER_TOKEN"}`))
			return
		}
		for _, file := range input.Files {
			if p.versions[file.VersionID] != file.SHA256 {
				w.WriteHeader(http.StatusConflict)
				return
			}
		}
		p.commits++
		for _, file := range input.Files {
			p.rows[file.Path] = fakeArtifactRow{size: file.Size, sha: file.SHA256, versionID: file.VersionID}
		}
		for _, path := range input.Deleted {
			delete(p.rows, path)
			p.deleted = append(p.deleted, path)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (p *fakeArtifactPlane) paths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	paths := make([]string, 0, len(p.rows))
	for path := range p.rows {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func (p *fakeArtifactPlane) counts() (manifests, uploads, commits int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.manifests, p.uploads, p.commits
}

func writeArtifact(t *testing.T, dir, path, body string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testSyncer(c *client, dir string) *artifactSyncer {
	return newArtifactSyncer(c, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// syncSettled runs the two scans a new or changed file needs.
func syncSettled(s *artifactSyncer) {
	s.sync(context.Background())
	s.sync(context.Background())
}

func TestArtifactSyncWaitsForFilesToSettle(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	s := testSyncer(c, dir)
	writeArtifact(t, dir, "report.html", "<h1>draft</h1>")

	if changing := s.sync(context.Background()); !changing {
		t.Fatal("a new file was not reported as changing")
	}
	if manifests, uploads, _ := plane.counts(); manifests != 0 || uploads != 0 {
		t.Fatalf("first scan sent requests: manifests=%d uploads=%d", manifests, uploads)
	}
	// A file still being written stays unsettled.
	writeArtifact(t, dir, "report.html", "<h1>draft, longer</h1>")
	s.sync(context.Background())
	if _, uploads, _ := plane.counts(); uploads != 0 {
		t.Fatal("a file that changed between scans was uploaded")
	}
	if changing := s.sync(context.Background()); changing {
		t.Fatal("a settled file was reported as changing")
	}
	if got := plane.paths(); !slices.Equal(got, []string{"report.html"}) {
		t.Fatalf("committed = %v", got)
	}
}

func TestArtifactSyncSkipsSymlinksVersionsAndOversizedFiles(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(dir, "linked-dir")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writeArtifact(t, dir, ".versions/report.html/1.html", "old")
	writeArtifact(t, dir, "nested/.versions/x.md", "old")
	writeArtifact(t, dir, "report.html", "current")
	big, err := os.Create(filepath.Join(dir, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(26 << 20); err != nil {
		t.Fatal(err)
	}
	_ = big.Close()

	syncSettled(testSyncer(c, dir))
	if got := plane.paths(); !slices.Equal(got, []string{"report.html"}) {
		t.Fatalf("committed = %v, want only report.html", got)
	}
}

func TestArtifactSyncSendsNoUploadForUnchangedFiles(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "a.md", "a")
	writeArtifact(t, dir, "assets/b.png", "b")
	s := testSyncer(c, dir)
	syncSettled(s)
	manifests, uploads, commits := plane.counts()
	if uploads != 2 || commits != 1 {
		t.Fatalf("first sync uploads=%d commits=%d", uploads, commits)
	}
	s.sync(context.Background())
	if m, u, c := plane.counts(); m != manifests || u != uploads || c != commits {
		t.Fatalf("an unchanged scan made requests: manifests %d->%d uploads %d->%d commits %d->%d",
			manifests, m, uploads, u, commits, c)
	}

	// A restarted worker with the same files asks once and uploads nothing.
	restarted := testSyncer(c, dir)
	syncSettled(restarted)
	if _, u, c := plane.counts(); u != uploads || c != commits {
		t.Fatalf("restart re-uploaded: uploads %d->%d commits %d->%d", uploads, u, commits, c)
	}
}

func TestArtifactSyncDeletesOnlyFilesItSynced(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "keep.md", "keep")
	writeArtifact(t, dir, "drop.md", "drop")
	s := testSyncer(c, dir)
	syncSettled(s)

	if err := os.Remove(filepath.Join(dir, "drop.md")); err != nil {
		t.Fatal(err)
	}
	s.sync(context.Background())
	if got := plane.paths(); !slices.Equal(got, []string{"keep.md"}) {
		t.Fatalf("after delete committed = %v", got)
	}

	// A restore onto a fresh disk starts with an empty directory: nothing is
	// deleted from the stored list.
	fresh := testSyncer(c, t.TempDir())
	syncSettled(fresh)
	if got := plane.paths(); !slices.Equal(got, []string{"keep.md"}) {
		t.Fatalf("fresh disk changed the stored list: %v", got)
	}
}

func TestArtifactSyncRetriesFailedCommitWithoutReuploading(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "report.html", "report")
	plane.failCommits = 1
	s := testSyncer(c, dir)
	syncSettled(s)
	if got := plane.paths(); len(got) != 0 {
		t.Fatalf("failed commit stored %v", got)
	}
	if s.failures != 1 || !s.retryAt.After(time.Now()) {
		t.Fatalf("failed commit: failures=%d retryAt=%v, want a backoff", s.failures, s.retryAt)
	}
	s.sync(context.Background())
	if _, _, commits := plane.counts(); commits != 0 {
		t.Fatal("a sync during the backoff retried the commit")
	}
	s.retryAt = time.Time{}
	if changing := s.sync(context.Background()); changing {
		t.Fatal("retry after a failed commit reported a change")
	}
	_, uploads, commits := plane.counts()
	if commits != 1 || uploads != 1 {
		t.Fatalf("retry uploads=%d commits=%d, want the first version reused", uploads, commits)
	}
	if got := plane.paths(); !slices.Equal(got, []string{"report.html"}) {
		t.Fatalf("committed = %v", got)
	}
}

func TestArtifactSyncStopsWhenControlPlaneHasNoArtifacts(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	plane.disabled = true
	dir := t.TempDir()
	writeArtifact(t, dir, "report.html", "report")
	s := testSyncer(c, dir)
	syncSettled(s)
	if !s.disabledUntil.After(time.Now()) {
		t.Fatal("syncer kept running against a control plane without artifacts")
	}
}

func TestArtifactSyncFinalFlushOnShutdown(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	s := testSyncer(c, dir)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	writeArtifact(t, dir, "last.md", "written just before shutdown")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(artifactFinalFlushTimeout):
		t.Fatal("final flush did not finish")
	}
	if got := plane.paths(); !slices.Equal(got, []string{"last.md"}) {
		t.Fatalf("final flush committed %v", got)
	}
}

func TestEscapeArtifactPath(t *testing.T) {
	if got := escapeArtifactPath("my docs/rep ort#1.html"); got != "my%20docs/rep%20ort%231.html" {
		t.Fatalf("escapeArtifactPath = %q", got)
	}
}

func TestArtifactSyncBacksOffAfterRepeatedFailures(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "report.html", "report")
	plane.failCommits = 3
	s := testSyncer(c, dir)
	syncSettled(s)
	var delays []time.Duration
	for range 2 {
		delays = append(delays, time.Until(s.retryAt).Round(time.Second))
		s.retryAt = time.Time{}
		if changing := s.sync(context.Background()); changing {
			t.Fatal("a failed request scheduled a settle rescan")
		}
	}
	if delays[0] != artifactRetryBase || delays[1] != 2*artifactRetryBase {
		t.Fatalf("backoff delays = %v, want %v then %v", delays, artifactRetryBase, 2*artifactRetryBase)
	}
	s.retryAt = time.Time{}
	s.sync(context.Background())
	if got := plane.paths(); !slices.Equal(got, []string{"report.html"}) || s.failures != 0 {
		t.Fatalf("after recovery committed = %v, failures = %d", got, s.failures)
	}
}

func TestArtifactSyncWaitsForFilesToChangeAfterARefusal(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "report.html", "report")
	plane.refuseCommits = http.StatusRequestEntityTooLarge
	s := testSyncer(c, dir)
	syncSettled(s)
	manifests, uploads, _ := plane.counts()
	s.sync(context.Background())
	s.sync(context.Background())
	if m, u, _ := plane.counts(); m != manifests || u != uploads {
		t.Fatalf("a refused commit was retried with the same files: manifests %d->%d uploads %d->%d", manifests, m, uploads, u)
	}
	plane.mu.Lock()
	plane.refuseCommits = 0
	plane.mu.Unlock()
	writeArtifact(t, dir, "notes.md", "notes")
	syncSettled(s)
	if got := plane.paths(); !slices.Equal(got, []string{"notes.md", "report.html"}) {
		t.Fatalf("after the files changed committed = %v", got)
	}
}

func TestArtifactSyncStopsForAReplacedWorker(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "report.html", "report")
	plane.staleCommits = true
	s := testSyncer(c, dir)
	syncSettled(s)
	if !s.disabledUntil.After(time.Now().Add(24 * time.Hour)) {
		t.Fatalf("disabledUntil = %v, want the syncer stopped", s.disabledUntil)
	}
}

func TestArtifactSyncUploadsEmptyFiles(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "placeholder.md", "")
	syncSettled(testSyncer(c, dir))
	if got := plane.paths(); !slices.Equal(got, []string{"placeholder.md"}) {
		t.Fatalf("committed = %v, want the empty file", got)
	}
}

func TestArtifactSyncRehashesAfterAChecksumRejection(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "report.html", "report")
	plane.rejectUploads = 1
	s := testSyncer(c, dir)
	syncSettled(s)
	if state := s.previous["report.html"]; state.sha != "" {
		t.Fatalf("cached sha kept after a checksum rejection: %q", state.sha)
	}
	s.retryAt = time.Time{}
	syncSettled(s)
	if got := plane.paths(); !slices.Equal(got, []string{"report.html"}) {
		t.Fatalf("committed = %v", got)
	}
}

func TestArtifactSyncKeepsFilesItCannotSync(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "report.csv", "small")
	writeArtifact(t, dir, "locked/notes.md", "notes")
	s := testSyncer(c, dir)
	syncSettled(s)
	if got := plane.paths(); !slices.Equal(got, []string{"locked/notes.md", "report.csv"}) {
		t.Fatalf("committed = %v", got)
	}
	// The file grows past the per-file limit: its last good version stays.
	big, err := os.OpenFile(filepath.Join(dir, "report.csv"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(26 << 20); err != nil {
		t.Fatal(err)
	}
	_ = big.Close()
	// A directory that cannot be listed keeps its files.
	if err := os.Chmod(filepath.Join(dir, "locked"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "locked"), 0o700) })
	if _, err := os.ReadDir(filepath.Join(dir, "locked")); err == nil {
		t.Skip("directory permissions are not enforced here")
	}
	syncSettled(s)
	if got := plane.paths(); !slices.Equal(got, []string{"locked/notes.md", "report.csv"}) {
		t.Fatalf("files the syncer could not read were deleted: %v", got)
	}
}

func TestArtifactPathStorable(t *testing.T) {
	if !artifactPathStorable("docs/report.html") {
		t.Fatal("a short path is not storable")
	}
	if !artifactPathStorable(strings.Repeat("a", maxArtifactPathBytes)) {
		t.Fatal("a path at the key limit is not storable")
	}
	if artifactPathStorable(strings.Repeat("a", maxArtifactPathBytes+1)) {
		t.Fatal("a path over the key limit is storable")
	}
	if artifactPathStorable("CON.md") {
		t.Fatal("an invalid path is storable")
	}
}

func TestArtifactSyncSkipsToolDirectories(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, ".git/HEAD", "ref")
	writeArtifact(t, dir, "app/node_modules/x/index.js", "x")
	writeArtifact(t, dir, "report.html", "report")
	syncSettled(testSyncer(c, dir))
	if got := plane.paths(); !slices.Equal(got, []string{"report.html"}) {
		t.Fatalf("committed = %v, want only report.html", got)
	}
}

func TestArtifactSyncStopsRescanningAFileThatKeepsChanging(t *testing.T) {
	_, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	s := testSyncer(c, dir)
	changing := true
	for index := range artifactMaxUnsettledScans + 1 {
		writeArtifact(t, dir, "log.txt", strings.Repeat("x", index+1))
		changing = s.sync(context.Background())
	}
	if changing {
		t.Fatal("a file that keeps changing still schedules settle rescans")
	}
}

// At the file cap, a new file that sorts first does not push a synced file
// out: the synced files keep their slots and edits to them still sync.
func TestArtifactSyncKeepsSyncedFilesAtTheCap(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	for index := range domain.MaxSessionArtifactFiles {
		writeArtifact(t, dir, fmt.Sprintf("f%03d.md", index), "x")
	}
	s := testSyncer(c, dir)
	syncSettled(s)
	if got := len(plane.paths()); got != domain.MaxSessionArtifactFiles {
		t.Fatalf("synced %d files, want %d", got, domain.MaxSessionArtifactFiles)
	}
	writeArtifact(t, dir, "a.md", "new")
	writeArtifact(t, dir, "f199.md", "edited")
	syncSettled(s)
	paths := plane.paths()
	if slices.Contains(paths, "a.md") || !slices.Contains(paths, "f199.md") || len(paths) != domain.MaxSessionArtifactFiles {
		t.Fatalf("at the cap: %d files, a.md=%v, f199.md=%v", len(paths), slices.Contains(paths, "a.md"), slices.Contains(paths, "f199.md"))
	}
	plane.mu.Lock()
	edited := plane.rows["f199.md"].size
	plane.mu.Unlock()
	if edited != int64(len("edited")) {
		t.Fatalf("edit to a synced file at the cap was not synced: size %d", edited)
	}
}

func TestArtifactSyncRefusalIgnoresAFileThatKeepsChanging(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "report.html", "report")
	plane.refuseCommits = http.StatusRequestEntityTooLarge
	s := testSyncer(c, dir)
	syncSettled(s)
	manifests, _, _ := plane.counts()
	for index := range 4 {
		writeArtifact(t, dir, "progress.log", strings.Repeat("x", index+1))
		s.sync(context.Background())
	}
	if m, _, _ := plane.counts(); m != manifests {
		t.Fatalf("a growing log resent the refused request: manifests %d -> %d", manifests, m)
	}
}

func TestArtifactSyncRefusalIgnoresAnUnreadableFile(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "report.html", "report")
	writeArtifact(t, dir, "secret.bin", "secret")
	if err := os.Chmod(filepath.Join(dir, "secret.bin"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "secret.bin"), 0o600) })
	if _, err := os.ReadFile(filepath.Join(dir, "secret.bin")); err == nil {
		t.Skip("file permissions are not enforced here")
	}
	plane.refuseCommits = http.StatusRequestEntityTooLarge
	s := testSyncer(c, dir)
	syncSettled(s)
	manifests, _, _ := plane.counts()
	for range 6 {
		s.sync(context.Background())
	}
	if m, _, _ := plane.counts(); m != manifests {
		t.Fatalf("an unreadable file resent the refused request: manifests %d -> %d", manifests, m)
	}
}

func TestArtifactSyncFinalFlushIgnoresTheBackoff(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "report.html", "report")
	plane.failCommits = 1
	s := testSyncer(c, dir)
	syncSettled(s)
	if !s.retryAt.After(time.Now()) {
		t.Fatal("no backoff after a failed commit")
	}
	writeArtifact(t, dir, "last.md", "written during the backoff")
	s.finalFlush()
	if got := plane.paths(); !slices.Equal(got, []string{"last.md", "report.html"}) {
		t.Fatalf("final flush during a backoff committed %v", got)
	}
}

func TestArtifactRestorePutsBackCommittedFiles(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	first := t.TempDir()
	writeArtifact(t, first, "report.html", "<h1>report</h1>")
	writeArtifact(t, first, "docs/notes.md", "# notes")
	syncSettled(testSyncer(c, first))
	manifests, uploads, commits := plane.counts()

	// A new sandbox starts with an empty directory.
	dir := t.TempDir()
	s := testSyncer(c, dir)
	restored, err := s.restore(context.Background())
	if err != nil || restored != 2 {
		t.Fatalf("restore = %d, %v", restored, err)
	}
	for path, want := range map[string]string{"report.html": "<h1>report</h1>", "docs/notes.md": "# notes"} {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v", path, got, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("restore wrote more than the committed files: %v, %v", entries, err)
	}

	// Restored files count as synced: no request goes out for them.
	syncSettled(s)
	if m, u, c := plane.counts(); m != manifests || u != uploads || c != commits {
		t.Fatalf("restored files were synced again: manifests %d->%d uploads %d->%d commits %d->%d", manifests, m, uploads, u, commits, c)
	}
	// Deleting a restored file deletes the committed one.
	if err := os.Remove(filepath.Join(dir, "docs", "notes.md")); err != nil {
		t.Fatal(err)
	}
	s.sync(context.Background())
	if got := plane.paths(); !slices.Equal(got, []string{"report.html"}) {
		t.Fatalf("after deleting a restored file committed = %v", got)
	}
}

func TestArtifactRestoreKeepsLocalFiles(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	first := t.TempDir()
	writeArtifact(t, first, "report.html", "<h1>committed</h1>")
	writeArtifact(t, first, "notes.md", "# notes")
	syncSettled(testSyncer(c, first))

	dir := t.TempDir()
	writeArtifact(t, dir, "report.html", "<h1>local edit</h1>")
	s := testSyncer(c, dir)
	if restored, err := s.restore(context.Background()); err != nil || restored != 1 {
		t.Fatalf("restore = %d, %v", restored, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "report.html")); string(got) != "<h1>local edit</h1>" {
		t.Fatalf("restore replaced a local file: %q", got)
	}
	syncSettled(s)
	plane.mu.Lock()
	committed := plane.rows["report.html"].sha
	plane.mu.Unlock()
	if want := sha256.Sum256([]byte("<h1>local edit</h1>")); committed != hex.EncodeToString(want[:]) {
		t.Fatal("the local file was not synced over the committed one")
	}
}

func TestArtifactRestoreRetriesAfterABadDownload(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	first := t.TempDir()
	writeArtifact(t, first, "report.html", "<h1>report</h1>")
	syncSettled(testSyncer(c, first))

	dir := t.TempDir()
	s := testSyncer(c, dir)
	plane.mu.Lock()
	plane.corruptDownloads = true
	plane.mu.Unlock()
	if _, err := s.restore(context.Background()); err == nil {
		t.Fatal("a download that does not match its SHA-256 was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "report.html")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a bad download was written: %v", err)
	}
	if !s.restorePending {
		t.Fatal("a failed restore is not retried")
	}

	plane.mu.Lock()
	plane.corruptDownloads = false
	plane.mu.Unlock()
	s.retryRestore(context.Background())
	if s.restorePending {
		t.Fatal("the retried restore did not finish")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "report.html")); string(got) != "<h1>report</h1>" {
		t.Fatalf("retried restore = %q", got)
	}
}

func TestArtifactRestoreWithoutArtifacts(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	plane.disabled = true
	s := testSyncer(c, t.TempDir())
	if restored, err := s.restore(context.Background()); err != nil || restored != 0 || s.restorePending {
		t.Fatalf("restore = %d, %v, pending %t", restored, err, s.restorePending)
	}
}

func TestArtifactRestoreLeavesOtherFilesAlone(t *testing.T) {
	_, c := newFakeArtifactPlane(t)
	first := t.TempDir()
	writeArtifact(t, first, "report.html", "<h1>report</h1>")
	syncSettled(testSyncer(c, first))

	// A folder named like a temporary one is an ordinary artifact folder.
	dir := t.TempDir()
	writeArtifact(t, dir, ".ao-restore/notes.md", "# keep me")
	s := testSyncer(c, dir)
	if restored, err := s.restore(context.Background()); err != nil || restored != 1 {
		t.Fatalf("restore = %d, %v", restored, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, ".ao-restore", "notes.md")); err != nil || string(got) != "# keep me" {
		t.Fatalf("restore changed an unrelated file: %q, %v", got, err)
	}
}

func TestArtifactRestoreRetryKeepsTheAgentsDeletions(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	first := t.TempDir()
	writeArtifact(t, first, "a.md", "# a")
	writeArtifact(t, first, "b.md", "# b")
	syncSettled(testSyncer(c, first))

	dir := t.TempDir()
	s := testSyncer(c, dir)
	plane.mu.Lock()
	plane.failDownload = "b.md"
	plane.mu.Unlock()
	if _, err := s.restore(context.Background()); err == nil {
		t.Fatal("a failed download was not reported")
	}
	// The agent deletes the restored a.md before the retry.
	if err := os.Remove(filepath.Join(dir, "a.md")); err != nil {
		t.Fatal(err)
	}
	plane.mu.Lock()
	plane.failDownload = ""
	plane.mu.Unlock()
	s.retryRestore(context.Background())
	if _, err := os.Stat(filepath.Join(dir, "a.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the retry brought back a deleted file: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "b.md")); string(got) != "# b" {
		t.Fatalf("the retry did not restore b.md: %q", got)
	}
	s.sync(context.Background())
	if got := plane.paths(); !slices.Equal(got, []string{"b.md"}) {
		t.Fatalf("the deletion did not sync: committed = %v", got)
	}
}

func TestArtifactRestoreCountsFilesStillOnDiskAsSynced(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	first := t.TempDir()
	writeArtifact(t, first, "same.md", "# same")
	writeArtifact(t, first, "edited.md", "# before")
	syncSettled(testSyncer(c, first))
	_, uploads, _ := plane.counts()

	// A resumed sandbox kept its disk: one file is as committed, one was edited.
	dir := t.TempDir()
	writeArtifact(t, dir, "same.md", "# same")
	writeArtifact(t, dir, "edited.md", "# after")
	s := testSyncer(c, dir)
	if restored, err := s.restore(context.Background()); err != nil || restored != 0 {
		t.Fatalf("restore = %d, %v", restored, err)
	}
	if len(s.synced) != 2 {
		t.Fatalf("files on disk were not counted as synced: %v", s.synced)
	}
	syncSettled(s)
	if _, u, _ := plane.counts(); u != uploads+1 {
		t.Fatalf("uploads = %d, want only the edited file (%d)", u, uploads+1)
	}
	// A kept file the agent deletes is deleted from the committed files.
	if err := os.Remove(filepath.Join(dir, "same.md")); err != nil {
		t.Fatal(err)
	}
	s.sync(context.Background())
	if got := plane.paths(); !slices.Equal(got, []string{"edited.md"}) {
		t.Fatalf("committed = %v", got)
	}
}

func TestArtifactScanKeepsCommittedBytesAtTheSessionLimit(t *testing.T) {
	_, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	s := testSyncer(c, dir)
	const mib = int64(1 << 20)
	sparse := func(path string, size int64) {
		t.Helper()
		file, err := os.OpenFile(filepath.Join(dir, path), os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(size); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
	}
	// Twenty 24 MiB files and one of 20 MiB are committed: 500 MiB, the limit.
	for index := range 20 {
		path := fmt.Sprintf("f%02d.bin", index)
		sparse(path, 24*mib)
		s.synced[path] = artifactState{size: 24 * mib, sha: "committed"}
	}
	sparse("last.bin", 20*mib)
	s.synced["last.bin"] = artifactState{size: 20 * mib, sha: "committed"}
	// The first file grows by 1 MiB, and a new file appears.
	sparse("f00.bin", 25*mib)
	writeArtifact(t, dir, "new.md", "# new")

	scan, err := s.scan()
	if err != nil {
		t.Fatal(err)
	}
	for _, refused := range []string{"f00.bin", "new.md"} {
		if _, ok := scan.current[refused]; ok {
			t.Errorf("%s was admitted over the 500 MiB limit", refused)
		}
	}
	// Every other committed file stays in the scan, so no commit leaves one out.
	for index := 1; index < 20; index++ {
		if _, ok := scan.current[fmt.Sprintf("f%02d.bin", index)]; !ok {
			t.Errorf("f%02d.bin was left out", index)
		}
	}
	if _, ok := scan.current["last.bin"]; !ok {
		t.Error("last.bin was left out")
	}
}

func TestArtifactSyncSeesARewriteThatKeepsSizeAndModTime(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "note.md", "AAAA")
	s := testSyncer(c, dir)
	syncSettled(s)
	info, err := os.Stat(filepath.Join(dir, "note.md"))
	if err != nil {
		t.Fatal(err)
	}

	writeArtifact(t, dir, "note.md", "BBBB")
	if err := os.Chtimes(filepath.Join(dir, "note.md"), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	syncSettled(s)
	plane.mu.Lock()
	committed := plane.rows["note.md"].sha
	plane.mu.Unlock()
	if want := sha256.Sum256([]byte("BBBB")); committed != hex.EncodeToString(want[:]) {
		t.Fatal("a rewrite that kept the size and modification time was not synced")
	}
}

// userDelete removes a committed file as the user's delete route does.
func (p *fakeArtifactPlane) userDelete(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.rows, path)
}

func TestArtifactSyncRemovesAFileTheUserDeleted(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "a.md", "# a")
	writeArtifact(t, dir, "b.md", "# b")
	s := testSyncer(c, dir)
	syncSettled(s)
	_, uploads, commits := plane.counts()

	plane.userDelete("a.md")
	s.reconcileCommitted(context.Background())
	if _, err := os.Stat(filepath.Join(dir, "a.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the deleted file is still on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.md")); err != nil {
		t.Fatalf("another file was removed: %v", err)
	}
	syncSettled(s)
	if _, u, c := plane.counts(); u != uploads || c != commits {
		t.Fatalf("the delete was synced back: uploads %d->%d commits %d->%d", uploads, u, commits, c)
	}
}

func TestArtifactSyncKeepsADeletedFileTheAgentChanged(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := t.TempDir()
	writeArtifact(t, dir, "a.md", "# a")
	s := testSyncer(c, dir)
	syncSettled(s)

	writeArtifact(t, dir, "a.md", "# a, edited after the delete")
	plane.userDelete("a.md")
	s.reconcileCommitted(context.Background())
	if got, _ := os.ReadFile(filepath.Join(dir, "a.md")); string(got) != "# a, edited after the delete" {
		t.Fatalf("the agent's change was removed: %q", got)
	}
	syncSettled(s)
	if got := plane.paths(); !slices.Equal(got, []string{"a.md"}) {
		t.Fatalf("the changed file was not synced as a new one: %v", got)
	}
}

func TestArtifactRestoreRemovesFilesDeletedWhileNoWorkerRan(t *testing.T) {
	plane, c := newFakeArtifactPlane(t)
	dir := filepath.Join(t.TempDir(), "session")
	writeArtifact(t, dir, "a.md", "# a")
	writeArtifact(t, dir, "b.md", "# b")
	syncSettled(testSyncer(c, dir))

	// The worker stops, the user deletes a.md, and a new worker starts on the kept disk.
	plane.userDelete("a.md")
	s := testSyncer(c, dir)
	if restored, err := s.restore(context.Background()); err != nil || restored != 0 {
		t.Fatalf("restore = %d, %v", restored, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a file deleted while no worker ran is still on disk: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "b.md")); string(got) != "# b" {
		t.Fatalf("b.md = %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("the synced list was written into the artifact directory: %v", entries)
	}
}

func TestWaitForWorkPokesTheSyncerWhenArtifactsChanged(t *testing.T) {
	answer := `{}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	c := &client{baseURL: server.URL, http: server.Client(), token: "worker-token"}
	calls := 0
	poke := func() { calls++ }
	c.artifactsChanged.Store(&poke)

	if err := c.WaitForWork(context.Background()); err != nil || calls != 0 {
		t.Fatalf("plain wake: calls=%d err=%v", calls, err)
	}
	answer = `{"artifactsChanged":true}`
	if err := c.WaitForWork(context.Background()); err != nil || calls != 1 {
		t.Fatalf("artifact wake: calls=%d err=%v", calls, err)
	}
}
