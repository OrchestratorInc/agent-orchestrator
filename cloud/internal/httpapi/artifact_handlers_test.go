package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/artifactstore"
	"github.com/aoagents/agent-orchestrator/cloud/internal/artifactstore/s3fake"
	"github.com/aoagents/agent-orchestrator/cloud/internal/auth"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

const (
	artifactOrgA     = "a1a1a1a1-0000-4000-8000-000000000001"
	artifactOrgB     = "b2b2b2b2-0000-4000-8000-000000000002"
	artifactSessionA = "a1a1a1a1-0000-4000-8000-0000000000a1"
	artifactSessionB = "b2b2b2b2-0000-4000-8000-0000000000b2"
	artifactUserA    = "a1a1a1a1-0000-4000-8000-0000000000f1"
	artifactUserB    = "b2b2b2b2-0000-4000-8000-0000000000f2"
	artifactTokenA   = "ao_local_user_a"
	artifactTokenB   = "ao_local_user_b"
	artifactEpoch    = int64(5)
)

// memoryArtifactRows is an in-memory sessionArtifactStore that applies the
// same organization scoping as the Postgres store.
type memoryArtifactRows struct {
	mu       sync.Mutex
	sessions map[string]string
	members  map[string]string
	rows     map[string]map[string]domain.SessionArtifact
	// pending holds open uploads by S3 key and version.
	pending map[domain.ArtifactObjectVersion]pendingUpload
	// replaced holds versions a commit replaced or deleted.
	replaced []domain.ArtifactObjectVersion
	commits  int
}

type pendingUpload struct {
	sessionID string
	size      int64
}

func newMemoryArtifactRows() *memoryArtifactRows {
	return &memoryArtifactRows{
		sessions: map[string]string{artifactSessionA: artifactOrgA, artifactSessionB: artifactOrgB},
		members:  map[string]string{artifactUserA: artifactOrgA, artifactUserB: artifactOrgB},
		rows:     map[string]map[string]domain.SessionArtifact{},
		pending:  map[domain.ArtifactObjectVersion]pendingUpload{},
	}
}

func (m *memoryArtifactRows) pendingStats(sessionID string) (int, int64) {
	count, bytes := 0, int64(0)
	for _, upload := range m.pending {
		if upload.sessionID == sessionID {
			count++
			bytes += upload.size
		}
	}
	return count, bytes
}

func (m *memoryArtifactRows) ArtifactUploadAllowed(_ context.Context, orgID, sessionID string, size int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count, bytes := m.pendingStats(sessionID)
	return count+1 <= domain.MaxPendingArtifactUploads && bytes+size <= domain.MaxPendingArtifactBytes, nil
}

func (m *memoryArtifactRows) RecordArtifactUpload(_ context.Context, orgID, sessionID, s3Key, versionID string, size int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[sessionID] != orgID {
		return postgres.ErrNotFound
	}
	count, bytes := m.pendingStats(sessionID)
	if count+1 > domain.MaxPendingArtifactUploads || bytes+size > domain.MaxPendingArtifactBytes {
		return postgres.ErrArtifactLimit
	}
	m.pending[domain.ArtifactObjectVersion{S3Key: s3Key, VersionID: versionID}] = pendingUpload{sessionID: sessionID, size: size}
	return nil
}

func (m *memoryArtifactRows) RequireSessionArtifactAccess(_ context.Context, principal domain.Principal, orgID, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.members[principal.UserID] != orgID {
		return postgres.ErrForbidden
	}
	if m.sessions[sessionID] != orgID {
		return postgres.ErrNotFound
	}
	return nil
}

func (m *memoryArtifactRows) SessionArtifactCounts(_ context.Context, orgID string, sessionIDs []string) (map[string]domain.SessionArtifactSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]domain.SessionArtifactSummary{}
	for _, sessionID := range sessionIDs {
		if m.sessions[sessionID] == orgID && len(m.rows[sessionID]) > 0 {
			summary := domain.SessionArtifactSummary{Count: len(m.rows[sessionID])}
			for _, row := range m.rows[sessionID] {
				if row.UpdatedAt.After(summary.UpdatedAt) {
					summary.UpdatedAt = row.UpdatedAt
				}
			}
			out[sessionID] = summary
		}
	}
	return out, nil
}

func (m *memoryArtifactRows) ChangedSessionArtifacts(_ context.Context, orgID, sessionID string, manifest []domain.ArtifactManifestEntry) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[sessionID] != orgID {
		return nil, postgres.ErrNotFound
	}
	changed := []string{}
	for _, entry := range manifest {
		row, ok := m.rows[sessionID][entry.Path]
		if !ok || row.Size != entry.Size || row.SHA256 != entry.SHA256 {
			changed = append(changed, entry.Path)
		}
	}
	return changed, nil
}

func (m *memoryArtifactRows) CommitSessionArtifacts(_ context.Context, orgID, sessionID, _ string, workerEpoch int64, upserts []domain.SessionArtifact, deletes []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(upserts) == 0 && len(deletes) == 0 {
		return nil
	}
	if m.sessions[sessionID] != orgID {
		return postgres.ErrNotFound
	}
	if workerEpoch != artifactEpoch {
		return postgres.ErrStaleWorker
	}
	for _, row := range upserts {
		upload, ok := m.pending[domain.ArtifactObjectVersion{S3Key: row.S3Key, VersionID: row.VersionID}]
		if !ok || upload.sessionID != sessionID {
			return postgres.ErrArtifactVersionUnknown
		}
	}
	next := map[string]domain.SessionArtifact{}
	for path, row := range m.rows[sessionID] {
		next[path] = row
	}
	for _, path := range deletes {
		delete(next, path)
	}
	for _, row := range upserts {
		next[row.Path] = row
	}
	var total int64
	for _, row := range next {
		total += row.Size
	}
	if len(next) > domain.MaxSessionArtifactFiles || total > domain.MaxSessionArtifactBytes {
		return postgres.ErrArtifactLimit
	}
	for path, old := range m.rows[sessionID] {
		if row, ok := next[path]; !ok || row.VersionID != old.VersionID {
			m.replaced = append(m.replaced, domain.ArtifactObjectVersion{S3Key: old.S3Key, VersionID: old.VersionID})
		}
	}
	for _, row := range upserts {
		delete(m.pending, domain.ArtifactObjectVersion{S3Key: row.S3Key, VersionID: row.VersionID})
	}
	m.rows[sessionID] = next
	m.commits++
	return nil
}

func (m *memoryArtifactRows) list(sessionID string) []domain.SessionArtifact {
	items := make([]domain.SessionArtifact, 0, len(m.rows[sessionID]))
	for _, row := range m.rows[sessionID] {
		items = append(items, row)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items
}

func (m *memoryArtifactRows) ListSessionArtifacts(_ context.Context, principal domain.Principal, orgID, sessionID string) ([]domain.SessionArtifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.members[principal.UserID] != orgID {
		return nil, postgres.ErrForbidden
	}
	if m.sessions[sessionID] != orgID {
		return nil, postgres.ErrNotFound
	}
	return m.list(sessionID), nil
}

func (m *memoryArtifactRows) SessionArtifactsBySessions(_ context.Context, orgID string, sessionIDs []string) (map[string][]domain.SessionArtifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]domain.SessionArtifact{}
	for _, sessionID := range sessionIDs {
		if m.sessions[sessionID] == orgID {
			out[sessionID] = m.list(sessionID)
		}
	}
	return out, nil
}

