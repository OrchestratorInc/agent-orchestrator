package httpapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/artifactstore"
	"github.com/aoagents/agent-orchestrator/cloud/internal/artifactstore/s3fake"
	"github.com/aoagents/agent-orchestrator/cloud/internal/auth"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

const (
	shareEmailA = "aditi1178@gmail.com"
	shareEmailB = "dev@example.com"
	shareOrigin = "http://share.localhost:8080"
)

// memoryShareRows is an in-memory artifactShareStore with the same per-user
// scoping, slug collision and limit rules as the Postgres store.
type memoryShareRows struct {
	mu     sync.Mutex
	shares map[string]domain.ArtifactShare
	clock  time.Time
}

func newMemoryShareRows() *memoryShareRows {
	return &memoryShareRows{shares: map[string]domain.ArtifactShare{}, clock: time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)}
}

func (m *memoryShareRows) countLocked(userID string) int {
	count := 0
	for _, share := range m.shares {
		if share.UserID == userID {
			count++
		}
	}
	return count
}

func (m *memoryShareRows) CountArtifactShares(_ context.Context, userID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.countLocked(userID), nil
}

func (m *memoryShareRows) CreateArtifactShare(_ context.Context, share domain.ArtifactShare, baseSlug string, limit int) (domain.ArtifactShare, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.countLocked(share.UserID) >= limit {
		return domain.ArtifactShare{}, postgres.ErrArtifactShareLimit
	}
	taken := map[string]bool{}
	for _, existing := range m.shares {
		taken[existing.Slug] = true
	}
	for attempt := 1; attempt <= domain.MaxArtifactShareSlugAttempts; attempt++ {
		slug := domain.ArtifactShareSlugCandidate(baseSlug, attempt)
		if taken[slug] {
			continue
		}
		m.clock = m.clock.Add(time.Second)
		share.Slug, share.CreatedAt, share.UpdatedAt = slug, m.clock, m.clock
		m.shares[share.ID] = share
		return share, nil
	}
	return domain.ArtifactShare{}, postgres.ErrConflict
}

func (m *memoryShareRows) UpdateArtifactShare(_ context.Context, share domain.ArtifactShare) (domain.ArtifactShare, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.shares[share.ID]
	if !ok || current.UserID != share.UserID {
		return domain.ArtifactShare{}, "", postgres.ErrNotFound
	}
	previous := current.VersionID
	m.clock = m.clock.Add(time.Second)
	current.Name, current.ContentType, current.Size = share.Name, share.ContentType, share.Size
	current.SHA256, current.VersionID, current.UpdatedAt = share.SHA256, share.VersionID, m.clock
	m.shares[share.ID] = current
	return current, previous, nil
}

func (m *memoryShareRows) GetArtifactShare(_ context.Context, userID, shareID string) (domain.ArtifactShare, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	share, ok := m.shares[shareID]
	if !ok || share.UserID != userID {
		return domain.ArtifactShare{}, postgres.ErrNotFound
	}
	return share, nil
}

func (m *memoryShareRows) ListArtifactShares(_ context.Context, userID string) ([]domain.ArtifactShare, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var shares []domain.ArtifactShare
	for _, share := range m.shares {
		if share.UserID == userID {
			shares = append(shares, share)
		}
	}
	sort.Slice(shares, func(i, j int) bool { return shares[i].CreatedAt.After(shares[j].CreatedAt) })
	return shares, nil
}

func (m *memoryShareRows) DeleteArtifactShare(_ context.Context, userID, shareID string) (domain.ArtifactShare, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	share, ok := m.shares[shareID]
	if !ok || share.UserID != userID {
		return domain.ArtifactShare{}, postgres.ErrNotFound
	}
	delete(m.shares, shareID)
	return share, nil
}

func (m *memoryShareRows) ArtifactShareBySlug(_ context.Context, slug string) (domain.ArtifactShare, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, share := range m.shares {
		if share.Slug == slug {
			return share, nil
		}
	}
	return domain.ArtifactShare{}, postgres.ErrNotFound
}

// shareRouterStore authenticates the two test users with their emails and
// serves their share rows.
type shareRouterStore struct {
	*artifactRouterStore
	*memoryShareRows
}

func (s *shareRouterStore) PrincipalFromLocalToken(_ context.Context, hash []byte) (domain.Principal, error) {
	switch {
	case bytes.Equal(hash, auth.HashToken(artifactTokenA)):
		return domain.Principal{UserID: artifactUserA, Provider: "local", Email: shareEmailA}, nil
	case bytes.Equal(hash, auth.HashToken(artifactTokenB)):
		return domain.Principal{UserID: artifactUserB, Provider: "local", Email: shareEmailB}, nil
	}
	return domain.Principal{}, postgres.ErrNotFound
}

