package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	commits  int
}

func newMemoryArtifactRows() *memoryArtifactRows {
	return &memoryArtifactRows{
		sessions: map[string]string{artifactSessionA: artifactOrgA, artifactSessionB: artifactOrgB},
		members:  map[string]string{artifactUserA: artifactOrgA, artifactUserB: artifactOrgB},
		rows:     map[string]map[string]domain.SessionArtifact{},
	}
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

func (m *memoryArtifactRows) CommitSessionArtifacts(_ context.Context, orgID, sessionID string, upserts []domain.SessionArtifact, deletes []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[sessionID] != orgID {
		return postgres.ErrNotFound
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

func (m *memoryArtifactRows) SessionArtifact(_ context.Context, orgID, sessionID, path string) (domain.SessionArtifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[sessionID][path]
	if m.sessions[sessionID] != orgID || !ok {
		return domain.SessionArtifact{}, postgres.ErrNotFound
	}
	return row, nil
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
		map[string]string{ArtifactSHA256Header: sha})
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
	commit := workerArtifactCommitRequest{}
	for path, body := range files {
		rec := h.upload(authorization, path, body, hexSHA(body))
		if rec.Code != http.StatusOK {
			h.t.Fatalf("upload %s: %d %s", path, rec.Code, rec.Body.String())
		}
		blob := decodeBody[workerArtifactBlobResponse](h.t, rec)
		commit.Files = append(commit.Files, workerArtifactCommitFile{
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
	worker := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)
	for _, rec := range []*httptest.ResponseRecorder{
		h.do(http.MethodGet, "/api/cloud/v1/orgs/"+artifactOrgA+"/sessions/"+artifactSessionA+"/artifacts", "Bearer "+artifactTokenA, nil, nil),
		h.do(http.MethodPost, "/api/cloud/v1/orgs/"+artifactOrgA+"/sessions/"+artifactSessionA+"/artifacts/access", "Bearer "+artifactTokenA, nil, nil),
		h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/manifest", worker, workerArtifactManifestRequest{}),
		h.upload(worker, "report.html", []byte("x"), hexSHA([]byte("x"))),
		h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", worker, workerArtifactCommitRequest{}),
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
	worker := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)

	manifest := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/manifest", worker, workerArtifactManifestRequest{
		Files: []workerArtifactManifestFile{{Path: "report.html", Size: int64(len(html)), SHA256: hexSHA(html)}},
	})
	if manifest.Code != http.StatusOK || !slices.Equal(decodeBody[workerArtifactManifestResponse](t, manifest).Changed, []string{"report.html"}) {
		t.Fatalf("first manifest: %d %s", manifest.Code, manifest.Body.String())
	}

	h.syncArtifacts(map[string][]byte{"report.html": html, "assets/chart.png": png})

	manifest = h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/manifest", worker, workerArtifactManifestRequest{
		Files: []workerArtifactManifestFile{
			{Path: "report.html", Size: int64(len(html)), SHA256: hexSHA(html)},
			{Path: "assets/chart.png", Size: int64(len(png)), SHA256: hexSHA(png)},
		},
	})
	if changed := decodeBody[workerArtifactManifestResponse](t, manifest).Changed; len(changed) != 0 {
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
	if projected.OutputType != domain.SessionOutputArtifact || len(projected.ArtifactFiles) != 2 {
		t.Fatalf("session projection = %+v", projected)
	}

	if body := getMe(t, h.server, domain.Principal{UserID: artifactUserA}); !body.Offering.Artifacts {
		t.Fatal("/me does not report artifacts with the flag on")
	}
}

func TestArtifactFileServesCommittedVersionOnly(t *testing.T) {
	h := newArtifactHarness(t, true)
	h.syncArtifacts(map[string][]byte{"report.html": []byte("committed")})
	worker := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)
	// A newer upload that is never committed becomes the latest S3 version.
	uncommitted := []byte("uncommitted")
	if rec := h.upload(worker, "report.html", uncommitted, hexSHA(uncommitted)); rec.Code != http.StatusOK {
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
	if rec := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", stale, workerArtifactCommitRequest{}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("stale epoch commit: %d", rec.Code)
	}

	worker := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)
	if rec := h.upload(worker, "report.html", body, hexSHA([]byte("other"))); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "checksum_mismatch") {
		t.Fatalf("checksum mismatch: %d %s", rec.Code, rec.Body.String())
	}
	if h.fake.Puts() != 0 {
		t.Fatal("a mismatched body created a version")
	}
	for _, path := range []string{"..%2Fescape.html", "%2Fabs.html", ".versions/a.html", "a%5Cb.html"} {
		if rec := h.upload(worker, path, body, hexSHA(body)); rec.Code != http.StatusBadRequest {
			t.Fatalf("path %q: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := h.upload(worker, "report.html", body, "NOT-HEX"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad sha header: %d", rec.Code)
	}

	big := httptest.NewRequest(http.MethodPut, "/api/cloud/v1/worker/artifacts/blob/big.bin", strings.NewReader(""))
	big.ContentLength = domain.MaxArtifactFileBytes + 1
	big.Header.Set("Authorization", worker)
	big.Header.Set(ArtifactSHA256Header, hexSHA(body))
	rec := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over file cap: %d %s", rec.Code, rec.Body.String())
	}

	chunked := httptest.NewRequest(http.MethodPut, "/api/cloud/v1/worker/artifacts/blob/report.html", bytes.NewReader(body))
	chunked.ContentLength = -1
	chunked.Header.Set("Authorization", worker)
	chunked.Header.Set(ArtifactSHA256Header, hexSHA(body))
	rec = httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, chunked)
	if rec.Code != http.StatusLengthRequired {
		t.Fatalf("unknown length: %d %s", rec.Code, rec.Body.String())
	}

	tooMany := workerArtifactManifestRequest{}
	for index := range domain.MaxSessionArtifactFiles + 1 {
		tooMany.Files = append(tooMany.Files, workerArtifactManifestFile{Path: "f" + strconv.Itoa(index), Size: 1, SHA256: hexSHA(body)})
	}
	if rec := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/manifest", worker, tooMany); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over file count: %d", rec.Code)
	}
}

