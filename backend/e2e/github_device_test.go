//go:build !windows

package e2e

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/systemcheck"
)

// This opt-in matrix runs released gh binaries through the real daemon. Only
// GitHub's OAuth/API responses are local fixtures; no account or keychain is used.
func TestGitHubDeviceLoginCLICompatibility(t *testing.T) {
	binaries := filepath.SplitList(os.Getenv("AO_GITHUB_E2E_BINARIES"))
	if len(binaries) == 0 {
		t.Skip("set AO_GITHUB_E2E_BINARIES to released gh binary paths")
	}
	for _, binary := range binaries {
		version, err := exec.Command(binary, "--version").Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, outcome := range []string{"approve", "deny", "expire", "cancel", "storage_failure", "revoke"} {
			t.Run(strings.Split(string(version), "\n")[0]+"/"+outcome, func(t *testing.T) {
				fixture := newGitHubDeviceFixture(t, binary)
				login := fixture.request(t, "POST")
				if login.State != systemcheck.GitHubDeviceLoginAwaitingApproval || login.UserCode != "ABCD-1234" || login.VerificationURL != "https://github.com/login/device" {
					t.Fatalf("login = %+v", login)
				}
				if again := fixture.request(t, "POST"); again.ID != login.ID {
					t.Fatal("concurrent start created a second attempt")
				}
				if outcome == "cancel" {
					if cancelled := fixture.request(t, "DELETE"); cancelled.State != systemcheck.GitHubDeviceLoginCancelled {
						t.Fatalf("cancel = %+v", cancelled)
					}
					next := fixture.request(t, "POST")
					if next.ID == login.ID {
						t.Fatal("retry reused cancelled attempt")
					}
					fixture.request(t, "DELETE")
					return
				}
				switch outcome {
				case "storage_failure":
					if err := os.Chmod(fixture.configDir, 0o500); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(fixture.configDir, 0o700) })
					fixture.outcome.Store("approve")
				case "revoke":
					fixture.outcome.Store("approve")
				default:
					fixture.outcome.Store(outcome)
				}
				want := systemcheck.GitHubDeviceLoginFailed
				if outcome == "approve" || outcome == "revoke" {
					want = systemcheck.GitHubDeviceLoginSucceeded
				}
				fixture.await(t, want)
				var auth systemcheck.Requirement
				status, err := fixture.daemon.call("GET", "/system/github-auth", nil, &auth)
				if err != nil || status != 200 || auth.Satisfied != (want == systemcheck.GitHubDeviceLoginSucceeded) {
					t.Fatalf("auth = %+v, status = %d, err = %v", auth, status, err)
				}
				if outcome == "revoke" {
					fixture.rejected.Store(true)
					status, err = fixture.daemon.call("GET", "/system/github-auth", nil, &auth)
					if err != nil || status != 200 || auth.Satisfied {
						t.Fatalf("revoked credential accepted: %+v, err=%v", auth, err)
					}
				}
			})
		}
	}
}

type githubDeviceFixture struct {
	daemon          *daemon
	configDir       string
	versionOverride string
	outcome         atomic.Value
	rejected        atomic.Bool
	codeDelay       atomic.Int64
	starts          atomic.Int64
}

func (f *githubDeviceFixture) request(t *testing.T, method string) systemcheck.GitHubDeviceLogin {
	t.Helper()
	var login systemcheck.GitHubDeviceLogin
	status, err := f.daemon.call(method, "/system/github-auth/device", nil, &login)
	if err != nil || status < 200 || status > 299 {
		t.Fatalf("%s device: status=%d err=%v response=%+v", method, status, err, login)
	}
	return login
}

func (f *githubDeviceFixture) await(t *testing.T, state systemcheck.GitHubDeviceLoginState) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		got := f.request(t, "GET")
		if got.State == state {
			return
		}
		if got.State == systemcheck.GitHubDeviceLoginFailed && state != got.State {
			t.Fatalf("login failed: %s", got.Error)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("login never reached %s: %+v", state, f.request(t, "GET"))
}