type shareHarness struct {
	t      *testing.T
	server *Server
	rows   *memoryShareRows
	fake   *s3fake.Server
}

func newShareHarness(t *testing.T, shares *ArtifactShareOptions) *shareHarness {
	t.Helper()
	fake := s3fake.New(t)
	artifacts, err := artifactstore.New(context.Background(), fake.Config())
	if err != nil {
		t.Fatal(err)
	}
	rows := newMemoryShareRows()
	store := &shareRouterStore{
		artifactRouterStore: &artifactRouterStore{memoryArtifactRows: newMemoryArtifactRows()},
		memoryShareRows:     rows,
	}
	server := New(Options{
		Store:                store,
		LocalAuthEnabled:     true,
		WorkerTokens:         worker.NewTokenManager([]byte(strings.Repeat("k", 32))),
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
		Artifacts:            artifacts,
		ArtifactAccessSecret: bytes.Repeat([]byte{7}, 32),
		ArtifactShares:       shares,
	})
	return &shareHarness{t: t, server: server, rows: rows, fake: fake}
}

func (h *shareHarness) request(method, target, host, token string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	if host != "" {
		req.Host = host
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, req)
	return rec
}

func (h *shareHarness) publish(token, name, project, feature string, body []byte) *httptest.ResponseRecorder {
	h.t.Helper()
	query := url.Values{"name": {name}, "project": {project}, "feature": {feature}}
	return h.request(http.MethodPost, "/api/cloud/v1/me/artifact-shares?"+query.Encode(), "", token, body,
		map[string]string{worker.ArtifactSHA256Header: hexSHA(body)})
}

func (h *shareHarness) republish(token, shareID, name string, body []byte) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.request(http.MethodPut, "/api/cloud/v1/me/artifact-shares/"+shareID+"?name="+url.QueryEscape(name), "", token, body,
		map[string]string{worker.ArtifactSHA256Header: hexSHA(body)})
}

func (h *shareHarness) mustPublish(token, name, project, feature string, body []byte) artifactShareResponse {
	h.t.Helper()
	rec := h.publish(token, name, project, feature, body)
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("publish %s: %d %s", name, rec.Code, rec.Body.String())
	}
	return decodeBody[struct {
		Share artifactShareResponse `json:"share"`
	}](h.t, rec).Share
}

// visit fetches a share link the way a browser would, by its host.
func (h *shareHarness) visit(link string, headers map[string]string) *httptest.ResponseRecorder {
	h.t.Helper()
	parsed, err := url.Parse(link)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.request(http.MethodGet, parsed.Path, parsed.Host, "", nil, headers)
}

func TestArtifactShareRoutesAreOffWithoutTheOption(t *testing.T) {
	h := newShareHarness(t, nil)
	if h.server.artifactSharesEnabled() {
		t.Fatal("artifact shares enabled without Options.ArtifactShares")
	}
	for _, rec := range []*httptest.ResponseRecorder{
		h.publish(artifactTokenA, "report.html", "p", "f", []byte("x")),
		h.request(http.MethodGet, "/api/cloud/v1/me/artifact-shares", "", artifactTokenA, nil, nil),
		h.request(http.MethodGet, "/", "p-f-aditi1178-gmail-com.share.localhost:8080", "", nil, nil),
	} {
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d with shares off; body=%s", rec.Code, rec.Body.String())
		}
	}
	if h.fake.Puts() != 0 {
		t.Fatal("a share reached S3 with shares off")
	}
}