func (m *memoryArtifactRows) SessionArtifact(_ context.Context, userID, orgID, sessionID, path string) (domain.SessionArtifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[sessionID][path]
	if m.sessions[sessionID] != orgID || m.members[userID] != orgID || !ok {
		return domain.SessionArtifact{}, postgres.ErrNotFound
	}
	return row, nil
}

func (m *memoryArtifactRows) DeleteSessionArtifact(_ context.Context, principal domain.Principal, orgID, sessionID, path string) (domain.ArtifactObjectVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.members[principal.UserID] != orgID {
		return domain.ArtifactObjectVersion{}, postgres.ErrForbidden
	}
	row, ok := m.rows[sessionID][path]
	if m.sessions[sessionID] != orgID || !ok {
		return domain.ArtifactObjectVersion{}, postgres.ErrNotFound
	}
	delete(m.rows[sessionID], path)
	version := domain.ArtifactObjectVersion{S3Key: row.S3Key, VersionID: row.VersionID}
	m.replaced = append(m.replaced, version)
	return version, nil
}

// artifactRouterStore answers the auth, worker-fencing and session calls the
// artifact routes make. Any other Store call panics on the nil embedded Store.
type artifactRouterStore struct {
	Store
	*memoryArtifactRows
	resumeCalls int
}

func (s *artifactRouterStore) PrincipalFromLocalToken(_ context.Context, hash []byte) (domain.Principal, error) {
	switch {
	case bytes.Equal(hash, auth.HashToken(artifactTokenA)):
		return domain.Principal{UserID: artifactUserA, Provider: "local"}, nil
	case bytes.Equal(hash, auth.HashToken(artifactTokenB)):
		return domain.Principal{UserID: artifactUserB, Provider: "local"}, nil
	}
	return domain.Principal{}, postgres.ErrNotFound
}

func (s *artifactRouterStore) WorkerConnectionCurrent(_ context.Context, _, _, _ string, epoch int64) (bool, error) {
	return epoch == artifactEpoch, nil
}

func (s *artifactRouterStore) ResumeSession(context.Context, domain.Principal, string, string) (domain.SandboxLifecycle, error) {
	s.resumeCalls++
	return domain.SandboxLifecycle{}, nil
}

func (s *artifactRouterStore) ListMemberships(_ context.Context, principal domain.Principal) ([]domain.Membership, error) {
	return []domain.Membership{{OrgID: s.members[principal.UserID], Role: "owner"}}, nil
}

func (s *artifactRouterStore) GetSession(_ context.Context, _ domain.Principal, orgID, sessionID string) (domain.Session, error) {
	return domain.Session{ID: sessionID, OrgID: orgID, Kind: "worker", OutputType: domain.SessionOutputArtifact}, nil
}

func (s *artifactRouterStore) WorkerLaunchSpec(_ context.Context, orgID, sessionID string) (domain.WorkerLaunch, error) {
	return domain.WorkerLaunch{OrgID: orgID, SessionID: sessionID, Kind: "worker", RequestedOutput: domain.RequestedOutputArtifact}, nil
}

func (s *artifactRouterStore) PRFactsBySession(context.Context, string, []string) (map[string][]contract.PRFacts, error) {
	return nil, nil
}

func (s *artifactRouterStore) PullRequestsBySessions(context.Context, string, []string) (map[string][]domain.PullRequest, error) {
	return nil, nil
}

type artifactHarness struct {
	t      *testing.T
	server *Server
	store  *artifactRouterStore
	fake   *s3fake.Server
	tokens *worker.TokenManager
}

func newArtifactHarness(t *testing.T, enabled bool) *artifactHarness {
	t.Helper()
	fake := s3fake.New(t)
	store := &artifactRouterStore{memoryArtifactRows: newMemoryArtifactRows()}
	tokens := worker.NewTokenManager([]byte(strings.Repeat("k", 32)))
	options := Options{
		Store:            store,
		LocalAuthEnabled: true,
		WorkerTokens:     tokens,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if enabled {
		artifacts, err := artifactstore.New(context.Background(), fake.Config())
		if err != nil {
			t.Fatal(err)
		}
		options.Artifacts = artifacts
		options.ArtifactAccessSecret = bytes.Repeat([]byte{7}, 32)
	}
	return &artifactHarness{t: t, server: New(options), store: store, fake: fake, tokens: tokens}
}

func (h *artifactHarness) workerToken(orgID, sessionID string, epoch int64) string {
	h.t.Helper()
	token, err := h.tokens.Issue(worker.Claims{
		OrgID: orgID, SessionID: sessionID, WorkerID: "w", Epoch: epoch, Scopes: []string{"worker:connect"},
	}, time.Hour)
	if err != nil {
		h.t.Fatal(err)
	}
	return "Worker " + token
}

func (h *artifactHarness) do(method, target, authorization string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, target, body)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, req)
	return rec
}

func (h *artifactHarness) workerJSON(method, target, authorization string, value any) *httptest.ResponseRecorder {
	h.t.Helper()
	body, _ := json.Marshal(value)
	return h.do(method, target, authorization, bytes.NewReader(body), map[string]string{"Content-Type": "application/json"})
}

func (h *artifactHarness) upload(authorization, path string, body []byte, sha string) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.do(http.MethodPut, "/api/cloud/v1/worker/artifacts/blob/"+path, authorization, bytes.NewReader(body),
		map[string]string{worker.ArtifactSHA256Header: sha})
}

func hexSHA(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(rec.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return value
}

// syncArtifacts uploads and commits files for session A as its current worker.
func (h *artifactHarness) syncArtifacts(files map[string][]byte) {
	h.t.Helper()
	authorization := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)
	commit := worker.ArtifactCommitRequest{}
	for path, body := range files {
		rec := h.upload(authorization, path, body, hexSHA(body))
		if rec.Code != http.StatusOK {
			h.t.Fatalf("upload %s: %d %s", path, rec.Code, rec.Body.String())
		}
		blob := decodeBody[worker.ArtifactBlobResponse](h.t, rec)
		commit.Files = append(commit.Files, worker.ArtifactCommitFile{
			Path: blob.Path, VersionID: blob.VersionID, Size: int64(len(body)), SHA256: hexSHA(body),
			MTime: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC),
		})
	}
	if rec := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", authorization, commit); rec.Code != http.StatusNoContent {
		h.t.Fatalf("commit: %d %s", rec.Code, rec.Body.String())
	}
}

