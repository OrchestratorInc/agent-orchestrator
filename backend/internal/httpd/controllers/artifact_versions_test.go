package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/artifactversions"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// fakeArtifactVersions holds versions in memory; RecordSave appends the file's
// current content as a user version.
type fakeArtifactVersions struct {
	dir      string
	versions []domain.SessionArtifactVersion // oldest first
	blobs    map[string]string               // sha256 -> content
	// snapshotErr fails Snapshot; afterSnapshot runs once it kept the content.
	snapshotErr   error
	afterSnapshot func()
}

func (f *fakeArtifactVersions) add(content string, source domain.ArtifactVersionSource) {
	sum := artifactSHA(content)
	f.blobs[sum] = content
	f.versions = append(f.versions, domain.SessionArtifactVersion{
		Path: "plan.md", Version: int64(len(f.versions) + 1), SHA256: sum, Size: int64(len(content)),
		Source: source, CreatedAt: time.Date(2026, 10, 15, 12, len(f.versions), 0, 0, time.UTC),
	})
}

func (f *fakeArtifactVersions) List(_ context.Context, _ domain.SessionID, relPath string) ([]domain.SessionArtifactVersion, error) {
	var out []domain.SessionArtifactVersion
	for i := len(f.versions) - 1; i >= 0; i-- {
		if f.versions[i].Path == relPath {
			out = append(out, f.versions[i])
		}
	}
	return out, nil
}

func (f *fakeArtifactVersions) ReadVersion(_ domain.SessionRecord, v domain.SessionArtifactVersion) ([]byte, error) {
	content, ok := f.blobs[v.SHA256]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(content), nil
}

func (f *fakeArtifactVersions) Sync(_ context.Context, _ domain.SessionRecord, relPath string) error {
	data, err := os.ReadFile(filepath.Join(f.dir, relPath))
	if err != nil {
		return err
	}
	if n := len(f.versions); n == 0 || f.blobs[f.versions[n-1].SHA256] != string(data) {
		f.add(string(data), domain.ArtifactVersionSourceAgent)
	}
	return nil
}

func (f *fakeArtifactVersions) Snapshot(ctx context.Context, sess domain.SessionRecord, relPath string) (string, error) {
	if f.snapshotErr != nil {
		return "", f.snapshotErr
	}
	if err := f.Sync(ctx, sess, relPath); err != nil {
		return "", err
	}
	if f.afterSnapshot != nil {
		f.afterSnapshot()
	}
	return f.versions[len(f.versions)-1].SHA256, nil
}

func (f *fakeArtifactVersions) RecordRestore(ctx context.Context, sess domain.SessionRecord, relPath string, from int64) error {
	if err := f.RecordSave(ctx, sess, relPath); err != nil {
		return err
	}
	f.versions[len(f.versions)-1].RestoredFrom = from
	return nil
}

func (f *fakeArtifactVersions) RecordSave(_ context.Context, _ domain.SessionRecord, relPath string) error {
	data, err := os.ReadFile(filepath.Join(f.dir, relPath))
	if err != nil {
		return err
	}
	f.add(string(data), domain.ArtifactVersionSourceUser)
	return nil
}

func (f *fakeArtifactVersions) DeleteVersions(_ context.Context, _ domain.SessionRecord, relPath string) error {
	kept := f.versions[:0]
	for _, v := range f.versions {
		if v.Path != relPath {
			kept = append(kept, v)
		}
	}
	f.versions = kept
	return nil
}

func artifactVersionServer(t *testing.T) (*httptest.Server, *fakeArtifactVersions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("v2 agent"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeArtifactVersions{dir: dir, blobs: map[string]string{}}
	fake.add("v1 agent", domain.ArtifactVersionSourceAgent)
	fake.add("v2 agent", domain.ArtifactVersionSourceAgent)

	svc := newFakeSessionService()
	s := svc.sessions["ao-1"]
	s.Metadata.ArtifactDir = dir
	s.ArtifactFiles = []domain.SessionArtifactFile{{Path: "plan.md", Name: "plan.md"}, {Path: "chart.png", Name: "chart.png"}, {Path: "style.css", Name: "style.css"}}
	svc.sessions["ao-1"] = s

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Sessions: svc, ArtifactVersions: fake}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv, fake, dir
}