func TestArtifactSharePublishAndServe(t *testing.T) {
	h := newShareHarness(t, &ArtifactShareOptions{Origin: shareOrigin, Limit: 3})
	html := []byte("<html><h1>Login page</h1></html>")
	share := h.mustPublish(artifactTokenA, "report.html", "agent-orchestrator", "Login Page", html)

	wantSlug := "agent-orchestrator-login-page-aditi1178-gmail-com"
	if share.Slug != wantSlug || share.URL != "http://"+wantSlug+".share.localhost:8080/" {
		t.Fatalf("share = %+v", share)
	}
	if share.Name != "report.html" || share.ContentType != "text/html; charset=utf-8" || share.Size != int64(len(html)) {
		t.Fatalf("share metadata = %+v", share)
	}

	page := h.visit(share.URL, nil)
	if page.Code != http.StatusOK || page.Body.String() != string(html) {
		t.Fatalf("visit: %d %q", page.Code, page.Body.String())
	}
	headers := page.Header()
	if headers.Get("Content-Type") != "text/html; charset=utf-8" ||
		headers.Get("X-Content-Type-Options") != "nosniff" ||
		headers.Get("Referrer-Policy") != "no-referrer" ||
		headers.Get("ETag") != `"`+hexSHA(html)+`"` ||
		!strings.HasPrefix(headers.Get("Content-Security-Policy"), "sandbox ") ||
		strings.Contains(headers.Get("Content-Security-Policy"), "allow-same-origin") ||
		headers.Get("Set-Cookie") != "" {
		t.Fatalf("headers = %v", headers)
	}
	if rec := h.visit(share.URL, map[string]string{"If-None-Match": `"` + hexSHA(html) + `"`}); rec.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match status = %d", rec.Code)
	}

	notes := h.mustPublish(artifactTokenA, "notes.md", "agent-orchestrator", "Docs", []byte("# Notes"))
	if rec := h.visit(notes.URL, nil); rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("markdown content type = %q", rec.Header().Get("Content-Type"))
	}

	list := h.request(http.MethodGet, "/api/cloud/v1/me/artifact-shares", "", artifactTokenA, nil, nil)
	listed := decodeBody[struct {
		Shares []artifactShareResponse `json:"shares"`
		Limit  int                     `json:"limit"`
		Used   int                     `json:"used"`
	}](t, list)
	if list.Code != http.StatusOK || listed.Limit != 3 || listed.Used != 2 || len(listed.Shares) != 2 || listed.Shares[0].ID != notes.ID {
		t.Fatalf("list: %d %+v", list.Code, listed)
	}
	if strings.Contains(list.Body.String(), "shares/") || strings.Contains(list.Body.String(), "versionId") {
		t.Fatalf("list exposes storage keys: %s", list.Body.String())
	}
	other := h.request(http.MethodGet, "/api/cloud/v1/me/artifact-shares", "", artifactTokenB, nil, nil)
	if used := decodeBody[struct {
		Used int `json:"used"`
	}](t, other).Used; used != 0 {
		t.Fatalf("user B sees %d of user A's shares", used)
	}
}

func TestArtifactShareSlugCollisionGetsASuffix(t *testing.T) {
	h := newShareHarness(t, &ArtifactShareOptions{Origin: shareOrigin, Limit: 3})
	first := h.mustPublish(artifactTokenA, "a.html", "proj", "feat", []byte("a"))
	second := h.mustPublish(artifactTokenA, "b.html", "proj", "feat", []byte("b"))
	if first.Slug != "proj-feat-aditi1178-gmail-com" || second.Slug != "proj-feat-aditi1178-gmail-com-2" {
		t.Fatalf("slugs = %q, %q", first.Slug, second.Slug)
	}
	if rec := h.visit(second.URL, nil); rec.Body.String() != "b" {
		t.Fatalf("second link served %q", rec.Body.String())
	}
}

func TestArtifactShareRepublishKeepsLinkAndSlot(t *testing.T) {
	h := newShareHarness(t, &ArtifactShareOptions{Origin: shareOrigin, Limit: 1})
	share := h.mustPublish(artifactTokenA, "report.html", "proj", "feat", []byte("v1"))
	for _, body := range []string{"v2", "v3"} {
		rec := h.republish(artifactTokenA, share.ID, "report.html", []byte(body))
		if rec.Code != http.StatusOK {
			t.Fatalf("republish %s: %d %s", body, rec.Code, rec.Body.String())
		}
		updated := decodeBody[struct {
			Share artifactShareResponse `json:"share"`
		}](t, rec).Share
		if updated.URL != share.URL || updated.ID != share.ID {
			t.Fatalf("republish changed the link: %+v", updated)
		}
		if page := h.visit(share.URL, nil); page.Body.String() != body {
			t.Fatalf("served %q after republishing %q", page.Body.String(), body)
		}
	}
	if versions := h.fake.Versions("shares/" + share.ID); versions != 1 {
		t.Fatalf("stored versions = %d, want only the live one", versions)
	}
	if rec := h.republish(artifactTokenB, share.ID, "report.html", []byte("hijack")); rec.Code != http.StatusNotFound {
		t.Fatalf("another user republished: %d", rec.Code)
	}
	if page := h.visit(share.URL, nil); page.Body.String() != "v3" {
		t.Fatalf("served %q after another user's attempt", page.Body.String())
	}
}