func (h *artifactHarness) accessPrefix(token, orgID, sessionID string) string {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/cloud/v1/orgs/"+orgID+"/sessions/"+sessionID+"/artifacts/access", "Bearer "+token, nil, nil)
	if rec.Code != http.StatusOK {
		h.t.Fatalf("access: %d %s", rec.Code, rec.Body.String())
	}
	access := decodeBody[artifactAccessResponse](h.t, rec)
	if !strings.HasPrefix(access.FilesPrefix, "/api/cloud/v1/artifact-files/") || !strings.HasSuffix(access.FilesPrefix, "/") {
		h.t.Fatalf("files prefix = %q", access.FilesPrefix)
	}
	return access.FilesPrefix
}

func TestArtifactRoutesAreOffWithoutTheFlag(t *testing.T) {
	h := newArtifactHarness(t, false)
	workerAuth := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)
	for _, rec := range []*httptest.ResponseRecorder{
		h.do(http.MethodGet, "/api/cloud/v1/orgs/"+artifactOrgA+"/sessions/"+artifactSessionA+"/artifacts", "Bearer "+artifactTokenA, nil, nil),
		h.do(http.MethodPost, "/api/cloud/v1/orgs/"+artifactOrgA+"/sessions/"+artifactSessionA+"/artifacts/access", "Bearer "+artifactTokenA, nil, nil),
		h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/manifest", workerAuth, worker.ArtifactManifestRequest{}),
		h.upload(workerAuth, "report.html", []byte("x"), hexSHA([]byte("x"))),
		h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", workerAuth, worker.ArtifactCommitRequest{}),
		h.do(http.MethodGet, "/api/cloud/v1/artifact-files/token/report.html", "", nil, nil),
	} {
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 404 with artifacts off; body=%s", rec.Code, rec.Body.String())
		}
	}
	if h.server.artifactsEnabled() {
		t.Fatal("artifacts enabled without Options.Artifacts")
	}
	if h.fake.Puts() != 0 {
		t.Fatal("a blob reached S3 with artifacts off")
	}
	body := getMe(t, h.server, domain.Principal{UserID: artifactUserA})
	if body.Offering.Artifacts {
		t.Fatal("/me reports artifacts with the flag off")
	}
}

func TestArtifactSyncListAndServe(t *testing.T) {
	h := newArtifactHarness(t, true)
	html := []byte(`<html><img src="assets/chart.png"></html>`)
	png := []byte("\x89PNG fake")
	workerAuth := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)

	manifest := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/manifest", workerAuth, worker.ArtifactManifestRequest{
		Files: []worker.ArtifactManifestFile{{Path: "report.html", Size: int64(len(html)), SHA256: hexSHA(html)}},
	})
	if manifest.Code != http.StatusOK || !slices.Equal(decodeBody[worker.ArtifactManifestResponse](t, manifest).Changed, []string{"report.html"}) {
		t.Fatalf("first manifest: %d %s", manifest.Code, manifest.Body.String())
	}

	h.syncArtifacts(map[string][]byte{"report.html": html, "assets/chart.png": png})

	manifest = h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/manifest", workerAuth, worker.ArtifactManifestRequest{
		Files: []worker.ArtifactManifestFile{
			{Path: "report.html", Size: int64(len(html)), SHA256: hexSHA(html)},
			{Path: "assets/chart.png", Size: int64(len(png)), SHA256: hexSHA(png)},
		},
	})
	if changed := decodeBody[worker.ArtifactManifestResponse](t, manifest).Changed; len(changed) != 0 {
		t.Fatalf("unchanged files reported as changed: %v", changed)
	}

	list := h.do(http.MethodGet, "/api/cloud/v1/orgs/"+artifactOrgA+"/sessions/"+artifactSessionA+"/artifacts", "Bearer "+artifactTokenA, nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	listed := decodeBody[struct {
		Artifacts []sessionArtifactResponse `json:"artifacts"`
	}](t, list).Artifacts
	if len(listed) != 2 || listed[1].Path != "report.html" || listed[1].Kind != "html" || listed[1].SHA256 != hexSHA(html) {
		t.Fatalf("listed = %+v", listed)
	}
	if strings.Contains(list.Body.String(), "orgs/") || strings.Contains(list.Body.String(), "versionId") {
		t.Fatalf("list exposes storage keys: %s", list.Body.String())
	}
	if h.store.resumeCalls != 0 {
		t.Fatal("listing artifacts resumed the session")
	}

	prefix := h.accessPrefix(artifactTokenA, artifactOrgA, artifactSessionA)
	page := h.do(http.MethodGet, prefix+"report.html", "", nil, nil)
	if page.Code != http.StatusOK || page.Body.String() != string(html) {
		t.Fatalf("serve html: %d %q", page.Code, page.Body.String())
	}
	headers := page.Header()
	if headers.Get("Content-Type") != "text/html; charset=utf-8" ||
		headers.Get("X-Content-Type-Options") != "nosniff" ||
		headers.Get("ETag") != `"`+hexSHA(html)+`"` ||
		!strings.HasPrefix(headers.Get("Content-Security-Policy"), "sandbox ") ||
		strings.Contains(headers.Get("Content-Security-Policy"), "allow-same-origin") ||
		strings.Contains(headers.Get("Content-Security-Policy"), "allow-popups") ||
		headers.Get("Content-Length") != strconv.Itoa(len(html)) {
		t.Fatalf("headers = %v", headers)
	}
	asset := h.do(http.MethodGet, prefix+"assets/chart.png", "", nil, nil)
	if asset.Code != http.StatusOK || asset.Header().Get("Content-Type") != "image/png" || asset.Body.String() != string(png) {
		t.Fatalf("serve relative asset: %d %v", asset.Code, asset.Header())
	}
	notModified := h.do(http.MethodGet, prefix+"report.html", "", nil, map[string]string{"If-None-Match": `"` + hexSHA(html) + `"`})
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match status = %d", notModified.Code)
	}

	session := h.do(http.MethodGet, "/api/cloud/v1/orgs/"+artifactOrgA+"/sessions/"+artifactSessionA, "Bearer "+artifactTokenA, nil, nil)
	projected := decodeBody[struct {
		Session sessionResponse `json:"session"`
	}](t, session).Session
	if projected.OutputType != domain.SessionOutputArtifact || len(projected.ArtifactFiles) != 2 ||
		projected.ArtifactCount == nil || *projected.ArtifactCount != 2 {
		t.Fatalf("session projection = %+v", projected)
	}

	if body := getMe(t, h.server, domain.Principal{UserID: artifactUserA}); !body.Offering.Artifacts {
		t.Fatal("/me does not report artifacts with the flag on")
	}
}

func TestArtifactFileServesCommittedVersionOnly(t *testing.T) {
	h := newArtifactHarness(t, true)
	h.syncArtifacts(map[string][]byte{"report.html": []byte("committed")})
	workerAuth := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)
	// A newer upload that is never committed becomes the latest S3 version.
	uncommitted := []byte("uncommitted")
	if rec := h.upload(workerAuth, "report.html", uncommitted, hexSHA(uncommitted)); rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	prefix := h.accessPrefix(artifactTokenA, artifactOrgA, artifactSessionA)
	if rec := h.do(http.MethodGet, prefix+"report.html", "", nil, nil); rec.Body.String() != "committed" {
		t.Fatalf("served %q, want the committed version", rec.Body.String())
	}
}

