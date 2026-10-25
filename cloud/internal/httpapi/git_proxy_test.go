package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/githubapp"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

const (
	gitProxyTestSession = "11111111-2222-3333-4444-555555555555"
	gitProxyTestToken   = "ghs_proxyinstallationtoken"
)

// gitProxyStore answers whether the worker is still the session's current one.
type gitProxyStore struct {
	Store
	stale bool
}

func (s *gitProxyStore) WorkerConnectionCurrent(context.Context, string, string, string, int64) (bool, error) {
	return !s.stale, nil
}

// gitProxyBroker grants the session acme/app (read and write) and acme/docs
// (read only), and records what the proxy asked for.
type gitProxyBroker struct {
	recordingCheckoutBroker
	mu       sync.Mutex
	requests []string
}

func (b *gitProxyBroker) IssueGitProxyGrant(_ context.Context, orgID, sessionID, repo string, push bool) (githubapp.CheckoutGrant, error) {
	b.mu.Lock()
	b.requests = append(b.requests, fmt.Sprintf("%s %s push=%v", sessionID, repo, push))
	b.mu.Unlock()
	if orgID != testOrgID || sessionID != gitProxyTestSession {
		return githubapp.CheckoutGrant{}, postgres.ErrForbidden
	}
	switch {
	case repo == "acme/app", repo == "acme/docs" && !push:
		return githubapp.CheckoutGrant{Token: gitProxyTestToken, ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	return githubapp.CheckoutGrant{}, postgres.ErrForbidden
}

// fakeGitHub is git http-backend behind GitHub's credential check: it serves
// only requests carrying the installation token and records every
// Authorization header it sees.
type fakeGitHub struct {
	root     string
	mu       sync.Mutex
	seenAuth []string
	requests int
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.seenAuth = append(g.seenAuth, r.Header.Get("Authorization"))
	g.requests++
	g.mu.Unlock()
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+gitProxyTestToken))
	if r.Header.Get("Authorization") != want {
		w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
		http.Error(w, "bad credentials", http.StatusUnauthorized)
		return
	}
	gitPath, _ := exec.LookPath("git")
	(&cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Env: []string{
			"GIT_PROJECT_ROOT=" + g.root,
			"GIT_HTTP_EXPORT_ALL=1",
			"REMOTE_USER=x-access-token",
		},
	}).ServeHTTP(w, r)
}

type gitProxyHarness struct {
	t        *testing.T
	github   *fakeGitHub
	broker   *gitProxyBroker
	store    *gitProxyStore
	proxyURL string
	token    string
	upstream string
	logs     *bytes.Buffer
}