func TestArtifactShareLimitAndRevoke(t *testing.T) {
	h := newShareHarness(t, &ArtifactShareOptions{Origin: shareOrigin, Limit: 3})
	var shares []artifactShareResponse
	for _, name := range []string{"one.html", "two.html", "three.html"} {
		shares = append(shares, h.mustPublish(artifactTokenA, name, "proj", name, []byte(name)))
	}
	putsBefore := h.fake.Puts()
	over := h.publish(artifactTokenA, "four.html", "proj", "four", []byte("four"))
	if over.Code != http.StatusConflict || !strings.Contains(over.Body.String(), "artifact_share_limit_reached") ||
		!strings.Contains(over.Body.String(), "Upgrade to AO Cloud") {
		t.Fatalf("fourth share: %d %s", over.Code, over.Body.String())
	}
	if h.fake.Puts() != putsBefore {
		t.Fatal("a share over the limit reached S3")
	}
	h.mustPublish(artifactTokenB, "mine.html", "proj", "feat", []byte("b"))

	if rec := h.request(http.MethodDelete, "/api/cloud/v1/me/artifact-shares/"+shares[0].ID, "", artifactTokenB, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("another user revoked: %d", rec.Code)
	}
	if rec := h.request(http.MethodDelete, "/api/cloud/v1/me/artifact-shares/"+shares[0].ID, "", artifactTokenA, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body.String())
	}
	if rec := h.visit(shares[0].URL, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("revoked link status = %d", rec.Code)
	}
	if versions := h.fake.Versions("shares/" + shares[0].ID); versions != 0 {
		t.Fatalf("revoked share kept %d stored versions", versions)
	}
	h.mustPublish(artifactTokenA, "four.html", "proj", "four", []byte("four"))
}

func TestArtifactShareUploadValidation(t *testing.T) {
	h := newShareHarness(t, &ArtifactShareOptions{Origin: shareOrigin, Limit: 3})
	body := []byte("x")
	cases := map[string]struct {
		target string
		sha    string
		want   int
	}{
		"missing name":   {"/api/cloud/v1/me/artifact-shares", hexSHA(body), http.StatusBadRequest},
		"directory name": {"/api/cloud/v1/me/artifact-shares?name=a%2Fb.html", hexSHA(body), http.StatusBadRequest},
		"dot dot":        {"/api/cloud/v1/me/artifact-shares?name=..", hexSHA(body), http.StatusBadRequest},
		"bad sha":        {"/api/cloud/v1/me/artifact-shares?name=a.html", "nothex", http.StatusBadRequest},
		"wrong sha":      {"/api/cloud/v1/me/artifact-shares?name=a.html", hexSHA([]byte("y")), http.StatusUnprocessableEntity},
	}
	for label, tc := range cases {
		rec := h.request(http.MethodPost, tc.target, "", artifactTokenA, body, map[string]string{worker.ArtifactSHA256Header: tc.sha})
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d; body=%s", label, rec.Code, tc.want, rec.Body.String())
		}
	}
	big := httptest.NewRequest(http.MethodPost, "/api/cloud/v1/me/artifact-shares?name=big.bin", strings.NewReader(""))
	big.ContentLength = domain.MaxArtifactShareBytes + 1
	big.Header.Set("Authorization", "Bearer "+artifactTokenA)
	big.Header.Set(worker.ArtifactSHA256Header, hexSHA(body))
	rec := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("too large: status = %d", rec.Code)
	}
	if rec := h.request(http.MethodPost, "/api/cloud/v1/me/artifact-shares?name=a.html", "", "", body, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated publish: status = %d", rec.Code)
	}
	if used, _ := h.rows.CountArtifactShares(context.Background(), artifactUserA); used != 0 {
		t.Fatalf("rejected uploads created %d shares", used)
	}
}

func TestArtifactShareHostRouting(t *testing.T) {
	h := newShareHarness(t, &ArtifactShareOptions{Origin: shareOrigin, Limit: 3})
	share := h.mustPublish(artifactTokenA, "report.html", "proj", "feat", []byte("hello"))
	host := share.Slug + ".share.localhost:8080"

	if rec := h.request(http.MethodGet, "/other.html", host, "", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("non-root path status = %d", rec.Code)
	}
	if rec := h.request(http.MethodPost, "/", host, "", nil, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", rec.Code)
	}
	if rec := h.request(http.MethodGet, "/", "missing-slug.share.localhost:8080", "", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown slug status = %d", rec.Code)
	}
	if rec := h.request(http.MethodGet, "/", "SHARE.LOCALHOST:8080", "", nil, nil); rec.Code == http.StatusOK {
		t.Fatal("the bare share domain served a share")
	}
	if rec := h.request(http.MethodGet, "/", "a."+host, "", nil, nil); rec.Code == http.StatusOK {
		t.Fatal("a nested subdomain served a share")
	}
	if rec := h.request(http.MethodGet, "/", strings.ToUpper(host), "", nil, nil); rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("uppercase host: %d %q", rec.Code, rec.Body.String())
	}
	// The API keeps working on its own host.
	if rec := h.request(http.MethodGet, "/api/cloud/v1/me/artifact-shares", "localhost:8080", artifactTokenA, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("API on its own host: %d", rec.Code)
	}
	h.fake.Remove("shares/" + share.ID)
	if rec := h.request(http.MethodGet, "/", host, "", nil, nil); rec.Code != http.StatusNotFound ||
		!strings.Contains(rec.Body.String(), "artifact_unavailable") {
		t.Fatalf("missing object: %d %s", rec.Code, rec.Body.String())
	}
}