func artifactVersionCall(t *testing.T, method, rawURL string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, rawURL, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func TestArtifactVersionsListNewestFirstAndReadOne(t *testing.T) {
	srv, _, _ := artifactVersionServer(t)
	base := srv.URL + "/api/v1/sessions/ao-1/artifact-versions"

	status, body := artifactVersionCall(t, http.MethodGet, base+"?path="+url.QueryEscape("plan.md"), nil)
	var list struct {
		Versions []struct {
			Version int64  `json:"version"`
			Source  string `json:"source"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(body, &list); err != nil || status != http.StatusOK {
		t.Fatalf("list: status=%d body=%s err=%v", status, body, err)
	}
	if len(list.Versions) != 2 || list.Versions[0].Version != 2 || list.Versions[1].Version != 1 || list.Versions[0].Source != "agent" {
		t.Fatalf("versions = %+v, want [2, 1]", list.Versions)
	}

	status, body = artifactVersionCall(t, http.MethodGet, base+"/1?path=plan.md", nil)
	var one struct {
		Version int64  `json:"version"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &one); err != nil || status != http.StatusOK || one.Version != 1 || one.Content != "v1 agent" {
		t.Fatalf("read: status=%d body=%s", status, body)
	}

	for name, target := range map[string]string{
		"unlisted file":   base + "?path=other.md",
		"unknown version": base + "/9?path=plan.md",
		"bad version":     base + "/zero?path=plan.md",
		"escaping path":   base + "?path=" + url.QueryEscape("../plan.md"),
	} {
		if status, _ := artifactVersionCall(t, http.MethodGet, target, nil); status != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", name, status)
		}
	}
}

func TestArtifactVersionRestoreWritesTheFileAndRecordsAUserVersion(t *testing.T) {
	srv, fake, dir := artifactVersionServer(t)
	restore := srv.URL + "/api/v1/sessions/ao-1/artifact-versions/restore"

	// The file moved on since the viewer last read it. A restore does not need
	// the viewer's fingerprint: it keeps what it replaces as a version first.
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("edited meanwhile"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, body := artifactVersionCall(t, http.MethodPost, restore, map[string]any{"path": "plan.md", "version": 1})
	if status != http.StatusOK {
		t.Fatalf("restore: status=%d body=%s", status, body)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "plan.md")); string(data) != "v1 agent" {
		t.Fatalf("file after restore = %q, want v1 agent", data)
	}
	last := fake.versions[len(fake.versions)-1]
	kept := fake.versions[len(fake.versions)-2]
	if len(fake.versions) != 4 || fake.blobs[kept.SHA256] != "edited meanwhile" || kept.Source != domain.ArtifactVersionSourceAgent {
		t.Fatalf("versions = %+v, want the replaced content kept as a version", fake.versions)
	}
	if last.Version != 4 || last.Source != domain.ArtifactVersionSourceUser || last.RestoredFrom != 1 {
		t.Fatalf("versions = %+v, want a last, user-written one that restored version 1", fake.versions)
	}

	status, _ = artifactVersionCall(t, http.MethodPost, restore, map[string]any{"path": "plan.md", "version": 7})
	if status != http.StatusNotFound {
		t.Fatalf("unknown version: status = %d, want 404", status)
	}
}

func TestArtifactVersionRestoreWritesNothingWhenTheCurrentContentCannotBeKept(t *testing.T) {
	for name, tc := range map[string]struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		"agent still writing": {artifactversions.ErrUnsettled, http.StatusConflict, "ARTIFACT_FILE_STALE"},
		"file too large":      {artifactversions.ErrTooLarge, http.StatusBadRequest, "ARTIFACT_FILE_TOO_LARGE"},
		"blob store failure":  {errors.New("disk full"), http.StatusInternalServerError, ""},
	} {
		t.Run(name, func(t *testing.T) {
			srv, fake, dir := artifactVersionServer(t)
			fake.snapshotErr = tc.err
			status, body := artifactVersionCall(t, http.MethodPost, srv.URL+"/api/v1/sessions/ao-1/artifact-versions/restore", map[string]any{"path": "plan.md", "version": 1})
			if status != tc.wantStatus || (tc.wantCode != "" && artifactErrorCode(t, body) != tc.wantCode) {
				t.Fatalf("restore: status=%d body=%s, want %d %s", status, body, tc.wantStatus, tc.wantCode)
			}
			if data, _ := os.ReadFile(filepath.Join(dir, "plan.md")); string(data) != "v2 agent" {
				t.Fatalf("file after refused restore = %q, want unchanged", data)
			}
			if len(fake.versions) != 2 {
				t.Fatalf("versions = %+v, want none added", fake.versions)
			}
		})
	}
}