func TestArtifactFileMissingObjectIsUnavailable(t *testing.T) {
	h := newArtifactHarness(t, true)
	h.syncArtifacts(map[string][]byte{"report.html": []byte("x")})
	h.fake.Remove("orgs/" + artifactOrgA + "/sessions/" + artifactSessionA + "/report.html")
	prefix := h.accessPrefix(artifactTokenA, artifactOrgA, artifactSessionA)
	rec := h.do(http.MethodGet, prefix+"report.html", "", nil, nil)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "artifact_unavailable") {
		t.Fatalf("missing object: %d %s", rec.Code, rec.Body.String())
	}
}

func TestArtifactFileRejectsBadTokensAndPaths(t *testing.T) {
	h := newArtifactHarness(t, true)
	h.syncArtifacts(map[string][]byte{"report.html": []byte("x")})
	prefix := h.accessPrefix(artifactTokenA, artifactOrgA, artifactSessionA)
	token := strings.TrimSuffix(strings.TrimPrefix(prefix, "/api/cloud/v1/artifact-files/"), "/")

	forged := token[:len(token)-2] + "AA"
	if rec := h.do(http.MethodGet, "/api/cloud/v1/artifact-files/"+forged+"/report.html", "", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("forged token status = %d", rec.Code)
	}
	expired := h.server.signArtifactAccess(artifactAccessClaims{OrgID: artifactOrgA, SessionID: artifactSessionA, Expires: time.Now().Add(-time.Second).Unix()})
	framed := h.do(http.MethodGet, "/api/cloud/v1/artifact-files/"+expired+"/report.html", "", nil, map[string]string{"Sec-Fetch-Dest": "iframe"})
	if framed.Code != http.StatusUnauthorized || !strings.HasPrefix(framed.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(framed.Body.String(), "link has expired") || !strings.HasPrefix(framed.Header().Get("Content-Security-Policy"), "sandbox;") {
		t.Fatalf("expired link in a frame: %d %v %q", framed.Code, framed.Header(), framed.Body.String())
	}
	if rec := h.do(http.MethodGet, "/api/cloud/v1/artifact-files/"+expired+"/report.html", "", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired token status = %d", rec.Code)
	}
	for _, path := range []string{
		"..%2F..%2Fetc%2Fpasswd", "../report.html", "%2E%2E/report.html", "a//report.html",
		"%2Freport.html", "..%5Creport.html", ".versions/report.html", "missing.html",
	} {
		rec := h.do(http.MethodGet, prefix+path, "", nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("path %q status = %d, want 404", path, rec.Code)
		}
	}
}

// Organization B can never read organization A's session or its files.
func TestArtifactCrossTenantAccessIsNotFound(t *testing.T) {
	h := newArtifactHarness(t, true)
	h.syncArtifacts(map[string][]byte{"secret.html": []byte("org a only")})

	viaOwnOrg := h.do(http.MethodGet, "/api/cloud/v1/orgs/"+artifactOrgB+"/sessions/"+artifactSessionA+"/artifacts", "Bearer "+artifactTokenB, nil, nil)
	if viaOwnOrg.Code != http.StatusNotFound {
		t.Fatalf("org B listing org A session via its own org: %d", viaOwnOrg.Code)
	}
	viaOtherOrg := h.do(http.MethodGet, "/api/cloud/v1/orgs/"+artifactOrgA+"/sessions/"+artifactSessionA+"/artifacts", "Bearer "+artifactTokenB, nil, nil)
	if viaOtherOrg.Code != http.StatusForbidden {
		t.Fatalf("org B listing in org A: %d", viaOtherOrg.Code)
	}
	access := h.do(http.MethodPost, "/api/cloud/v1/orgs/"+artifactOrgB+"/sessions/"+artifactSessionA+"/artifacts/access", "Bearer "+artifactTokenB, nil, nil)
	if access.Code != http.StatusNotFound {
		t.Fatalf("org B access to org A session: %d", access.Code)
	}
	ownPrefix := h.accessPrefix(artifactTokenB, artifactOrgB, artifactSessionB)
	if rec := h.do(http.MethodGet, ownPrefix+"secret.html", "", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("org B token reading org A path: %d", rec.Code)
	}
	// A worker of session B cannot write into session A: keys come from its claims.
	workerB := h.workerToken(artifactOrgB, artifactSessionB, artifactEpoch)
	body := []byte("planted")
	if rec := h.upload(workerB, "secret.html", body, hexSHA(body)); rec.Code != http.StatusOK {
		t.Fatalf("worker B upload: %d", rec.Code)
	}
	if h.fake.Versions("orgs/"+artifactOrgA+"/sessions/"+artifactSessionA+"/secret.html") != 1 {
		t.Fatal("worker B wrote into session A's prefix")
	}
}

func TestWorkerArtifactAuthAndValidation(t *testing.T) {
	h := newArtifactHarness(t, true)
	body := []byte("<html></html>")

	if rec := h.upload("", "report.html", body, hexSHA(body)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no worker token: %d", rec.Code)
	}
	stale := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch-1)
	if rec := h.upload(stale, "report.html", body, hexSHA(body)); rec.Code != http.StatusUnauthorized ||
		!strings.Contains(rec.Body.String(), "STALE_WORKER_TOKEN") {
		t.Fatalf("stale epoch: %d %s", rec.Code, rec.Body.String())
	}
	if rec := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", stale, worker.ArtifactCommitRequest{}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("stale epoch commit: %d", rec.Code)
	}

	workerAuth := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)
	if rec := h.upload(workerAuth, "report.html", body, hexSHA([]byte("other"))); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "checksum_mismatch") {
		t.Fatalf("checksum mismatch: %d %s", rec.Code, rec.Body.String())
	}
	if h.fake.Puts() != 0 {
		t.Fatal("a mismatched body created a version")
	}
	for _, path := range []string{"..%2Fescape.html", "%2Fabs.html", ".versions/a.html", "a%5Cb.html"} {
		if rec := h.upload(workerAuth, path, body, hexSHA(body)); rec.Code != http.StatusBadRequest {
			t.Fatalf("path %q: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := h.upload(workerAuth, "report.html", body, "NOT-HEX"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad sha header: %d", rec.Code)
	}

	big := httptest.NewRequest(http.MethodPut, "/api/cloud/v1/worker/artifacts/blob/big.bin", strings.NewReader(""))
	big.ContentLength = domain.MaxArtifactFileBytes + 1
	big.Header.Set("Authorization", workerAuth)
	big.Header.Set(worker.ArtifactSHA256Header, hexSHA(body))
	rec := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over file cap: %d %s", rec.Code, rec.Body.String())
	}

	chunked := httptest.NewRequest(http.MethodPut, "/api/cloud/v1/worker/artifacts/blob/report.html", bytes.NewReader(body))
	chunked.ContentLength = -1
	chunked.Header.Set("Authorization", workerAuth)
	chunked.Header.Set(worker.ArtifactSHA256Header, hexSHA(body))
	rec = httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, chunked)
	if rec.Code != http.StatusLengthRequired {
		t.Fatalf("unknown length: %d %s", rec.Code, rec.Body.String())
	}

	tooMany := worker.ArtifactManifestRequest{}
	for index := range domain.MaxSessionArtifactFiles + 1 {
		tooMany.Files = append(tooMany.Files, worker.ArtifactManifestFile{Path: "f" + strconv.Itoa(index), Size: 1, SHA256: hexSHA(body)})
	}
	if rec := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/manifest", workerAuth, tooMany); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over file count: %d", rec.Code)
	}
}