func TestWorkerArtifactCommitVerifiesVersions(t *testing.T) {
	h := newArtifactHarness(t, true)
	worker := h.workerToken(artifactOrgA, artifactSessionA, artifactEpoch)
	body := []byte("report")
	upload := decodeBody[workerArtifactBlobResponse](t, h.upload(worker, "report.html", body, hexSHA(body)))

	missing := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", worker, workerArtifactCommitRequest{
		Files: []workerArtifactCommitFile{{Path: "report.html", VersionID: "no-such-version", Size: int64(len(body)), SHA256: hexSHA(body)}},
	})
	if missing.Code != http.StatusConflict || !strings.Contains(missing.Body.String(), "artifact_version_missing") {
		t.Fatalf("missing version: %d %s", missing.Code, missing.Body.String())
	}
	mismatch := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", worker, workerArtifactCommitRequest{
		Files: []workerArtifactCommitFile{{Path: "report.html", VersionID: upload.VersionID, Size: int64(len(body)), SHA256: hexSHA([]byte("other"))}},
	})
	if mismatch.Code != http.StatusConflict || !strings.Contains(mismatch.Body.String(), "artifact_version_mismatch") {
		t.Fatalf("mismatched version: %d %s", mismatch.Code, mismatch.Body.String())
	}
	if h.store.commits != 0 {
		t.Fatal("a rejected commit reached the store")
	}
	ok := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", worker, workerArtifactCommitRequest{
		Files: []workerArtifactCommitFile{{Path: "report.html", VersionID: upload.VersionID, Size: int64(len(body)), SHA256: hexSHA(body)}},
	})
	if ok.Code != http.StatusNoContent || h.store.commits != 1 {
		t.Fatalf("commit: %d %s", ok.Code, ok.Body.String())
	}
	deleted := h.workerJSON(http.MethodPost, "/api/cloud/v1/worker/artifacts/commit", worker, workerArtifactCommitRequest{Deleted: []string{"report.html"}})
	if deleted.Code != http.StatusNoContent || len(h.store.rows[artifactSessionA]) != 0 {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
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
	launch, err := launchContextFrom(domain.WorkerLaunch{SessionID: artifactSessionA, Kind: "worker", RequestedOutput: domain.RequestedOutputArtifact}, true)
	if err != nil || launch.RequestedOutput != domain.RequestedOutputArtifact {
		t.Fatalf("launch = %+v, %v", launch, err)
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