func TestArtifactVersionRestoreRefusesAnAgentWriteThatRacedTheSnapshot(t *testing.T) {
	srv, fake, dir := artifactVersionServer(t)
	// The agent writes after the current content was kept but before the restore
	// writes. That edit has no copy yet, so the restore must not replace it.
	fake.afterSnapshot = func() {
		if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("agent raced"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	status, body := artifactVersionCall(t, http.MethodPost, srv.URL+"/api/v1/sessions/ao-1/artifact-versions/restore", map[string]any{"path": "plan.md", "version": 1})
	if status != http.StatusConflict || artifactErrorCode(t, body) != "ARTIFACT_FILE_STALE" {
		t.Fatalf("restore: status=%d body=%s, want 409 ARTIFACT_FILE_STALE", status, body)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "plan.md")); string(data) != "agent raced" {
		t.Fatalf("file after raced restore = %q, want the agent's edit kept", data)
	}
	if last := fake.versions[len(fake.versions)-1]; last.Source != domain.ArtifactVersionSourceAgent || last.RestoredFrom != 0 {
		t.Fatalf("versions = %+v, want no restore recorded", fake.versions)
	}
}

func TestArtifactVersionListOnlyReadsAndSyncRecordsTheEdit(t *testing.T) {
	srv, fake, dir := artifactVersionServer(t)
	base := srv.URL + "/api/v1/sessions/ao-1/artifact-versions"
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("v3 agent"), 0o600); err != nil {
		t.Fatal(err)
	}

	if status, body := artifactVersionCall(t, http.MethodGet, base+"?path=plan.md", nil); status != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", status, body)
	}
	if len(fake.versions) != 2 {
		t.Fatalf("versions after a list = %d, want 2: a GET must not record", len(fake.versions))
	}

	status, body := artifactVersionCall(t, http.MethodPost, base+"/sync", map[string]any{"path": "plan.md"})
	var list struct {
		Versions []struct {
			Version int64 `json:"version"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(body, &list); err != nil || status != http.StatusOK {
		t.Fatalf("sync: status=%d body=%s err=%v", status, body, err)
	}
	if len(fake.versions) != 3 || len(list.Versions) != 3 || list.Versions[0].Version != 3 {
		t.Fatalf("sync listed %+v, want the new edit recorded and listed first", list.Versions)
	}
	if status, _ := artifactVersionCall(t, http.MethodPost, base+"/sync", map[string]any{"path": "other.md"}); status != http.StatusNotFound {
		t.Fatalf("sync of an unlisted file: status = %d, want 404", status)
	}
}

func TestArtifactVersionReadTruncatesALongVersion(t *testing.T) {
	srv, fake, _ := artifactVersionServer(t)
	long := strings.Repeat("é", 200<<10)              // 400 KiB of two-byte characters
	fake.add(long, domain.ArtifactVersionSourceAgent) // version 3

	status, body := artifactVersionCall(t, http.MethodGet, srv.URL+"/api/v1/sessions/ao-1/artifact-versions/3?path=plan.md", nil)
	var one struct {
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
		Size      int64  `json:"size"`
	}
	if err := json.Unmarshal(body, &one); err != nil || status != http.StatusOK {
		t.Fatalf("read: status=%d err=%v", status, err)
	}
	if !one.Truncated || len(one.Content) != 256<<10 || !utf8.ValidString(one.Content) || one.Size != int64(len(long)) {
		t.Fatalf("read: truncated=%v len=%d size=%d, want the first 256 KiB on a character boundary", one.Truncated, len(one.Content), one.Size)
	}
}

func TestArtifactVersionsAreNotImplementedWithoutAService(t *testing.T) {
	srv, _ := artifactFileServer(t, map[string]string{"plan.md": "x"})
	status, _ := artifactVersionCall(t, http.MethodGet, srv.URL+"/api/v1/sessions/ao-1/artifact-versions?path=plan.md", nil)
	if status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", status)
	}
}

func artifactErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var out struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	return out.Code
}

func TestArtifactSaveRecordsAUserVersion(t *testing.T) {
	srv, fake, dir := artifactVersionServer(t)

	status, body := artifactVersionCall(t, http.MethodPut, srv.URL+"/api/v1/sessions/ao-1/artifact-files", map[string]any{
		"path": "plan.md", "content": "v3 by hand", "expectedFileFingerprint": artifactSHA("v2 agent"),
	})
	if status != http.StatusOK {
		t.Fatalf("save: status=%d body=%s", status, body)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "plan.md")); string(data) != "v3 by hand" {
		t.Fatalf("file after save = %q", data)
	}
	last := fake.versions[len(fake.versions)-1]
	if len(fake.versions) != 3 || last.Source != domain.ArtifactVersionSourceUser || fake.blobs[last.SHA256] != "v3 by hand" {
		t.Fatalf("versions = %+v, want a third user version holding the saved text", fake.versions)
	}
}

func TestArtifactVersionReadRefusesABinaryVersionAndRestoreWritesIt(t *testing.T) {
	srv, fake, dir := artifactVersionServer(t)
	base := srv.URL + "/api/v1/sessions/ao-1/artifact-versions"
	restore := base + "/restore"
	binary := "\xff\xfe not text"
	fake.add(binary, domain.ArtifactVersionSourceAgent) // version 3
	fake.add("v4 agent", domain.ArtifactVersionSourceAgent)
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("v4 agent"), 0o600); err != nil {
		t.Fatal(err)
	}
	lostBlob := fake.versions[0].SHA256
	delete(fake.blobs, lostBlob) // version 1's content is gone

	status, body := artifactVersionCall(t, http.MethodGet, base+"/3?path=plan.md", nil)
	if status != http.StatusBadRequest || artifactErrorCode(t, body) != "ARTIFACT_FILE_BINARY" {
		t.Errorf("read binary: status=%d body=%s, want 400 ARTIFACT_FILE_BINARY", status, body)
	}
	status, body = artifactVersionCall(t, http.MethodGet, base+"/1?path=plan.md", nil)
	if status != http.StatusNotFound || artifactErrorCode(t, body) != "ARTIFACT_VERSION_NOT_FOUND" {
		t.Errorf("read lost blob: status=%d body=%s, want 404 ARTIFACT_VERSION_NOT_FOUND", status, body)
	}
	status, _ = artifactVersionCall(t, http.MethodPost, restore, map[string]any{"path": "plan.md", "version": 1})
	if status != http.StatusNotFound {
		t.Errorf("restore lost blob: status=%d, want 404", status)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "plan.md")); string(data) != "v4 agent" {
		t.Fatalf("file after a refused restore = %q, want unchanged", data)
	}
	status, body = artifactVersionCall(t, http.MethodPost, restore, map[string]any{"path": "plan.md", "version": 3})
	if status != http.StatusOK {
		t.Fatalf("restore binary: status=%d body=%s", status, body)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "plan.md")); string(data) != binary {
		t.Fatalf("file after restore = %q, want the binary version", data)
	}
	if last := fake.versions[len(fake.versions)-1]; last.Source != domain.ArtifactVersionSourceUser || last.RestoredFrom != 3 {
		t.Fatalf("newest version = %+v, want a user restore of version 3", last)
	}
}

// mediaVersionServer lists one PNG with two real versions.
func mediaVersionServer(t *testing.T) (*httptest.Server, []byte, []byte) {
	t.Helper()
	srv, fake, dir := artifactVersionServer(t)
	older := append([]byte("\x89PNG\r\n\x1a\n\x00older"), bytes.Repeat([]byte{0xff}, 64)...)
	newer := append([]byte("\x89PNG\r\n\x1a\n\x00newer"), bytes.Repeat([]byte{0xfe}, 64)...)
	if err := os.WriteFile(filepath.Join(dir, "chart.png"), newer, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, content := range [][]byte{older, newer} {
		fake.add(string(content), domain.ArtifactVersionSourceAgent)
		fake.versions[len(fake.versions)-1].Path = "chart.png"
	}
	return srv, older, newer
}

func TestPreviewFileServesAKeptVersionOfAnArtifact(t *testing.T) {
	srv, older, newer := mediaVersionServer(t)
	base := srv.URL + "/api/v1/sessions/ao-1/preview/files/chart.png?source=artifact&raw=true"

	resp, err := http.Get(base + "&artifactVersion=3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, older) {
		t.Fatalf("version 3: status=%d body=%q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("content type = %q", ct)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("a version must stay sandboxed: %v", resp.Header)
	}

	req, _ := http.NewRequest(http.MethodGet, base+"&artifactVersion=4", nil)
	req.Header.Set("Range", "bytes=8-12")
	ranged, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ranged.Body.Close() }()
	part, _ := io.ReadAll(ranged.Body)
	if ranged.StatusCode != http.StatusPartialContent || !bytes.Equal(part, newer[8:13]) {
		t.Fatalf("range: status=%d body=%q, want 206 for a seek in a video", ranged.StatusCode, part)
	}

	cached, _ := http.NewRequest(http.MethodGet, base+"&artifactVersion=3", nil)
	cached.Header.Set("If-None-Match", resp.Header.Get("ETag"))
	unchanged, err := http.DefaultClient.Do(cached)
	if err != nil {
		t.Fatal(err)
	}
	_ = unchanged.Body.Close()
	if unchanged.StatusCode != http.StatusNotModified {
		t.Fatalf("revalidation: status=%d, want 304", unchanged.StatusCode)
	}
}

func TestPreviewFileRefusesAVersionItCannotName(t *testing.T) {
	srv, _, _ := mediaVersionServer(t)
	base := srv.URL + "/api/v1/sessions/ao-1/preview/files/"
	for name, target := range map[string]string{
		"unknown version": base + "chart.png?source=artifact&artifactVersion=99",
		"another file's":  base + "chart.png?source=artifact&artifactVersion=1",
		"not a number":    base + "chart.png?source=artifact&artifactVersion=first",
		"zero":            base + "chart.png?source=artifact&artifactVersion=0",
		"unlisted file":   base + "secret.png?source=artifact&artifactVersion=3",
	} {
		status, body := artifactVersionCall(t, http.MethodGet, target, nil)
		if status != http.StatusNotFound || artifactErrorCode(t, body) != "ARTIFACT_VERSION_NOT_FOUND" {
			t.Errorf("%s: status=%d body=%s", name, status, body)
		}
	}
}

func TestArtifactVersionListSaysItServesMediaVersions(t *testing.T) {
	srv, _, _ := mediaVersionServer(t)
	status, body := artifactVersionCall(t, http.MethodGet, srv.URL+"/api/v1/sessions/ao-1/artifact-versions?path=chart.png", nil)
	var list struct {
		MediaVersions bool `json:"mediaVersions"`
	}
	if err := json.Unmarshal(body, &list); err != nil || status != http.StatusOK || !list.MediaVersions {
		t.Fatalf("status=%d body=%s err=%v, want mediaVersions", status, body, err)
	}
}

// A page's own cache-busting query must not be taken for a kept version.
func TestPreviewFileLeavesAPagesVersionQueryAlone(t *testing.T) {
	srv, _, dir := artifactVersionServer(t)
	if err := os.WriteFile(filepath.Join(dir, "style.css"), []byte("body{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, body := artifactVersionCall(t, http.MethodGet, srv.URL+"/api/v1/sessions/ao-1/preview/files/style.css?source=artifact&version=2", nil)
	if status != http.StatusOK || string(body) != "body{}" {
		t.Fatalf("status=%d body=%s, want the live file", status, body)
	}
}

func TestArtifactOriginServesAKeptVersion(t *testing.T) {
	srv, older, newer := mediaVersionServer(t)
	origin, err := previewutil.ArtifactFileURL("http://127.0.0.1", "ao-1", "chart.png")
	if err != nil {
		t.Fatal(err)
	}
	body, status, _ := doPreviewOriginRequest(t, srv, origin, "/chart.png?artifactVersion=3")
	if status != http.StatusOK || !bytes.Equal(body, older) {
		t.Fatalf("version 3: status=%d body=%q", status, body)
	}
	body, status, _ = doPreviewOriginRequest(t, srv, origin, "/chart.png?version=3")
	if status != http.StatusOK || !bytes.Equal(body, newer) {
		t.Fatalf("a page's version query: status=%d body=%q, want the live file", status, body)
	}
	if _, status, _ = doPreviewOriginRequest(t, srv, origin, "/chart.png?artifactVersion=99"); status != http.StatusNotFound {
		t.Fatalf("unknown version: status=%d, want 404, never the live file", status)
	}
}

// Restore is not limited to the types the viewer offers it for: every binary
// comes back byte for byte, through the real capture and blob store.
func TestArtifactVersionRestoreIsByteExactForAnyBinaryType(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store := sqlitetest.MustOpenAt(t, filepath.Join(dataDir, "db"))
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "ao", Path: dataDir, RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "ao", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := sessionartifacts.Dir(dataDir, rec.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	capturer := artifactversions.New(store, dataDir, nil)
	mtime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	write := func(name string, content []byte) {
		t.Helper()
		target := filepath.Join(dir, name)
		if err := os.WriteFile(target, content, 0o600); err != nil {
			t.Fatal(err)
		}
		mtime = mtime.Add(time.Second)
		if err := os.Chtimes(target, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	capture := func() {
		t.Helper()
		for range 2 {
			if err := capturer.CaptureSession(ctx, rec); err != nil {
				t.Fatal(err)
			}
		}
	}
	names := []string{"bundle.zip", "report.docx", "sheet.xlsx", "data.sqlite", "tool.bin"}
	older := func(name string) []byte { return append([]byte("\x00\xff\xfeolder "+name), 0x80, 0x00) }
	for _, name := range names {
		write(name, older(name))
	}
	capture()
	for _, name := range names {
		write(name, append(older(name), []byte("newer")...))
	}
	capture()
	files, err := sessionartifacts.List(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	svc := newFakeSessionService()
	s := svc.sessions["ao-1"]
	s.ID = rec.ID
	s.Metadata.ArtifactDir = dir
	s.ArtifactFiles = files
	svc.sessions[rec.ID] = s
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Sessions: svc, ArtifactVersions: capturer}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	for _, name := range names {
		status, body := artifactVersionCall(t, http.MethodPost, srv.URL+"/api/v1/sessions/"+string(rec.ID)+"/artifact-versions/restore", map[string]any{"path": name, "version": 1})
		if status != http.StatusOK {
			t.Fatalf("%s: restore status=%d body=%s", name, status, body)
		}
		if data, _ := os.ReadFile(filepath.Join(dir, name)); !bytes.Equal(data, older(name)) {
			t.Fatalf("%s: restored %q, want %q", name, data, older(name))
		}
		kept, err := store.ListSessionArtifactVersions(ctx, rec.ID, name)
		if err != nil || len(kept) != 3 || kept[0].RestoredFrom != 1 {
			t.Fatalf("%s: versions = %+v err=%v, want a restore of version 1 on top", name, kept, err)
		}
	}
}