func TestWorkerArtifactCommitVerifiesVersions(t *testing.T) {
	h := newArtifactHarness(t, true)
	workerAuth := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)
	body := []byte("report")
	upload := decodeBody[worker.ArtifactBlobResponse](t, h.upload(workerAuth, "report.html", body, hexSHA(body)))

	missing := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", workerAuth, worker.ArtifactCommitRequest{
		Files: []worker.ArtifactCommitFile{{Path: "report.html", VersionID: "no-such-version", Size: int64(len(body)), SHA256: hexSHA(body)}},
	})
	if missing.Code != http.StatusConflict || !strings.Contains(missing.Body.String(), "artifact_version_missing") {
		t.Fatalf("missing version: %d %s", missing.Code, missing.Body.String())
	}
	mismatch := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", workerAuth, worker.ArtifactCommitRequest{
		Files: []worker.ArtifactCommitFile{{Path: "report.html", VersionID: upload.VersionID, Size: int64(len(body)), SHA256: hexSHA([]byte("other"))}},
	})
	if mismatch.Code != http.StatusConflict || !strings.Contains(mismatch.Body.String(), "artifact_version_mismatch") {
		t.Fatalf("mismatched version: %d %s", mismatch.Code, mismatch.Body.String())
	}
	if h.store.commits != 0 {
		t.Fatal("a rejected commit reached the store")
	}
	ok := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", workerAuth, worker.ArtifactCommitRequest{
		Files: []worker.ArtifactCommitFile{{Path: "report.html", VersionID: upload.VersionID, Size: int64(len(body)), SHA256: hexSHA(body)}},
	})
	if ok.Code != http.StatusNoContent || h.store.commits != 1 {
		t.Fatalf("commit: %d %s", ok.Code, ok.Body.String())
	}
	deleted := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", workerAuth, worker.ArtifactCommitRequest{Deleted: []string{"report.html"}})
	if deleted.Code != http.StatusNoContent || len(h.store.rows[artifactSessionA]) != 0 {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}
	if len(h.store.replaced) != 1 || h.store.replaced[0].VersionID != upload.VersionID {
		t.Fatalf("replaced = %+v, want the deleted version queued for the sweeper", h.store.replaced)
	}
}

func TestCreateSessionRequestedOutput(t *testing.T) {
	request := func(output string) *http.Request {
		req := offeringSessionRequest(t, teamPrincipal("nodeops"), "")
		body, _ := io.ReadAll(req.Body)
		patched := strings.TrimSuffix(string(body), "}") + `,"requestedOutput":"` + output + `"}`
		req.Body = io.NopCloser(strings.NewReader(patched))
		req.ContentLength = int64(len(patched))
		return req
	}

	off := &offeringStore{}
	rec := httptest.NewRecorder()
	newOfferingServer(off).createSession(rec, request("artifact"))
	if rec.Code != http.StatusUnprocessableEntity || off.created {
		t.Fatalf("flag off: %d %s", rec.Code, rec.Body.String())
	}

	on := &artifactOfferingStore{offeringStore: &offeringStore{}, memoryArtifactRows: newMemoryArtifactRows()}
	server := newOfferingServer(on)
	server.artifacts, _ = artifactstore.New(context.Background(), s3fake.New(t).Config())
	server.artifactRows = on
	server.artifactAccessKey = []byte("key")
	rec = httptest.NewRecorder()
	server.createSession(rec, request("pdf"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown output: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.createSession(rec, request("artifact"))
	if rec.Code != http.StatusCreated || on.captured.RequestedOutput != domain.RequestedOutputArtifact {
		t.Fatalf("flag on: %d %s captured=%+v", rec.Code, rec.Body.String(), on.captured)
	}
}

type artifactOfferingStore struct {
	*offeringStore
	*memoryArtifactRows
}

func TestLaunchContextCarriesRequestedOutput(t *testing.T) {
	session := domain.WorkerLaunch{SessionID: artifactSessionA, Kind: "worker", RequestedOutput: domain.RequestedOutputArtifact}
	launch, err := launchContextFrom(session, launchCapabilities{resolvesWorkspacePath: true, artifacts: true})
	if err != nil || launch.RequestedOutput != domain.RequestedOutputArtifact {
		t.Fatalf("launch = %+v, %v", launch, err)
	}
	launch, err = launchContextFrom(session, launchCapabilities{resolvesWorkspacePath: true})
	if err != nil || launch.RequestedOutput != "" {
		t.Fatalf("launch with artifacts off = %+v, %v; want no requested output", launch, err)
	}
}

func TestSessionOutputTypeCountsLaterPullRequests(t *testing.T) {
	prs := []contract.PRFacts{{URL: "https://github.test/o/r/pull/1"}}
	cases := []struct {
		stored string
		prs    []contract.PRFacts
		want   string
	}{
		{"", nil, domain.SessionOutputNone},
		{domain.SessionOutputNone, prs, domain.SessionOutputPR},
		{domain.SessionOutputArtifact, nil, domain.SessionOutputArtifact},
		{domain.SessionOutputArtifact, prs, domain.SessionOutputPRAndArtifact},
		{domain.SessionOutputPR, nil, domain.SessionOutputPR},
	}
	for _, tc := range cases {
		if got := sessionOutputType(domain.Session{OutputType: tc.stored}, tc.prs); got != tc.want {
			t.Errorf("stored %q prs %d = %q, want %q", tc.stored, len(tc.prs), got, tc.want)
		}
	}
}

func TestWorkerReconnectReportsArtifacts(t *testing.T) {
	cases := []struct {
		enabled, capable, want bool
	}{
		{enabled: false, capable: true, want: false},
		{enabled: true, capable: false, want: false},
		{enabled: true, capable: true, want: true},
	}
	for _, tc := range cases {
		h := newArtifactHarness(t, tc.enabled)
		headers := map[string]string{}
		if tc.capable {
			headers[worker.CapabilitiesHeader] = worker.CapabilityArtifacts
		}
		rec := h.do(http.MethodGet, "/api/cloud/v1/worker/reconnect", h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch), nil, headers)
		if rec.Code != http.StatusOK {
			t.Fatalf("reconnect: %d %s", rec.Code, rec.Body.String())
		}
		launch := decodeBody[worker.BootstrapResponse](t, rec).Launch
		wantOutput := ""
		if tc.want {
			wantOutput = domain.RequestedOutputArtifact
		}
		if launch.Artifacts != tc.want || launch.RequestedOutput != wantOutput {
			t.Fatalf("%+v: launch artifacts=%v requestedOutput=%q", tc, launch.Artifacts, launch.RequestedOutput)
		}
	}
}

func TestArtifactUploadsAreCappedBeforeCommit(t *testing.T) {
	h := newArtifactHarness(t, true)
	h.store.mu.Lock()
	for index := range domain.MaxPendingArtifactUploads {
		h.store.pending[domain.ArtifactObjectVersion{S3Key: "k", VersionID: strconv.Itoa(index)}] = pendingUpload{sessionID: artifactSessionA, size: 1}
	}
	h.store.mu.Unlock()
	workerAuth := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)
	rec := h.upload(workerAuth, "report.html", []byte("x"), hexSHA([]byte("x")))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("upload over the pending limit: %d %s", rec.Code, rec.Body.String())
	}
	if h.fake.Puts() != 0 {
		t.Fatal("an upload over the pending limit reached S3")
	}
}

