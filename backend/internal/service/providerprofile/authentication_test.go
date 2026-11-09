package providerprofile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// authFixture 仅使用受本地 TLS 客户端信任的假服务与假凭据。
func authFixture(t *testing.T, handler http.HandlerFunc) (*Registry, string) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), Filename)
	data, err := json.Marshal([]ports.ProviderProfile{{ID: "local", Name: "本地测试", Harness: domain.HarnessCodex, BaseURL: server.URL + "/v1", Model: "test-model", CredentialEnv: "AO_WORKFLOW_CREDENTIAL_TEST"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	r := &Registry{Path: path, Transport: server.Client().Transport, LookupEnv: func(string) (string, bool) { return "fake-private-key", true }}
	choices, err := r.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r, choices[0].Reference
}

// TestAuthenticationBindsCredentialAndDeduplicates 删除凭据身份比较将复用轮换前的成功授权。
func TestAuthenticationBindsCredentialAndDeduplicates(t *testing.T) {
	var calls atomic.Int32
	r, ref := authFixture(t, func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if req.URL.Path != "/v1/models" || req.Method != http.MethodGet {
			t.Error("unexpected generation or target request")
		}
		if req.Header.Get("Authorization") != "Bearer fake-private-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
	})
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			env, err := r.Environment(ctx, ref, domain.HarnessCodex, "test-model", "")
			if err != nil || len(env) != 1 || env[ports.ProviderProfileKeyEnv] != "fake-private-key" {
				t.Error("verified credential was not available exclusively in launch environment")
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 3 {
		t.Fatalf("concurrent/cached probes made %d requests, want 3", calls.Load())
	}
	r.LookupEnv = func(string) (string, bool) { return "rotated-wrong-key", true }
	state, err := r.Authentication(ctx, ref, domain.HarnessCodex, "test-model", "")
	if err != nil || state != domain.AgentAuthenticationUnauthorized {
		t.Fatalf("rotation reused old authorization: %s %v", state, err)
	}
	if _, err := r.Environment(ctx, ref, domain.HarnessCodex, "test-model", ""); err == nil || strings.Contains(err.Error(), "rotated-wrong-key") {
		t.Fatal("rejected secret was launched or exposed")
	}
	if calls.Load() != 6 {
		t.Fatalf("negative evidence was not cached: %d", calls.Load())
	}
	reopened := &Registry{Path: r.Path, Transport: r.Transport, LookupEnv: func(string) (string, bool) { return "fake-private-key", true }}
	if _, err := reopened.Environment(ctx, ref, domain.HarnessCodex, "test-model", ""); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 9 {
		t.Fatal("reopened registry failed to establish new credential evidence")
	}
}

// TestAuthenticationNeverAuthorizesPublicOrUnboundedResponses 删除对照、状态或正文边界会授权下列不充分证据。
func TestAuthenticationNeverAuthorizesPublicOrUnboundedResponses(t *testing.T) {
	for _, mode := range []string{"public", "accept-any-key", "rejected-401", "rejected-403", "missing-model", "redirect", "oversized", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			r, ref := authFixture(t, func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if mode == "timeout" {
					<-req.Context().Done()
					return
				}
				if mode == "redirect" {
					http.Redirect(w, req, "/must-not-follow", http.StatusTemporaryRedirect)
					return
				}
				key := req.Header.Get("Authorization")
				if mode != "public" && (mode != "accept-any-key" || key == "") && key != "Bearer fake-private-key" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				switch mode {
				case "rejected-401":
					w.WriteHeader(http.StatusUnauthorized)
				case "rejected-403":
					w.WriteHeader(http.StatusForbidden)
				case "missing-model":
					_, _ = w.Write([]byte(`{"data":[{"id":"other"}]}`))
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat(" ", 1<<20) + `{"data":[{"id":"test-model"}]}`))
				default:
					_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			state, err := r.Authentication(ctx, ref, domain.HarnessCodex, "test-model", "")
			if state == domain.AgentAuthenticationAuthorized {
				t.Fatalf("insufficient evidence authorized: %s %v", state, err)
			}
			if calls.Load() > 3 || mode == "redirect" && calls.Load() != 1 {
				t.Fatalf("probe exceeded request boundary: %d", calls.Load())
			}
		})
	}
}

// TestAuthenticationRejectsDriftWhileWaiting 删除最后重读会在慢探测期间把旧路由证据交给已变化的任务。
func TestAuthenticationRejectsDriftWhileWaiting(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	r, ref := authFixture(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer fake-private-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
	})
	result := make(chan error, 1)
	go func() {
		_, err := r.Environment(context.Background(), ref, domain.HarnessCodex, "test-model", "")
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("probe did not reach local service")
	}
	raw, err := os.ReadFile(r.Path)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	err = os.WriteFile(r.Path, []byte(strings.Replace(string(raw), "test-model", "changed-model", 1)), 0600)
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-result; err == nil {
		t.Fatal("in-flight route drift authorized a launch")
	}
}
