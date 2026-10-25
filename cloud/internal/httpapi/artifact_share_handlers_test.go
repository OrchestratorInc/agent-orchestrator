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
	// queue is the sweeper queue of share object versions, keyed by
	// key+"@"+version.
	queue map[string]queuedShareVersion
	// documents maps a user's document key to its share ID.
	documents map[string]string
}

type queuedShareVersion struct {
	version domain.ArtifactObjectVersion
	reason  string
	due     bool
}

func newMemoryShareRows() *memoryShareRows {
	return &memoryShareRows{
		shares:    map[string]domain.ArtifactShare{},
		clock:     time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC),
		queue:     map[string]queuedShareVersion{},
		documents: map[string]string{},
	}
}

func (m *memoryShareRows) queueLocked(key, versionID, reason string, due bool) {
	m.queue[key+"@"+versionID] = queuedShareVersion{
		version: domain.ArtifactObjectVersion{S3Key: key, VersionID: versionID}, reason: reason, due: due,
	}
}

func (m *memoryShareRows) claimUploadLocked(key, versionID string) error {
	entry, ok := m.queue[key+"@"+versionID]
	if !ok || entry.reason != "upload" {
		return postgres.ErrArtifactVersionUnknown
	}
	delete(m.queue, key+"@"+versionID)
	return nil
}

// expireGrace makes every queued version due, as if its grace had passed.
func (m *memoryShareRows) expireGrace() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, entry := range m.queue {
		entry.due = true
		m.queue[name] = entry
	}
}

func (m *memoryShareRows) queued(reason string) []domain.ArtifactObjectVersion {
	m.mu.Lock()
	defer m.mu.Unlock()
	var versions []domain.ArtifactObjectVersion
	for _, entry := range m.queue {
		if entry.reason == reason {
			versions = append(versions, entry.version)
		}
	}
	return versions
}

func (m *memoryShareRows) RecordArtifactShareUpload(_ context.Context, _ string, key, versionID string, _ int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queueLocked(key, versionID, "upload", false)
	return nil
}

func (m *memoryShareRows) DiscardArtifactShareUpload(_ context.Context, _ string, key, versionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry, ok := m.queue[key+"@"+versionID]; ok && entry.reason == "upload" {
		entry.due = true
		m.queue[key+"@"+versionID] = entry
	}
	return nil
}

func (m *memoryShareRows) ClaimArtifactObjectVersions(_ context.Context, limit int, _ time.Duration) ([]domain.ArtifactObjectVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var versions []domain.ArtifactObjectVersion
	for _, entry := range m.queue {
		if entry.due && len(versions) < limit {
			versions = append(versions, entry.version)
		}
	}
	return versions, nil
}