func newGitProxyHarness(t *testing.T) *gitProxyHarness {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	upstream := filepath.Join(root, "acme", "app.git")
	runGit(t, "", "init", "--bare", "--initial-branch=main", upstream)
	runGit(t, upstream, "config", "http.receivepack", "true")
	seed := t.TempDir()
	runGit(t, seed, "init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "README.md")
	runGit(t, seed, "commit", "-m", "init")
	runGit(t, seed, "push", upstream, "main")

	github := &fakeGitHub{root: root}
	githubServer := httptest.NewServer(github)
	t.Cleanup(githubServer.Close)

	tokens := worker.NewTokenManager([]byte(strings.Repeat("k", 32)))
	token, err := tokens.Issue(worker.Claims{
		OrgID: testOrgID, SessionID: gitProxyTestSession, WorkerID: "w1", Epoch: 1, Scopes: []string{"worker:git"},
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	broker := &gitProxyBroker{}
	store := &gitProxyStore{}
	var logs bytes.Buffer
	server := httptest.NewServer(New(Options{
		Store:          store,
		WorkerTokens:   tokens,
		CheckoutBroker: broker,
		GitUpstreamURL: githubServer.URL,
		Logger:         slog.New(slog.NewTextHandler(&logs, nil)),
	}).Handler())
	t.Cleanup(server.Close)
	return &gitProxyHarness{
		t: t, github: github, broker: broker, store: store, token: token, upstream: upstream, logs: &logs,
		proxyURL: worker.GitProxyBaseURL(server.URL, gitProxyTestSession),
	}
}

// git runs git as the sandbox does: GitHub URLs rewritten to the proxy, and a
// credential helper that answers with the worker token.
func (h *gitProxyHarness) git(dir string, args ...string) (string, error) {
	h.t.Helper()
	base := []string{
		"-c", "url." + h.proxyURL + ".insteadOf=https://github.com/",
		"-c", "credential.helper=",
		"-c", "credential.helper=!f() { echo username=ao-worker; echo password=" + h.token + "; }; f",
		"-c", "protocol.version=0",
	}
	command := exec.Command("git", append(base, args...)...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	output, err := command.CombinedOutput()
	return string(output), err
}

func (h *gitProxyHarness) clone() string {
	h.t.Helper()
	dest := filepath.Join(h.t.TempDir(), "app")
	if output, err := h.git("", "clone", "https://github.com/acme/app.git", dest); err != nil {
		h.t.Fatalf("clone through the proxy: %v\n%s", err, output)
	}
	return dest
}

func (h *gitProxyHarness) assertGitHubSawOnlyTheInstallationToken() {
	h.t.Helper()
	h.github.mu.Lock()
	defer h.github.mu.Unlock()
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+gitProxyTestToken))
	for _, auth := range h.github.seenAuth {
		if auth != want {
			h.t.Fatalf("GitHub received Authorization %q; the worker's credential must never be forwarded", auth)
		}
		if strings.Contains(auth, h.token) {
			h.t.Fatal("the worker token reached GitHub")
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=AO Test", "GIT_AUTHOR_EMAIL=ao@example.test",
		"GIT_COMMITTER_NAME=AO Test", "GIT_COMMITTER_EMAIL=ao@example.test")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

// A clone, a fetch and a branch push all go through the proxy with the
// installation token the control plane holds; GitHub never sees the worker's
// credential, and the sandbox never sees GitHub's.
func TestGitProxyClonesFetchesAndPushesWithTheControlPlanesCredential(t *testing.T) {
	h := newGitProxyHarness(t)
	checkout := h.clone()
	if got := runGit(t, checkout, "config", "--get", "remote.origin.url"); got != "https://github.com/acme/app.git" {
		t.Fatalf("origin = %q, want the GitHub URL kept as configured", got)
	}
	if err := os.WriteFile(filepath.Join(checkout, "CHANGE.md"), []byte("change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, checkout, "add", "CHANGE.md")
	runGit(t, checkout, "commit", "-m", "change")
	if output, err := h.git(checkout, "push", "origin", "HEAD:refs/heads/ao/session"); err != nil {
		t.Fatalf("push through the proxy: %v\n%s", err, output)
	}
	if output, err := h.git(checkout, "fetch", "origin"); err != nil {
		t.Fatalf("fetch through the proxy: %v\n%s", err, output)
	}
	head := runGit(t, checkout, "rev-parse", "HEAD")
	if pushed := runGit(t, h.upstream, "rev-parse", "refs/heads/ao/session"); pushed != head {
		t.Fatalf("GitHub has ao/session at %s, want %s", pushed, head)
	}
	h.assertGitHubSawOnlyTheInstallationToken()
	h.broker.mu.Lock()
	defer h.broker.mu.Unlock()
	var sawPush, sawFetch bool
	for _, request := range h.broker.requests {
		sawPush = sawPush || request == gitProxyTestSession+" acme/app push=true"
		sawFetch = sawFetch || request == gitProxyTestSession+" acme/app push=false"
	}
	if !sawPush || !sawFetch {
		t.Fatalf("broker requests = %v, want a fetch and a push grant for acme/app", h.broker.requests)
	}
	if strings.Contains(h.logs.String(), gitProxyTestToken) || strings.Contains(h.logs.String(), h.token) {
		t.Fatal("a credential was logged")
	}
}

// The proxy refuses a tag push and a branch deletion with a message git shows,
// before anything reaches GitHub.
func TestGitProxyRefusesTagsAndDeletions(t *testing.T) {
	h := newGitProxyHarness(t)
	checkout := h.clone()
	runGit(t, checkout, "tag", "v1")
	output, err := h.git(checkout, "push", "origin", "refs/tags/v1")
	if err == nil {
		t.Fatalf("tag push succeeded:\n%s", output)
	}
	if !strings.Contains(output, "AO lets a session push branches only, not refs/tags/v1") {
		t.Fatalf("tag push output does not explain the refusal:\n%s", output)
	}
	if _, err := exec.Command("git", "--git-dir", h.upstream, "rev-parse", "--verify", "refs/tags/v1").Output(); err == nil {
		t.Fatal("the tag reached GitHub")
	}
	output, err = h.git(checkout, "push", "origin", ":refs/heads/main")
	if err == nil || !strings.Contains(output, "AO does not let a session delete refs/heads/main") {
		t.Fatalf("branch deletion = %v:\n%s", err, output)
	}
	if got := runGit(t, h.upstream, "rev-parse", "refs/heads/main"); got == "" {
		t.Fatal("main was deleted")
	}
	h.assertGitHubSawOnlyTheInstallationToken()
}

// A repository outside the session's grant, a read-only repository's push, and
// another session's path are refused before any request reaches GitHub.
func TestGitProxyRefusesWhatTheSessionMayNotReach(t *testing.T) {
	h := newGitProxyHarness(t)
	if output, err := h.git("", "clone", "https://github.com/acme/secret.git", filepath.Join(t.TempDir(), "secret")); err == nil {
		t.Fatalf("clone of an ungranted repository succeeded:\n%s", output)
	} else if !strings.Contains(output, "403") {
		t.Fatalf("ungranted clone output:\n%s", output)
	}
	if h.github.requests != 0 {
		t.Fatalf("GitHub received %d requests for an ungranted repository", h.github.requests)
	}

	other := worker.GitProxyBaseURL(strings.TrimSuffix(h.proxyURL, worker.GitProxyRoutePrefix+gitProxyTestSession+"/"), "99999999-0000-0000-0000-000000000000")
	request, _ := http.NewRequest(http.MethodGet, other+"acme/app.git/info/refs?service=git-upload-pack", nil)
	request.SetBasicAuth("ao-worker", h.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("another session's path = %d, want 403", response.StatusCode)
	}

	request, _ = http.NewRequest(http.MethodGet, h.proxyURL+"acme/docs.git/info/refs?service=git-receive-pack", nil)
	request.SetBasicAuth("ao-worker", h.token)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("push to a read-only repository = %d, want 403", response.StatusCode)
	}
	if h.github.requests != 0 {
		t.Fatalf("GitHub received %d requests for refused operations", h.github.requests)
	}
}

// Without a worker credential the proxy asks git for one (which runs the
// session credential helper); a replaced worker's credential is refused.
func TestGitProxyAuthenticatesTheWorker(t *testing.T) {
	h := newGitProxyHarness(t)
	target := h.proxyURL + "acme/app.git/info/refs?service=git-upload-pack"
	response, err := http.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(response.Header.Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("anonymous request = %d %q, want a Basic challenge", response.StatusCode, response.Header.Get("WWW-Authenticate"))
	}
	for name, password := range map[string]string{"forged token": "not-a-worker-token", "empty": ""} {
		request, _ := http.NewRequest(http.MethodGet, target, nil)
		request.SetBasicAuth("ao-worker", password)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s = %d, want 401", name, response.StatusCode)
		}
	}
	h.store.stale = true
	request, _ := http.NewRequest(http.MethodGet, target, nil)
	request.SetBasicAuth("ao-worker", h.token)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), "replaced") {
		t.Fatalf("replaced worker = %d %q, want 401", response.StatusCode, body)
	}
	if h.github.requests != 0 {
		t.Fatalf("GitHub received %d unauthenticated requests", h.github.requests)
	}
}

// Only the smart-HTTP endpoints are proxied.
func TestParseGitProxyPath(t *testing.T) {
	for _, test := range []struct {
		method, path, service string
		ok, push              bool
	}{
		{http.MethodGet, "acme/app.git/info/refs", "git-upload-pack", true, false},
		{http.MethodGet, "acme/app/info/refs", "git-receive-pack", true, true},
		{http.MethodPost, "acme/app.git/git-upload-pack", "", true, false},
		{http.MethodPost, "acme/app.git/git-receive-pack", "", true, true},
		{http.MethodGet, "acme/app.git/info/refs", "", false, false},
		{http.MethodGet, "acme/app.git/HEAD", "", false, false},
		{http.MethodGet, "acme/app.git/objects/info/packs", "", false, false},
		{http.MethodPost, "acme/app.git/info/lfs/objects/batch", "", false, false},
		{http.MethodGet, "acme/app.git/git-upload-pack", "", false, false},
		{http.MethodGet, "../app.git/info/refs", "git-upload-pack", false, false},
		{http.MethodGet, "acme/..git/info/refs", "git-upload-pack", false, false},
		{http.MethodGet, "acme%2Fx/app.git/info/refs", "git-upload-pack", false, false},
		{http.MethodGet, "acme", "git-upload-pack", false, false},
	} {
		request, ok := parseGitProxyPath(test.method, test.path, test.service)
		if ok != test.ok || (ok && request.push != test.push) {
			t.Errorf("parseGitProxyPath(%s %s ?service=%s) = %+v, %v; want ok=%v push=%v",
				test.method, test.path, test.service, request, ok, test.ok, test.push)
		}
	}
}

// The command check replays everything it read, so the pack streams on intact.
func TestCheckGitPushCommandsReplaysTheRequest(t *testing.T) {
	zero := strings.Repeat("0", 40)
	sha := strings.Repeat("a", 40)
	pkt := func(line string) string { return fmt.Sprintf("%04x%s", len(line)+4, line) }
	request := pkt(zero+" "+sha+" refs/heads/ao/x\x00report-status side-band-64k\n") +
		pkt(sha+" "+sha+" refs/heads/ao/y\n") + "0000" + "PACK-bytes-follow"
	body, refusal, err := checkGitPushCommands("", strings.NewReader(request))
	if err != nil || refusal != "" {
		t.Fatalf("check = %q, %v", refusal, err)
	}
	replayed, _ := io.ReadAll(body)
	if string(replayed) != request {
		t.Fatalf("replayed %q, want the request unchanged", replayed)
	}
	for _, refused := range []string{
		pkt(sha+" "+zero+" refs/heads/main\x00report-status\n") + "0000",
		pkt(zero+" "+sha+" refs/tags/v1\x00report-status\n") + "0000",
		pkt(zero+" "+sha+" refs/pull/1/head\x00report-status\n") + "0000",
	} {
		if _, refusal, err := checkGitPushCommands("", strings.NewReader(refused)); err != nil || refusal == "" {
			t.Fatalf("check(%q) = %q, %v; want a refusal", refused, refusal, err)
		}
	}
	if _, refusal, _ := checkGitPushCommands("gzip", strings.NewReader(request)); refusal == "" {
		t.Fatal("a compressed push was accepted unchecked")
	}
	if _, _, err := checkGitPushCommands("", strings.NewReader("zzzz")); err == nil {
		t.Fatal("a malformed push was accepted")
	}
}

// A large push arrives chunked (git streams packs over http.postBuffer): the
// proxy streams it to GitHub byte for byte, with the checked commands replayed
// ahead of the pack.
func TestGitProxyStreamsAChunkedPushIntact(t *testing.T) {
	tokens := worker.NewTokenManager([]byte(strings.Repeat("k", 32)))
	token, err := tokens.Issue(worker.Claims{
		OrgID: testOrgID, SessionID: gitProxyTestSession, WorkerID: "w1", Epoch: 1, Scopes: []string{"worker:git"},
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	zero := strings.Repeat("0", 40)
	sha := strings.Repeat("b", 40)
	line := zero + " " + sha + " refs/heads/ao/big\x00report-status side-band-64k\n"
	commands := fmt.Sprintf("%04x%s0000", len(line)+4, line)
	pack := bytes.Repeat([]byte("PACKDATA"), 1<<20) // 8 MiB
	var received []byte
	var chunked bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunked = len(r.TransferEncoding) > 0 && r.TransferEncoding[0] == "chunked"
		received, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
		_, _ = io.WriteString(w, "0000")
	}))
	defer upstream.Close()
	server := httptest.NewServer(New(Options{
		Store: &gitProxyStore{}, WorkerTokens: tokens, CheckoutBroker: &gitProxyBroker{},
		GitUpstreamURL: upstream.URL, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Handler())
	defer server.Close()

	body := io.MultiReader(strings.NewReader(commands), bytes.NewReader(pack))
	request, _ := http.NewRequest(http.MethodPost,
		worker.GitProxyBaseURL(server.URL, gitProxyTestSession)+"acme/app.git/git-receive-pack", io.NopCloser(body))
	request.Header.Set("Content-Type", "application/x-git-receive-pack-request")
	request.SetBasicAuth("ao-worker", token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if !chunked {
		t.Fatal("the push of unknown length was not streamed chunked to GitHub")
	}
	if want := append([]byte(commands), pack...); !bytes.Equal(received, want) {
		t.Fatalf("GitHub received %d bytes, want the %d bytes sent", len(received), len(want))
	}
}