func TestArtifactCommitNeedsAnOpenUpload(t *testing.T) {
	h := newArtifactHarness(t, true)
	h.syncArtifacts(map[string][]byte{"report.html": []byte("first")})
	workerAuth := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)
	committed := h.store.rows[artifactSessionA]["report.html"]
	// Committing the same version again: it is no longer an open upload.
	rec := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", workerAuth, worker.ArtifactCommitRequest{
		Files: []worker.ArtifactCommitFile{{Path: "report.html", VersionID: committed.VersionID, Size: committed.Size, SHA256: committed.SHA256}},
	})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "artifact_version_missing") {
		t.Fatalf("recommit: %d %s", rec.Code, rec.Body.String())
	}
	h.syncArtifacts(map[string][]byte{"report.html": []byte("second")})
	if len(h.store.replaced) != 1 || h.store.replaced[0].VersionID != committed.VersionID {
		t.Fatalf("replaced = %+v, want the first version queued", h.store.replaced)
	}
}

func TestArtifactEmptyCommitChangesNothing(t *testing.T) {
	h := newArtifactHarness(t, true)
	workerAuth := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)
	rec := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", workerAuth, worker.ArtifactCommitRequest{})
	if rec.Code != http.StatusNoContent || h.store.commits != 0 {
		t.Fatalf("empty commit: %d, commits = %d", rec.Code, h.store.commits)
	}
}