func (m *memoryShareRows) ForgetArtifactObjectVersions(_ context.Context, versions []domain.ArtifactObjectVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, version := range versions {
		delete(m.queue, version.S3Key+"@"+version.VersionID)
	}
	return nil
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

func (m *memoryShareRows) ArtifactSharePublishAllowed(_ context.Context, userID, documentKey string, limit int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, published := m.documents[userID+"|"+documentKey]
	return published || m.countLocked(userID) < limit, nil
}

func (m *memoryShareRows) PublishArtifactShare(_ context.Context, share domain.ArtifactShare, baseSlug, documentKey string, limit int) (domain.ArtifactShare, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existingID, ok := m.documents[share.UserID+"|"+documentKey]; ok {
		if err := m.claimUploadLocked(share.S3Key, share.VersionID); err != nil {
			return domain.ArtifactShare{}, false, err
		}
		current := m.shares[existingID]
		if current.RevokedAt == nil {
			m.queueLocked(current.S3Key, current.VersionID, "replaced", false)
		}
		current.RevokedAt = nil
		m.clock = m.clock.Add(time.Second)
		current.ContentType, current.Size, current.SHA256 = share.ContentType, share.Size, share.SHA256
		current.S3Key, current.VersionID, current.UpdatedAt = share.S3Key, share.VersionID, m.clock
		m.shares[existingID] = current
		return current, true, nil
	}
	if m.countLocked(share.UserID) >= limit {
		return domain.ArtifactShare{}, false, postgres.ErrArtifactShareLimit
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
		if err := m.claimUploadLocked(share.S3Key, share.VersionID); err != nil {
			return domain.ArtifactShare{}, false, err
		}
		m.clock = m.clock.Add(time.Second)
		share.Slug, share.CreatedAt, share.UpdatedAt = slug, m.clock, m.clock
		m.shares[share.ID] = share
		m.documents[share.UserID+"|"+documentKey] = share.ID
		return share, false, nil
	}
	return domain.ArtifactShare{}, false, postgres.ErrConflict
}

func (m *memoryShareRows) UpdateArtifactShare(_ context.Context, share domain.ArtifactShare) (domain.ArtifactShare, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.shares[share.ID]
	if !ok || current.UserID != share.UserID || current.RevokedAt != nil {
		return domain.ArtifactShare{}, postgres.ErrNotFound
	}
	if share.Name != current.Name {
		return domain.ArtifactShare{}, postgres.ErrArtifactShareNameChanged
	}
	if err := m.claimUploadLocked(current.S3Key, share.VersionID); err != nil {
		return domain.ArtifactShare{}, err
	}
	if current.VersionID != share.VersionID {
		m.queueLocked(current.S3Key, current.VersionID, "replaced", false)
	}
	m.clock = m.clock.Add(time.Second)
	current.ContentType, current.Size = share.ContentType, share.Size
	current.SHA256, current.VersionID, current.UpdatedAt = share.SHA256, share.VersionID, m.clock
	m.shares[share.ID] = current
	return current, nil
}

func (m *memoryShareRows) GetArtifactShare(_ context.Context, userID, shareID string) (domain.ArtifactShare, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	share, ok := m.shares[shareID]
	if !ok || share.UserID != userID || share.RevokedAt != nil {
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

func (m *memoryShareRows) RevokeArtifactShare(_ context.Context, userID, shareID string) (domain.ArtifactShare, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	share, ok := m.shares[shareID]
	if !ok || share.UserID != userID || share.RevokedAt != nil {
		return domain.ArtifactShare{}, postgres.ErrNotFound
	}
	m.clock = m.clock.Add(time.Second)
	revokedAt := m.clock
	share.RevokedAt = &revokedAt
	m.shares[shareID] = share
	m.queueLocked(share.S3Key, share.VersionID, "replaced", false)
	return share, nil
}

func (m *memoryShareRows) ArtifactShareBySlug(_ context.Context, slug string) (domain.ArtifactShare, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, share := range m.shares {
		if share.Slug == slug && share.RevokedAt == nil {
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
	t         *testing.T
	server    *Server
	rows      *memoryShareRows
	fake      *s3fake.Server
	artifacts *artifactstore.Store
}

// sweepAfterGrace runs the artifact sweeper once every queued version is due.
func (h *shareHarness) sweepAfterGrace() error {
	h.t.Helper()
	h.rows.expireGrace()
	_, err := artifactstore.Sweep(context.Background(), h.rows, h.artifacts)
	return err
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
	return &shareHarness{t: t, server: server, rows: rows, fake: fake, artifacts: artifacts}
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
		!strings.HasPrefix(headers.Get("ETag"), `"`+hexSHA(html)+"-") ||
		!strings.HasPrefix(headers.Get("Content-Security-Policy"), "sandbox ") ||
		strings.Contains(headers.Get("Content-Security-Policy"), "allow-same-origin") ||
		headers.Get("Set-Cookie") != "" {
		t.Fatalf("headers = %v", headers)
	}
	if rec := h.visit(share.URL, map[string]string{"If-None-Match": headers.Get("ETag")}); rec.Code != http.StatusNotModified {
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
	if versions := h.fake.Versions("shares/" + share.ID); versions != 3 {
		t.Fatalf("stored versions before the sweep = %d, want all 3 kept for in-flight reads", versions)
	}
	if replaced := h.rows.queued("replaced"); len(replaced) != 2 {
		t.Fatalf("queued replaced versions = %d, want 2", len(replaced))
	}
	if err := h.sweepAfterGrace(); err != nil {
		t.Fatal(err)
	}
	if versions := h.fake.Versions("shares/" + share.ID); versions != 1 {
		t.Fatalf("stored versions after the sweep = %d, want only the live one", versions)
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
	if err := h.sweepAfterGrace(); err != nil {
		t.Fatal(err)
	}
	if versions := h.fake.Versions("shares/" + shares[0].ID); versions != 0 {
		t.Fatalf("revoked share kept %d stored versions after the sweep", versions)
	}
	// The revoked link keeps its slot, so a new document is still refused.
	if rec := h.publish(artifactTokenA, "four.html", "proj", "four", []byte("four")); rec.Code != http.StatusConflict {
		t.Fatalf("new document after a revoke: %d, want the limit", rec.Code)
	}
	if rec := h.request(http.MethodDelete, "/api/cloud/v1/me/artifact-shares/"+shares[0].ID, "", artifactTokenA, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("revoking twice: %d", rec.Code)
	}
	if rec := h.republish(artifactTokenA, shares[0].ID, "one.html", []byte("x")); rec.Code != http.StatusNotFound {
		t.Fatalf("PUT on a revoked link: %d", rec.Code)
	}
	list := h.request(http.MethodGet, "/api/cloud/v1/me/artifact-shares", "", artifactTokenA, nil, nil)
	listed := decodeBody[struct {
		Shares []artifactShareResponse `json:"shares"`
		Used   int                     `json:"used"`
	}](t, list)
	revoked := 0
	for _, share := range listed.Shares {
		if share.RevokedAt != nil {
			revoked++
		}
	}
	if listed.Used != 3 || revoked != 1 {
		t.Fatalf("list after revoke: used %d, revoked %d", listed.Used, revoked)
	}
	// Publishing the revoked document again restores its own link.
	restored := h.publish(artifactTokenA, "one.html", "proj", "one.html", []byte("back"))
	if restored.Code != http.StatusOK {
		t.Fatalf("republishing the revoked document: %d %s", restored.Code, restored.Body.String())
	}
	if share := decodeBody[struct {
		Share artifactShareResponse `json:"share"`
	}](t, restored).Share; share.ID != shares[0].ID || share.RevokedAt != nil {
		t.Fatalf("restored share = %+v", share)
	}
	if page := h.visit(shares[0].URL, nil); page.Code != http.StatusOK || page.Body.String() != "back" {
		t.Fatalf("restored link: %d %q", page.Code, page.Body.String())
	}
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
	if used, _ := h.rows.ListArtifactShares(context.Background(), artifactUserA); len(used) != 0 {
		t.Fatalf("rejected uploads created %d shares", len(used))
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

// A delete that fails stays queued and is retried by the next sweep, so a
// revoked share's bytes are not kept forever.
func TestArtifactShareRevokeRetriesFailedDeletes(t *testing.T) {
	h := newShareHarness(t, &ArtifactShareOptions{Origin: shareOrigin, Limit: 3})
	share := h.mustPublish(artifactTokenA, "report.html", "proj", "feat", []byte("secret"))
	h.fake.FailDeletes(true)
	if rec := h.request(http.MethodDelete, "/api/cloud/v1/me/artifact-shares/"+share.ID, "", artifactTokenA, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body.String())
	}
	if err := h.sweepAfterGrace(); err == nil {
		t.Fatal("sweep reported success while deletes fail")
	}
	if versions := h.fake.Versions("shares/" + share.ID); versions != 1 || len(h.rows.queued("replaced")) != 1 {
		t.Fatalf("after a failed delete: %d stored versions, %d queued", versions, len(h.rows.queued("replaced")))
	}
	h.fake.FailDeletes(false)
	if err := h.sweepAfterGrace(); err != nil {
		t.Fatal(err)
	}
	if versions := h.fake.Versions("shares/" + share.ID); versions != 0 || len(h.rows.queued("replaced")) != 0 {
		t.Fatalf("after recovery: %d stored versions, %d queued", versions, len(h.rows.queued("replaced")))
	}
}

// A read that loaded the share row before a republish can still fetch the
// version it named until the grace period ends.
func TestArtifactShareReplacedVersionOutlivesRepublish(t *testing.T) {
	h := newShareHarness(t, &ArtifactShareOptions{Origin: shareOrigin, Limit: 3})
	share := h.mustPublish(artifactTokenA, "report.html", "proj", "feat", []byte("v1"))
	before, err := h.rows.ArtifactShareBySlug(context.Background(), share.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if rec := h.republish(artifactTokenA, share.ID, "report.html", []byte("v2")); rec.Code != http.StatusOK {
		t.Fatalf("republish: %d", rec.Code)
	}
	object, err := h.artifacts.Get(context.Background(), before.S3Key, before.VersionID)
	if err != nil {
		t.Fatalf("in-flight read of the replaced version: %v", err)
	}
	body, _ := io.ReadAll(object.Body)
	object.Body.Close()
	if string(body) != "v1" {
		t.Fatalf("replaced version body = %q", body)
	}
}

// Two documents whose project and feature only collide in the lossy URL slug
// get two links; the second never replaces the first.
func TestArtifactShareSlugCollisionsStaySeparateDocuments(t *testing.T) {
	h := newShareHarness(t, &ArtifactShareOptions{Origin: shareOrigin, Limit: 3})
	first := h.mustPublish(artifactTokenA, "index.html", "a-b", "c", []byte("first"))
	second := h.publish(artifactTokenA, "index.html", "a", "b-c", []byte("second"))
	body := decodeBody[struct {
		Share    artifactShareResponse `json:"share"`
		Replaced bool                  `json:"replaced"`
	}](t, second)
	if second.Code != http.StatusCreated || body.Replaced || body.Share.ID == first.ID {
		t.Fatalf("second document = %d %+v, want a new link", second.Code, body)
	}
	if body.Share.Slug != first.Slug+"-2" {
		t.Fatalf("second slug = %q, want %q", body.Share.Slug, first.Slug+"-2")
	}
	if page := h.visit(first.URL, nil); page.Body.String() != "first" {
		t.Fatalf("first link now serves %q", page.Body.String())
	}
	if replaced := h.rows.queued("replaced"); len(replaced) != 0 {
		t.Fatalf("a separate document queued %d versions for deletion", len(replaced))
	}
	// Publishing the first document again still replaces only its own link.
	again := h.publish(artifactTokenA, "index.html", "a-b", "c", []byte("first v2"))
	if decodeBody[struct {
		Share artifactShareResponse `json:"share"`
	}](t, again).Share.ID != first.ID {
		t.Fatal("republishing the first document did not update its own link")
	}
	if page := h.visit(body.Share.URL, nil); page.Body.String() != "second" {
		t.Fatalf("second link now serves %q", page.Body.String())
	}
}

// A link's file name is part of its document, so PUT cannot rename it: the
// renamed file is a new document, and the old name still names this link.
func TestArtifactSharePutRejectsAnotherFileName(t *testing.T) {
	h := newShareHarness(t, &ArtifactShareOptions{Origin: shareOrigin, Limit: 3})
	old := h.mustPublish(artifactTokenA, "old.html", "p", "f", []byte("old"))
	putsBefore := h.fake.Puts()

	rec := h.republish(artifactTokenA, old.ID, "new.html", []byte("new"))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "artifact_share_name_changed") {
		t.Fatalf("PUT with another name: %d %s", rec.Code, rec.Body.String())
	}
	if h.fake.Puts() != putsBefore {
		t.Fatal("a refused rename reached S3")
	}
	if page := h.visit(old.URL, nil); page.Body.String() != "old" {
		t.Fatalf("link serves %q after a refused rename", page.Body.String())
	}

	// new.html is its own document: a new link, which takes a slot.
	renamed := h.mustPublish(artifactTokenA, "new.html", "p", "f", []byte("new"))
	if renamed.ID == old.ID || renamed.Name != "new.html" {
		t.Fatalf("publishing new.html = %+v, want a new link", renamed)
	}
	// old.html still names the original link, which keeps serving that file.
	again := h.publish(artifactTokenA, "old.html", "p", "f", []byte("old v2"))
	body := decodeBody[struct {
		Share    artifactShareResponse `json:"share"`
		Replaced bool                  `json:"replaced"`
	}](t, again)
	if again.Code != http.StatusOK || !body.Replaced || body.Share.ID != old.ID || body.Share.Name != "old.html" {
		t.Fatalf("publishing old.html again = %d %+v", again.Code, body)
	}
	if page := h.visit(renamed.URL, nil); page.Body.String() != "new" {
		t.Fatalf("new.html link serves %q", page.Body.String())
	}

	// The same name is still accepted.
	if rec := h.republish(artifactTokenA, old.ID, "old.html", []byte("old v3")); rec.Code != http.StatusOK {
		t.Fatalf("PUT with the same name: %d %s", rec.Code, rec.Body.String())
	}
}

// The share ETag covers the Content-Type as well as the bytes.
func TestShareETagChangesWithContentType(t *testing.T) {
	share := domain.ArtifactShare{SHA256: strings.Repeat("a", 64), ContentType: "text/plain; charset=utf-8"}
	html := share
	html.ContentType = "text/html; charset=utf-8"
	if shareETag(share) == shareETag(html) {
		t.Fatal("identical bytes with another Content-Type kept the same ETag")
	}
	if !strings.HasPrefix(shareETag(share), `"`+share.SHA256+"-") {
		t.Fatalf("ETag = %s", shareETag(share))
	}
}

// A share upload may keep streaming past the server's ReadTimeout.
func TestArtifactShareUploadOutlivesServerReadTimeout(t *testing.T) {
	h := newShareHarness(t, &ArtifactShareOptions{Origin: shareOrigin, Limit: 3})
	const readTimeout = 200 * time.Millisecond
	listener := httptest.NewUnstartedServer(h.server.Handler())
	listener.Config.ReadTimeout = readTimeout
	listener.Start()
	defer listener.Close()

	body := []byte("slow but complete")
	reader, writer := io.Pipe()
	go func() {
		_, _ = writer.Write(body[:4])
		time.Sleep(3 * readTimeout)
		_, _ = writer.Write(body[4:])
		_ = writer.Close()
	}()
	req, err := http.NewRequest(http.MethodPost, listener.URL+"/api/cloud/v1/me/artifact-shares?name=slow.html&project=p&feature=f", reader)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Authorization", "Bearer "+artifactTokenA)
	req.Header.Set(worker.ArtifactSHA256Header, hexSHA(body))
	resp, err := listener.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("slow upload: %d %s", resp.StatusCode, payload)
	}
}

// Publishing the same document again replaces the version its link serves:
// the URL and slot stay, the earlier version is no longer served, and it is
// queued for the sweeper.
func TestArtifactSharePublishingTheSameDocumentReplacesItsLink(t *testing.T) {
	h := newShareHarness(t, &ArtifactShareOptions{Origin: shareOrigin, Limit: 1})
	first := h.mustPublish(artifactTokenA, "report.html", "proj", "feat", []byte("v1"))

	again := h.publish(artifactTokenA, "report.html", "proj", "feat", []byte("v2"))
	if again.Code != http.StatusOK {
		t.Fatalf("republishing the same document: %d %s", again.Code, again.Body.String())
	}
	body := decodeBody[struct {
		Share    artifactShareResponse `json:"share"`
		Replaced bool                  `json:"replaced"`
	}](t, again)
	if !body.Replaced || body.Share.ID != first.ID || body.Share.URL != first.URL {
		t.Fatalf("republish = %+v, want the first link replaced", body)
	}
	if page := h.visit(first.URL, nil); page.Body.String() != "v2" {
		t.Fatalf("link served %q, want only the new version", page.Body.String())
	}
	if replaced := h.rows.queued("replaced"); len(replaced) != 1 || replaced[0].S3Key != "shares/"+first.ID {
		t.Fatalf("queued replaced versions = %+v, want the first version", replaced)
	}
	listed, _ := h.rows.ListArtifactShares(context.Background(), artifactUserA)
	if len(listed) != 1 {
		t.Fatalf("user holds %d links, want 1", len(listed))
	}

	// A different file, or the same file under another feature, is another
	// document, so it needs a free slot.
	for _, other := range []struct{ name, feature string }{{"notes.html", "feat"}, {"report.html", "other"}} {
		if rec := h.publish(artifactTokenA, other.name, "proj", other.feature, []byte("x")); rec.Code != http.StatusConflict {
			t.Fatalf("%s under %s: %d, want the limit", other.name, other.feature, rec.Code)
		}
	}
	// Another user publishing the same names gets their own link.
	theirs := h.mustPublish(artifactTokenB, "report.html", "proj", "feat", []byte("b"))
	if theirs.ID == first.ID {
		t.Fatal("another user's publish replaced this user's link")
	}
	if page := h.visit(first.URL, nil); page.Body.String() != "v2" {
		t.Fatalf("first link now serves %q", page.Body.String())
	}

	// After a revoke, publishing the document again restores its link.
	if rec := h.request(http.MethodDelete, "/api/cloud/v1/me/artifact-shares/"+first.ID, "", artifactTokenA, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rec.Code)
	}
	restored := h.publish(artifactTokenA, "report.html", "proj", "feat", []byte("v3"))
	if restored.Code != http.StatusOK {
		t.Fatalf("publishing after a revoke: %d %s", restored.Code, restored.Body.String())
	}
	if share := decodeBody[struct {
		Share artifactShareResponse `json:"share"`
	}](t, restored).Share; share.ID != first.ID {
		t.Fatalf("publishing after a revoke made link %s, want %s restored", share.ID, first.ID)
	}
}
