package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAttackerHostRejectedBeforeCredentialAttachment(t *testing.T) {
	var attackerHits int32
	attackerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attackerHits, 1)
		fmt.Fprint(w, `{"iid":1,"title":"x","state":"opened"}`)
	}))
	t.Cleanup(attackerSrv.Close)

	allowedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[]`)
	}))
	t.Cleanup(allowedSrv.Close)

	allowedHost := hostFromURL(t, allowedSrv.URL)

	p, err := NewProvider(ProviderOptions{
		Token:              StaticTokenSource("secret-gitlab-token"),
		SkipTokenPreflight: true,
		AllowedHosts:       []string{allowedHost},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.client = NewClient(ClientOptions{
		Token:    StaticTokenSource("secret-gitlab-token"),
		RESTBase: allowedSrv.URL + "/api/v4",
	})
	p.hostClientCfg.HTTPClient = &http.Client{Transport: &rewriteTransport{target: allowedSrv.URL}}

	repo, ok := p.ParseRepository("https://gitlab.attacker.example/group/repo.git")
	if ok {
		t.Fatalf("ParseRepository accepted gitlab.attacker.example (repo=%+v); want rejection — host is not in the allowlist", repo)
	}

	attackerRepo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.attacker.example", Owner: "group", Name: "repo", Repo: "group/repo"}
	ref := ports.SCMPRRef{Repo: attackerRepo, Number: 1, URL: "https://gitlab.attacker.example/group/repo/-/merge_requests/1"}
	if _, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref}); err == nil {
		t.Fatal("FetchPullRequests: error = nil, want error for attacker host")
	}

	if got := atomic.LoadInt32(&attackerHits); got != 0 {
		t.Errorf("attacker server received %d requests; want 0 — credential must not be attached to non-allowlisted hosts", got)
	}
}

func TestPerHostCredentialIsolation(t *testing.T) {
	type tokenRecord struct {
		host  string
		token string
	}
	mu := make(chan tokenRecord, 16)

	comSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu <- tokenRecord{host: "gitlab.com", token: r.Header.Get("Authorization")}
		json.NewEncoder(w).Encode([]any{})
	}))
	t.Cleanup(comSrv.Close)

	selfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu <- tokenRecord{host: "self-managed", token: r.Header.Get("Authorization")}
		json.NewEncoder(w).Encode([]any{})
	}))
	t.Cleanup(selfSrv.Close)

	selfHost := hostFromURL(t, selfSrv.URL)

	p, err := NewProvider(ProviderOptions{
		Token:              StaticTokenSource("com-secret"),
		SkipTokenPreflight: true,
		AllowedHosts:       []string{selfHost},
		HostTokens: map[string]TokenSource{
			selfHost: StaticTokenSource("self-secret"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.client = NewClient(ClientOptions{
		Token:    StaticTokenSource("com-secret"),
		RESTBase: comSrv.URL + "/api/v4",
	})
	p.hostClientCfg.HTTPClient = &http.Client{Transport: &hostRoutingTransport{
		routes: map[string]string{selfHost: selfSrv.URL},
	}}

	selfRepo := ports.SCMRepo{Provider: "gitlab", Host: selfHost, Owner: "eng", Name: "widget", Repo: "eng/widget"}
	if _, err := p.ListPRsByRepo(context.Background(), selfRepo, time.Time{}); err != nil {
		t.Fatalf("ListPRsByRepo(self-managed): %v", err)
	}

	comRepo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	if _, err := p.ListPRsByRepo(context.Background(), comRepo, time.Time{}); err != nil {
		t.Fatalf("ListPRsByRepo(gitlab.com): %v", err)
	}

	close(mu)
	var records []tokenRecord
	for r := range mu {
		records = append(records, r)
	}

	var selfTok string
	var comTok string
	for _, r := range records {
		if r.host == "self-managed" {
			selfTok = r.token
		}
		if r.host == "gitlab.com" {
			comTok = r.token
		}
	}
	if selfTok != "Bearer self-secret" {
		t.Errorf("self-managed host Authorization = %q, want %q (gitlab.com token must NOT leak to self-managed host)", selfTok, "Bearer self-secret")
	}
	if comTok != "Bearer com-secret" {
		t.Errorf("gitlab.com host Authorization = %q, want %q (self-managed token must NOT leak to gitlab.com)", comTok, "Bearer com-secret")
	}
}

func TestSelfManagedCustomPortRESTBase(t *testing.T) {
	var seenPath string
	var seenHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenHost = r.Host
		json.NewEncoder(w).Encode([]any{})
	}))
	t.Cleanup(srv.Close)

	const customPortHost = "gitlab.internal:8443"

	p, err := NewProvider(ProviderOptions{
		Token:              StaticTokenSource("tok"),
		SkipTokenPreflight: true,
		AllowedHosts:       []string{customPortHost},
		HostTokens: map[string]TokenSource{
			customPortHost: StaticTokenSource("tok"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.client = NewClient(ClientOptions{
		Token:    StaticTokenSource("tok"),
		RESTBase: srv.URL + "/api/v4",
	})
	p.hostClientCfg.HTTPClient = &http.Client{Transport: &rewriteTransport{target: srv.URL}}

	repo := ports.SCMRepo{Provider: "gitlab", Host: customPortHost, Owner: "eng", Name: "widget", Repo: "eng/widget"}
	if _, err := p.ListPRsByRepo(context.Background(), repo, time.Time{}); err != nil {
		t.Fatalf("ListPRsByRepo: %v", err)
	}

	if !strings.HasPrefix(seenPath, "/api/v4/") {
		t.Errorf("request path = %q, want /api/v4/... prefix (REST base must include port)", seenPath)
	}
	if seenHost == "gitlab.com" {
		t.Errorf("request host = gitlab.com, want the custom-port self-managed host (port must be preserved)")
	}
}

func TestSSHRemoteParsesToSameHostPortAsHTTPS(t *testing.T) {
	const customPortHost = "gitlab.internal:8443"

	p, err := NewProvider(ProviderOptions{
		Token:              StaticTokenSource("tok"),
		SkipTokenPreflight: true,
		AllowedHosts:       []string{customPortHost},
	})
	if err != nil {
		t.Fatal(err)
	}

	httpsRemote := "https://gitlab.internal:8443/group/repo.git"
	sshRemote := "ssh://git@gitlab.internal:8443/group/repo.git"

	httpsRepo, httpsOK := p.ParseRepository(httpsRemote)
	sshRepo, sshOK := p.ParseRepository(sshRemote)

	if !httpsOK {
		t.Fatalf("ParseRepository(%q) = false, want true (allowlisted host)", httpsRemote)
	}
	if !sshOK {
		t.Fatalf("ParseRepository(%q) = false, want true (ssh:// must parse for allowlisted host)", sshRemote)
	}
	if httpsRepo.Host != customPortHost {
		t.Errorf("HTTPS parse Host = %q, want %q", httpsRepo.Host, customPortHost)
	}
	if sshRepo.Host != customPortHost {
		t.Errorf("SSH parse Host = %q, want %q (ssh:// must produce same host:port as https://)", sshRepo.Host, customPortHost)
	}
	if sshRepo.Owner != httpsRepo.Owner || sshRepo.Name != httpsRepo.Name || sshRepo.Repo != httpsRepo.Repo {
		t.Errorf("SSH parse = %+v, HTTPS parse = %+v; want identical owner/name/repo", sshRepo, httpsRepo)
	}
}

func TestGitLabComAlwaysAllowed(t *testing.T) {
	p, err := NewProvider(ProviderOptions{
		Token:              StaticTokenSource("tok"),
		SkipTokenPreflight: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	repo, ok := p.ParseRepository("git@gitlab.com:myorg/myrepo.git")
	if !ok {
		t.Fatal("ParseRepository(gitlab.com) = false, want true (gitlab.com always allowed)")
	}
	if repo.Host != "gitlab.com" {
		t.Errorf("Host = %q, want gitlab.com", repo.Host)
	}
}

func TestSelfManagedHostNotInAllowlistRejected(t *testing.T) {
	p, err := NewProvider(ProviderOptions{
		Token:              StaticTokenSource("tok"),
		SkipTokenPreflight: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := p.ParseRepository("git@gitlab.mycompany.com:team/project.git"); ok {
		t.Error("ParseRepository(gitlab.mycompany.com) = true, want false (host not in allowlist)")
	}
}

func TestAllowlistedSelfManagedHostAccepted(t *testing.T) {
	p, err := NewProvider(ProviderOptions{
		Token:              StaticTokenSource("tok"),
		SkipTokenPreflight: true,
		AllowedHosts:       []string{"gitlab.mycompany.com"},
	})
	if err != nil {
		t.Fatal(err)
	}

	repo, ok := p.ParseRepository("git@gitlab.mycompany.com:team/project.git")
	if !ok {
		t.Fatal("ParseRepository(allowlisted self-managed) = false, want true")
	}
	if repo.Host != "gitlab.mycompany.com" {
		t.Errorf("Host = %q, want gitlab.mycompany.com", repo.Host)
	}
}

func hostFromURL(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	return u.Host
}

type hostRoutingTransport struct {
	routes map[string]string
}

func (t *hostRoutingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, ok := t.routes[req.URL.Host]
	if !ok {
		return http.DefaultTransport.RoundTrip(req)
	}
	req2 := req.Clone(req.Context())
	u, err := url.Parse(target + req2.URL.Path)
	if err != nil {
		return nil, err
	}
	u.RawQuery = req2.URL.RawQuery
	req2.URL = u
	return http.DefaultTransport.RoundTrip(req2)
}