func TestArtifactFileAccessEndsWithMembership(t *testing.T) {
	h := newArtifactHarness(t, true)
	h.syncArtifacts(map[string][]byte{"report.html": []byte("x")})
	prefix := h.accessPrefix(artifactTokenA, artifactOrgA, artifactSessionA)
	if rec := h.do(http.MethodGet, prefix+"report.html", "", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("member read: %d", rec.Code)
	}
	h.store.mu.Lock()
	delete(h.store.members, artifactUserA)
	h.store.mu.Unlock()
	rec := h.do(http.MethodGet, prefix+"report.html", "", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("read after the user left: %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Security-Policy") != "" || rec.Header().Get("ETag") != "" {
		t.Fatalf("an error response carries file headers: %v", rec.Header())
	}
}

func TestArtifactFileHeadReadsNoBody(t *testing.T) {
	h := newArtifactHarness(t, true)
	body := []byte("<html>x</html>")
	h.syncArtifacts(map[string][]byte{"report.html": body})
	prefix := h.accessPrefix(artifactTokenA, artifactOrgA, artifactSessionA)
	rec := h.do(http.MethodHead, prefix+"report.html", "", nil, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatalf("HEAD: %d %v", rec.Code, rec.Header())
	}
	if h.fake.Gets() != 0 {
		t.Fatalf("HEAD read the object body %d times", h.fake.Gets())
	}
}

func TestArtifactFileServesByteRanges(t *testing.T) {
	h := newArtifactHarness(t, true)
	body := []byte("0123456789")
	h.syncArtifacts(map[string][]byte{"clip.mp4": body})
	prefix := h.accessPrefix(artifactTokenA, artifactOrgA, artifactSessionA)

	whole := h.do(http.MethodGet, prefix+"clip.mp4", "", nil, nil)
	if whole.Code != http.StatusOK || whole.Header().Get("Accept-Ranges") != "bytes" || whole.Body.String() != string(body) {
		t.Fatalf("whole file: %d %v %q", whole.Code, whole.Header(), whole.Body.String())
	}
	etag := whole.Header().Get("ETag")

	for _, tc := range []struct{ name, header, want, contentRange string }{
		{"closed", "bytes=2-4", "234", "bytes 2-4/10"},
		{"open ended", "bytes=7-", "789", "bytes 7-9/10"},
		{"suffix", "bytes=-3", "789", "bytes 7-9/10"},
	} {
		rec := h.do(http.MethodGet, prefix+"clip.mp4", "", nil, map[string]string{"Range": tc.header})
		if rec.Code != http.StatusPartialContent || rec.Body.String() != tc.want || rec.Header().Get("Content-Range") != tc.contentRange ||
			rec.Header().Get("Content-Length") != strconv.Itoa(len(tc.want)) || rec.Header().Get("Content-Type") != "video/mp4" {
			t.Fatalf("%s: %d %v %q", tc.name, rec.Code, rec.Header(), rec.Body.String())
		}
	}

	for name, headers := range map[string]map[string]string{
		"several ranges": {"Range": "bytes=0-1,3-4"},
		"unit not bytes": {"Range": "items=0-1"},
		"stale If-Range": {"Range": "bytes=2-4", "If-Range": `"other"`},
	} {
		if rec := h.do(http.MethodGet, prefix+"clip.mp4", "", nil, headers); rec.Code != http.StatusOK || rec.Body.String() != string(body) {
			t.Fatalf("%s: %d %q", name, rec.Code, rec.Body.String())
		}
	}
	if rec := h.do(http.MethodGet, prefix+"clip.mp4", "", nil, map[string]string{"Range": "bytes=2-4", "If-Range": etag}); rec.Code != http.StatusPartialContent {
		t.Fatalf("matching If-Range: %d", rec.Code)
	}

	past := h.do(http.MethodGet, prefix+"clip.mp4", "", nil, map[string]string{"Range": "bytes=50-60"})
	if past.Code != http.StatusRequestedRangeNotSatisfiable || past.Header().Get("Content-Range") != "bytes */10" {
		t.Fatalf("range past the end: %d %v", past.Code, past.Header())
	}
}

func TestArtifactMediaContentTypes(t *testing.T) {
	for path, want := range map[string]string{
		"a.mp4": "video/mp4", "a.webm": "video/webm", "a.mov": "video/quicktime", "a.m4v": "video/x-m4v", "a.ogv": "video/ogg",
		"a.m4a": "audio/mp4", "a.aac": "audio/aac", "a.flac": "audio/flac", "a.ogg": "audio/ogg", "a.opus": "audio/ogg",
		"a.avif": "image/avif", "a.apng": "image/apng", "a.bmp": "image/bmp", "a.ico": "image/x-icon",
	} {
		if got := artifactstore.ContentType(path); got != want {
			t.Errorf("ContentType(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestListWorkerChildrenIncludesArtifactFiles(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	store := &stubStore{
		listChildren: func(_ context.Context, orgID, _ string, _ bool, _ *domain.Cursor, _ int) ([]domain.Session, bool, error) {
			return []domain.Session{{ID: testChildID, OrgID: orgID, Kind: "worker", UpdatedAt: now, CreatedAt: now}}, false, nil
		},
	}
	rows := newMemoryArtifactRows()
	rows.sessions[testChildID] = testOrgID
	rows.rows[testChildID] = map[string]domain.SessionArtifact{
		"report.html": {Path: "report.html", Name: "report.html", Kind: "html", Size: 20, UpdatedAt: now},
	}
	server := testServer(store)
	list := func() string {
		t.Helper()
		w := httptest.NewRecorder()
		server.listWorkerChildren(w, workerRequest(t, http.MethodGet, "/worker/children", "", "worker:orchestrate"))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}

	if body := list(); strings.Contains(body, "artifact") {
		t.Fatalf("artifacts off: listing names artifacts: %s", body)
	}

	server.artifacts, _ = artifactstore.New(context.Background(), s3fake.New(t).Config())
	server.artifactRows = rows
	server.artifactAccessKey = []byte("key")
	var response struct {
		Items []struct {
			ArtifactCount      *int       `json:"artifactCount"`
			ArtifactsUpdatedAt *time.Time `json:"artifactsUpdatedAt"`
			ArtifactFiles      []struct {
				Path string `json:"path"`
				Kind string `json:"kind"`
			} `json:"artifactFiles"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(list()), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %+v", response.Items)
	}
	item := response.Items[0]
	if item.ArtifactCount == nil || *item.ArtifactCount != 1 || item.ArtifactsUpdatedAt == nil || !item.ArtifactsUpdatedAt.Equal(now) {
		t.Fatalf("artifact summary = %+v", item)
	}
	if len(item.ArtifactFiles) != 1 || item.ArtifactFiles[0].Path != "report.html" || item.ArtifactFiles[0].Kind != "html" {
		t.Fatalf("artifact files = %+v", item.ArtifactFiles)
	}
}

func TestWorkerReadsBackItsCommittedArtifacts(t *testing.T) {
	h := newArtifactHarness(t, true)
	report, notes := []byte("<p>report</p>"), []byte("# notes")
	h.syncArtifacts(map[string][]byte{"report.html": report, "docs/notes.md": notes})
	workerAuth := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)

	list := decodeBody[worker.ArtifactListResponse](t, h.do(http.MethodGet, "/api/cloud/v1/worker/artifacts", workerAuth, nil, nil))
	want := []worker.ArtifactManifestFile{
		{Path: "docs/notes.md", Size: int64(len(notes)), SHA256: hexSHA(notes)},
		{Path: "report.html", Size: int64(len(report)), SHA256: hexSHA(report)},
	}
	if !reflect.DeepEqual(list.Files, want) {
		t.Fatalf("list = %+v, want %+v", list.Files, want)
	}

	rec := h.do(http.MethodGet, "/api/cloud/v1/worker/artifacts/blob/docs/notes.md", workerAuth, nil, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != string(notes) || rec.Header().Get(worker.ArtifactSHA256Header) != hexSHA(notes) {
		t.Fatalf("blob: %d %q sha=%q", rec.Code, rec.Body.String(), rec.Header().Get(worker.ArtifactSHA256Header))
	}
	if rec := h.do(http.MethodGet, "/api/cloud/v1/worker/artifacts/blob/missing.md", workerAuth, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing file: %d %s", rec.Code, rec.Body.String())
	}

	// Another session's worker sees none of these files.
	other := h.workerToken(artifactOrgB, artifactSessionB, artifactEpoch)
	if list := decodeBody[worker.ArtifactListResponse](t, h.do(http.MethodGet, "/api/cloud/v1/worker/artifacts", other, nil, nil)); len(list.Files) != 0 {
		t.Fatalf("other session list = %+v", list.Files)
	}
	if rec := h.do(http.MethodGet, "/api/cloud/v1/worker/artifacts/blob/report.html", other, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("other session blob: %d %s", rec.Code, rec.Body.String())
	}
}

func TestWorkerArtifactReadRoutesNeedArtifactsOn(t *testing.T) {
	h := newArtifactHarness(t, false)
	workerAuth := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)
	for _, target := range []string{"/api/cloud/v1/worker/artifacts", "/api/cloud/v1/worker/artifacts/blob/report.html"} {
		if rec := h.do(http.MethodGet, target, workerAuth, nil, nil); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s with artifacts off: %d", target, rec.Code)
		}
	}
}

func TestUserDeletesASessionArtifact(t *testing.T) {
	h := newArtifactHarness(t, true)
	h.syncArtifacts(map[string][]byte{"report.html": []byte("<p>report</p>"), "notes.md": []byte("# notes")})
	route := "/api/cloud/v1/orgs/" + artifactOrgA + "/sessions/" + artifactSessionA + "/artifacts"
	key := "orgs/" + artifactOrgA + "/sessions/" + artifactSessionA + "/report.html"

	if rec := h.do(http.MethodDelete, route+"?path=report.html", "Bearer "+artifactTokenA, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	listed := decodeBody[struct {
		Artifacts []sessionArtifactResponse `json:"artifacts"`
	}](t, h.do(http.MethodGet, route, "Bearer "+artifactTokenA, nil, nil)).Artifacts
	if len(listed) != 1 || listed[0].Path != "notes.md" {
		t.Fatalf("listed after delete = %+v", listed)
	}
	if versions := h.fake.Versions(key); versions != 0 {
		t.Fatalf("the stored version was not deleted: %d versions left", versions)
	}
	if len(h.store.replaced) != 1 || h.store.replaced[0].S3Key != key {
		t.Fatalf("the version was not queued for the sweeper: %+v", h.store.replaced)
	}

	for name, want := range map[string]int{
		route + "?path=report.html": http.StatusNotFound,
		route + "?path=../x":        http.StatusBadRequest,
		route:                       http.StatusBadRequest,
	} {
		if rec := h.do(http.MethodDelete, name, "Bearer "+artifactTokenA, nil, nil); rec.Code != want {
			t.Errorf("DELETE %s = %d, want %d", name, rec.Code, want)
		}
	}
	if rec := h.do(http.MethodDelete, route+"?path=notes.md", "Bearer "+artifactTokenB, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("another org's member deleted a file: %d", rec.Code)
	}
}

func TestDeleteArtifactRouteNeedsArtifactsOn(t *testing.T) {
	h := newArtifactHarness(t, false)
	route := "/api/cloud/v1/orgs/" + artifactOrgA + "/sessions/" + artifactSessionA + "/artifacts?path=report.html"
	if rec := h.do(http.MethodDelete, route, "Bearer "+artifactTokenA, nil, nil); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("delete with artifacts off: %d", rec.Code)
	}
}

// cliMaxResponseBody mirrors the ao CLI's response limit (maxResponseBody in
// cmd/ao-cloud-agent), which decodes this listing.
const cliMaxResponseBody = 2 << 20

func TestWorkerChildrenListingFitsTheCLIResponseLimit(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	rows := newMemoryArtifactRows()
	children := make([]domain.Session, 0, 100)
	// The longest path that still fits a bucket key under orgs/<uuid>/sessions/<uuid>/.
	longest := strings.Repeat("p", 1024-len("orgs/00000000-0000-0000-0000-000000000000/sessions/00000000-0000-0000-0000-000000000000/")-len("/000.md")) + "/"
	for index := range 100 {
		id := fmt.Sprintf("c0c0c0c0-0000-4000-8000-%012d", index)
		children = append(children, domain.Session{ID: id, OrgID: testOrgID, Kind: "worker", UpdatedAt: now, CreatedAt: now})
		rows.sessions[id] = testOrgID
		rows.rows[id] = map[string]domain.SessionArtifact{}
		for file := range domain.MaxSessionArtifactFiles {
			path := fmt.Sprintf("%s%03d.md", longest, file)
			rows.rows[id][path] = domain.SessionArtifact{Path: path, Name: path, Kind: "markdown", Size: 1, SHA256: strings.Repeat("a", 64), UpdatedAt: now.Add(time.Duration(file) * time.Second)}
		}
	}
	store := &stubStore{
		listChildren: func(context.Context, string, string, bool, *domain.Cursor, int) ([]domain.Session, bool, error) {
			return children, false, nil
		},
	}
	server := testServer(store)
	server.artifacts, _ = artifactstore.New(context.Background(), s3fake.New(t).Config())
	server.artifactRows = rows
	server.artifactAccessKey = []byte("key")

	w := httptest.NewRecorder()
	server.listWorkerChildren(w, workerRequest(t, http.MethodGet, "/worker/children?limit=100", "", "worker:orchestrate"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if size := w.Body.Len(); size > cliMaxResponseBody {
		t.Fatalf("a full page of children with the most and longest artifact paths is %d bytes, over the CLI's %d", size, cliMaxResponseBody)
	}
	var response struct {
		Items []struct {
			ArtifactCount          *int `json:"artifactCount"`
			ArtifactFilesTruncated bool `json:"artifactFilesTruncated"`
			ArtifactFiles          []struct {
				Path string `json:"path"`
			} `json:"artifactFiles"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	first := response.Items[0]
	if first.ArtifactCount == nil || *first.ArtifactCount != domain.MaxSessionArtifactFiles || !first.ArtifactFilesTruncated || len(first.ArtifactFiles) != maxChildArtifactFiles {
		t.Fatalf("child = count %v truncated %t files %d", first.ArtifactCount, first.ArtifactFilesTruncated, len(first.ArtifactFiles))
	}
	if want := fmt.Sprintf("%s%03d.md", longest, domain.MaxSessionArtifactFiles-1); first.ArtifactFiles[0].Path != want {
		t.Fatal("the most recently changed file is not listed first")
	}
}

// writeJSON escapes &, < and > to six bytes each, so a page of paths made of
// them is the largest encoding the listing can produce.
func TestWorkerChildrenListingFitsTheCLIResponseLimitWithEscapedPaths(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	const prefix = "orgs/00000000-0000-0000-0000-000000000000/sessions/00000000-0000-0000-0000-000000000000/"
	escapedPath := func(file int) string {
		segment := strings.Repeat("&", 255)
		name := fmt.Sprintf("/%03d.md", file)
		head := segment + "/" + segment + "/" + segment + "/"
		return head + strings.Repeat("&", 1024-len(prefix)-len(head)-len(name)) + name
	}
	if path := escapedPath(0); len(prefix+path) != 1024 {
		t.Fatalf("test path key is %d bytes, want the 1024 maximum", len(prefix+path))
	} else if cleaned, err := domain.CleanArtifactPath(path); err != nil || cleaned != path {
		t.Fatalf("the escaped test path is not a valid artifact path: %v", err)
	} else if _, err := artifactstore.Key("00000000-0000-0000-0000-000000000000", "00000000-0000-0000-0000-000000000000", path); err != nil {
		t.Fatalf("the escaped test path does not fit a key: %v", err)
	}
	rows := newMemoryArtifactRows()
	children := make([]domain.Session, 0, 100)
	for index := range 100 {
		id := fmt.Sprintf("c1c1c1c1-0000-4000-8000-%012d", index)
		children = append(children, domain.Session{ID: id, OrgID: testOrgID, Kind: "worker", UpdatedAt: now, CreatedAt: now})
		rows.sessions[id] = testOrgID
		rows.rows[id] = map[string]domain.SessionArtifact{}
		for file := range maxChildArtifactFiles {
			path := escapedPath(file)
			rows.rows[id][path] = domain.SessionArtifact{Path: path, Name: path, Kind: "markdown", Size: 1, SHA256: strings.Repeat("a", 64), UpdatedAt: now.Add(time.Duration(file) * time.Second)}
		}
	}
	store := &stubStore{
		listChildren: func(context.Context, string, string, bool, *domain.Cursor, int) ([]domain.Session, bool, error) {
			return children, false, nil
		},
	}
	server := testServer(store)
	server.artifacts, _ = artifactstore.New(context.Background(), s3fake.New(t).Config())
	server.artifactRows = rows
	server.artifactAccessKey = []byte("key")

	w := httptest.NewRecorder()
	server.listWorkerChildren(w, workerRequest(t, http.MethodGet, "/worker/children?limit=100", "", "worker:orchestrate"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if size := w.Body.Len(); size > cliMaxResponseBody {
		t.Fatalf("a full page of children with escaped artifact paths is %d bytes, over the CLI's %d", size, cliMaxResponseBody)
	}
	var response struct {
		Items []struct {
			ArtifactCount          *int `json:"artifactCount"`
			ArtifactFilesTruncated bool `json:"artifactFilesTruncated"`
			ArtifactFiles          []struct {
				Path string `json:"path"`
			} `json:"artifactFiles"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	first := response.Items[0]
	if len(first.ArtifactFiles) == 0 || len(first.ArtifactFiles) >= maxChildArtifactFiles || !first.ArtifactFilesTruncated || *first.ArtifactCount != maxChildArtifactFiles {
		t.Fatalf("child = %d files listed, truncated %t, count %d; want fewer than %d, truncated, the full count", len(first.ArtifactFiles), first.ArtifactFilesTruncated, *first.ArtifactCount, maxChildArtifactFiles)
	}
	if first.ArtifactFiles[0].Path != escapedPath(maxChildArtifactFiles-1) {
		t.Fatal("the most recently changed file is not listed first")
	}
}