func newGitHubDeviceFixture(t *testing.T, binary string) *githubDeviceFixture {
	t.Helper()
	// Compile before installing the OAuth-only proxy in this test's environment.
	buildDaemon(t)
	root := t.TempDir()
	f := &githubDeviceFixture{configDir: filepath.Join(root, "gh-config")}
	f.versionOverride = filepath.Join(root, "gh-version")
	f.outcome.Store("pending")
	if err := os.Mkdir(f.configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cert, ca := githubFixtureCertificate(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-OAuth-Scopes", "repo, read:org, gist")
		if f.rejected.Load() && (r.URL.Path == "/user" || r.URL.Path == "/") {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"message":"Bad credentials"}`)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/login/") && !strings.Contains(r.Header.Get("Authorization"), "ao-fixture-token") {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"message":"Requires authentication"}`)
			return
		}
		var body any
		switch r.URL.Path {
		case "/login/device/code":
			f.starts.Add(1)
			if delay := f.codeDelay.Load(); delay > 0 {
				select {
				case <-time.After(time.Duration(delay) * time.Millisecond):
				case <-r.Context().Done():
					return
				}
			}
			body = map[string]any{"device_code": "fixture-device", "user_code": "ABCD-1234", "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 1}
		case "/login/oauth/access_token":
			switch f.outcome.Load() {
			case "approve":
				body = map[string]any{"access_token": "ao-fixture-token", "token_type": "bearer", "scope": "repo,read:org,gist"}
			case "deny":
				body = map[string]any{"error": "access_denied", "error_description": "The user denied access."}
			case "expire":
				body = map[string]any{"error": "expired_token", "error_description": "The device code expired."}
			default:
				body = map[string]any{"error": "authorization_pending"}
			}
		case "/graphql":
			body = map[string]any{"data": map[string]any{"viewer": map[string]any{"login": "ao-onboarding-fixture"}}}
		default:
			body = map[string]any{"login": "ao-onboarding-fixture", "id": 1}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || (r.Host != "github.com:443" && r.Host != "api.github.com:443") {
			http.Error(w, "unexpected OAuth destination", http.StatusBadGateway)
			return
		}
		upstream, err := net.Dial("tcp", server.Listener.Addr().String())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		_, _ = fmt.Fprint(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close(); _ = client.Close() }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		_ = upstream.Close()
	}))
	t.Cleanup(proxy.Close)
	caPath := filepath.Join(root, "oauth-ca.pem")
	if err := os.WriteFile(caPath, ca, 0o600); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	quoted := "'" + strings.ReplaceAll(binary, "'", "'\\''") + "'"
	help, err := exec.Command(binary, "auth", "login", "--help").Output()
	if err != nil {
		t.Fatal(err)
	}
	quotedVersion := "'" + strings.ReplaceAll(f.versionOverride, "'", "'\\''") + "'"
	script := "#!/bin/sh\nif [ \"$1\" = --version ] && [ -f " + quotedVersion + " ]; then cat " + quotedVersion + "; exit 0; fi\n"
	if strings.Contains(string(help), "--insecure-storage") {
		script += "if [ \"$1\" = auth ] && [ \"$2\" = login ]; then exec " + quoted + " \"$@\" --insecure-storage; fi\n"
	}
	script += "exec " + quoted + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// The connected UI can request an update on macOS. Never let that fixture
	// invoke the host's real Homebrew or change its installed GitHub CLI.
	brew := "#!/bin/sh\nif [ \"$1\" = upgrade ]; then rm -f " + quotedVersion + "; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "brew"), []byte(brew), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
		t.Setenv(name, "")
	}
	t.Setenv("GH_CONFIG_DIR", f.configDir)
	t.Setenv("GH_HOST", "github.com")
	browserPath := filepath.Join(binDir, "user-browser")
	browserLog := filepath.Join(root, "unexpected-browser")
	if err := os.WriteFile(browserPath, []byte("#!/bin/sh\necho opened > '"+browserLog+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BROWSER", "'"+browserPath+"'")
	t.Setenv("GH_BROWSER", "'"+browserPath+"'")
	t.Cleanup(func() {
		if _, err := os.Stat(browserLog); !os.IsNotExist(err) {
			t.Error("gh launched the user's browser instead of leaving navigation to the renderer")
		}
	})
	t.Setenv("GH_PROMPT_DISABLED", "1")
	t.Setenv("GH_DEBUG", "api")
	t.Setenv("SSL_CERT_FILE", caPath)
	t.Setenv("GODEBUG", "x509sslcertoverrideplatform=1")
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	f.daemon = startDaemon(t, filepath.Join(root, "data"))
	return f
}

func githubFixtureCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "AO test OAuth"}, DNSNames: []string{"github.com", "api.github.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	cert, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return cert, certPEM
}

// TestGitHubDeviceBrowserFixture exposes the real daemon/CLI to the renderer.
// Only the external OAuth server is controlled, with all credentials disposable.
func TestGitHubDeviceBrowserFixture(t *testing.T) {
	binary := os.Getenv("AO_GITHUB_BROWSER_BINARY")
	if binary == "" {
		t.Skip("set AO_GITHUB_BROWSER_BINARY for connected browser verification")
	}
	f := newGitHubDeviceFixture(t, binary)
	done := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"baseUrl": strings.TrimSuffix(f.daemon.baseURL, "/api/v1"), "starts": f.starts.Load()})
			return
		}
		switch strings.TrimPrefix(r.URL.Path, "/") {
		case "old-cli":
			if err := os.WriteFile(f.versionOverride, []byte("gh version 2.3.0 (fixture)\n"), 0600); err != nil {
				t.Error(err)
			}
		case "approve", "deny", "expire":
			f.outcome.Store(strings.TrimPrefix(r.URL.Path, "/"))
		case "revoke":
			f.rejected.Store(true)
		case "reset", "delay":
			f.request(t, "DELETE")
			if err := os.Remove(f.versionOverride); err != nil && !os.IsNotExist(err) {
				t.Error(err)
			}
			f.outcome.Store("pending")
			f.rejected.Store(false)
			f.codeDelay.Store(0)
			if r.URL.Path == "/delay" {
				f.codeDelay.Store(3000)
			}
			if err := os.Remove(filepath.Join(f.configDir, "hosts.yml")); err != nil && !os.IsNotExist(err) {
				t.Error(err)
			}
		case "shutdown":
			once.Do(func() { close(done) })
		default:
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	if path := os.Getenv("AO_GITHUB_FIXTURE_FILE"); path != "" {
		if err := os.WriteFile(path, []byte(server.URL), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Printf("AO_GITHUB_FIXTURE_URL=%s\n", server.URL)
	select {
	case <-done:
	case <-time.After(10 * time.Minute):
		t.Fatal("browser fixture timed out")
	}
}
