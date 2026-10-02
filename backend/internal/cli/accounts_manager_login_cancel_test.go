package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	accountsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/accountsmanager"
)

func assertLoginCancellationAcknowledgement(t *testing.T, output, operationID string, asJSON bool) {
	t.Helper()
	if strings.Contains(output, "cancelled") || strings.Contains(output, "revoked") || strings.Contains(output, "status") {
		t.Errorf("bodyless DELETE invented observed state: %s", output)
	}
	if asJSON {
		var got map[string]any
		if err := json.Unmarshal([]byte(output), &got); err != nil {
			t.Fatalf("acknowledgement JSON: %v", err)
		}
		if len(got) != 2 || got["operationId"] != operationID || got["cancellationRequestAcknowledged"] != true {
			t.Errorf("expected a distinct cancellation-request acknowledgement, got %s", output)
		}
		return
	}
	want := "operation: " + operationID + "\ncancellation request acknowledged; sign-in outcome not confirmed\n"
	if output != want {
		t.Errorf("acknowledgement = %q, want %q", output, want)
	}
}

func TestManagedAccountsLoginCancelAcknowledgement(t *testing.T) {
	for _, state := range []string{"unknown", "completed-pruned", "pending"} {
		for _, format := range []string{"human", "json"} {
			t.Run(state+"/"+format, func(t *testing.T) {
				cfg := setConfigEnv(t)
				capture := &agentSwitchRequestCapture{}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/internal/") {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					capture.record(r)
					if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/accounts-manager/oauth-sessions/login-a" {
						http.NotFound(w, r)
						return
					}
					w.WriteHeader(http.StatusNoContent)
				}))
				t.Cleanup(server.Close)
				writeRunFileFor(t, cfg, server)
				args := []string{"accounts", "login-cancel", "login-a"}
				if format == "json" {
					args = append(args, "--json")
				}
				for attempt := 1; attempt <= 2; attempt++ {
					out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
					if err != nil || errOut != "" {
						t.Fatalf("acknowledgement failed: %v %s", err, errOut)
					}
					assertLoginCancellationAcknowledgement(t, out, "login-a", format == "json")
					method, path, body, count := capture.snapshot()
					if count != attempt || method != http.MethodDelete || path != "/api/v1/accounts-manager/oauth-sessions/login-a" || len(body) != 0 {
						t.Fatalf("acknowledgement changed request semantics: %s %s count=%d", method, path, count)
					}
				}
			})
		}
	}
}

func TestManagedAccountsLoginCancelHelp(t *testing.T) {
	for _, action := range []string{"cancel", "status"} {
		t.Run(action, func(t *testing.T) {
			out, errOut, err := executeCLI(t, Deps{}, "accounts", "login-"+action, "--help")
			if err != nil || errOut != "" {
				t.Fatalf("help: %v %s", err, errOut)
			}
			want := "Output safe sign-in state as JSON"
			if action == "cancel" {
				want = "Output cancellation request acknowledgement as JSON"
				if !strings.Contains(out, "Request sign-in cancellation") || strings.Contains(out, "Output safe sign-in state") {
					t.Fatalf("cancellation help claims observed state: %s", out)
				}
			}
			if !strings.Contains(out, want) {
				t.Fatalf("output contract missing from help: %s", out)
			}
		})
	}
}

type loginCancellationRunner struct {
	*core.ManagementClient
	mu          sync.Mutex
	committed   bool
	cancelCalls int
	removeCalls int
	events      chan core.OAuthEvent
}

func (f *loginCancellationRunner) ListCredentials(context.Context) ([]core.CredentialSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.committed {
		return nil, nil
	}
	return []core.CredentialSummary{{Ref: "private-credential", Provider: core.ProviderCodex, Kind: "oauth", Generation: 1, Status: core.CredentialActive}}, nil
}

func (*loginCancellationRunner) CredentialPublicID(string) (string, error) {
	return "amc_saved", nil
}

func (*loginCancellationRunner) OAuthPublicID(string) (string, error) {
	return "login-a", nil
}

func (*loginCancellationRunner) StartOAuth(_ context.Context, provider core.Provider, mode core.OAuthMode) (core.OAuthSession, error) {
	return core.OAuthSession{Provider: provider, Mode: mode, State: "private-login-state", ExpiresAt: time.Now().Add(10 * time.Minute)}, nil
}

func (f *loginCancellationRunner) StreamOAuthEvents(ctx context.Context, consume func(core.OAuthEvent) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-f.events:
			if err := consume(event); err != nil {
				return err
			}
		}
	}
}

func (f *loginCancellationRunner) CancelOAuth(_ context.Context, state string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if state != "private-login-state" {
		return core.ErrInvalidCredential
	}
	f.cancelCalls++
	if f.committed {
		return core.ErrCredentialConflict
	}
	return nil
}

func (f *loginCancellationRunner) RemoveCredential(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeCalls++
	return errors.New("sign-in cancellation must not remove credentials")
}

func TestManagedAccountsLoginCancelHTTPServicePruning(t *testing.T) {
	for _, format := range []string{"human", "json"} {
		t.Run(format, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := setConfigEnv(t)
				if err := runfile.Write(cfg.runFile, runfile.Info{PID: os.Getpid(), Port: 3001, StartedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				runner := &loginCancellationRunner{events: make(chan core.OAuthEvent)}
				service := accountsvc.New(runner)
				service.Start(ctx)
				router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{AccountsManagerService: service}, httpd.ControlDeps{})
				var lastDeleteStatus int
				var lastDeleteRequestID string
				deps := Deps{ProcessAlive: func(int) bool { return true }, HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					r.RemoteAddr = "127.0.0.1:32100"
					w := httptest.NewRecorder()
					router.ServeHTTP(w, r)
					if r.Method == http.MethodDelete {
						lastDeleteStatus = w.Code
						var envelope apiError
						if w.Code == http.StatusConflict {
							if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
								t.Fatal(err)
							}
						}
						lastDeleteRequestID = envelope.RequestID
						if w.Code == http.StatusNoContent && w.Body.Len() != 0 {
							t.Fatal("public DELETE unexpectedly returned observed state")
						}
					}
					return w.Result(), nil
				})}}
				run := func(args ...string) (string, error) {
					t.Helper()
					if format == "json" {
						args = append(args, "--json")
					}
					out, errOut, err := executeCLI(t, deps, append([]string{"accounts"}, args...)...)
					if errOut != "" || strings.Contains(out, "private-") {
						t.Fatalf("unexpected diagnostics or private output: %s %s", out, errOut)
					}
					return out, err
				}
				acknowledge := func(id string) {
					t.Helper()
					out, err := run("login-cancel", id)
					if err != nil || lastDeleteStatus != http.StatusNoContent {
						t.Fatalf("expected public 204 acknowledgement: %v HTTP %d", err, lastDeleteStatus)
					}
					assertLoginCancellationAcknowledgement(t, out, id, format == "json")
				}
				assertStatus := func(want string) {
					t.Helper()
					out, err := run("login-status", "login-a")
					if err != nil || strings.Contains(out, "cancellationRequestAcknowledged") {
						t.Fatalf("observed status %q: %v %s", want, err, out)
					}
					if format == "json" {
						var observed managedLoginDTO
						if json.Unmarshal([]byte(out), &observed) != nil || observed.ID != "login-a" || observed.Status != want || (want == "expired" && observed.FailureCode != "cancelled") {
							t.Fatalf("observed status changed: %s", out)
						}
					} else if out != "operation: login-a\nstatus: "+want+"\n" {
						t.Fatalf("observed status changed: %s", out)
					}
				}
				acknowledge("login-unknown")
				acknowledge("login-unknown")
				if _, err := run("login", "--provider", "codex", "--mode", "device"); err != nil {
					t.Fatal(err)
				}
				acknowledge("login-a")
				assertStatus("pending")
				runner.events <- core.OAuthEvent{State: "private-login-state", Provider: core.ProviderCodex, Mode: core.OAuthModeDevice, Status: core.OAuthExpired, FailureCode: "cancelled"}
				synctest.Wait()
				assertStatus("expired")
				time.Sleep(time.Minute + time.Nanosecond)
				synctest.Wait()
				if _, err := run("login", "--provider", "codex", "--mode", "device"); err != nil {
					t.Fatal(err)
				}
				runner.mu.Lock()
				runner.committed = true
				runner.mu.Unlock()
				runner.events <- core.OAuthEvent{State: "private-login-state", Provider: core.ProviderCodex, Mode: core.OAuthModeDevice, Status: core.OAuthCompleted}
				synctest.Wait()
				assertStatus("completed")
				out, err := run("login-cancel", "login-a")
				var apiErr apiResponseError
				if ExitCode(err) != 1 || out != "" || !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict || lastDeleteStatus != http.StatusConflict || apiErr.ErrorBody.Code != "ACCOUNTS_MANAGER_CONFLICT" || lastDeleteRequestID == "" || apiErr.ErrorBody.RequestID != lastDeleteRequestID || !strings.Contains(err.Error(), "[request "+lastDeleteRequestID+"]") {
					t.Fatalf("committed conflict or request ID lost: %v, output=%q, HTTP=%d, request=%q", err, out, lastDeleteStatus, lastDeleteRequestID)
				}
				assertStatus("completed")
				time.Sleep(time.Minute + time.Nanosecond)
				synctest.Wait()
				if snapshot := service.Snapshot(); len(snapshot.OAuthSessions) != 0 || len(snapshot.Accounts) != 1 || snapshot.Accounts[0].ID != "amc_saved" {
					t.Fatalf("production pruning did not retain the credential: %+v", snapshot)
				}
				t.Log("production service pruned completed sign-in; committed credential remains")
				acknowledge("login-a")
				acknowledge("login-a")
				out, err = run("login-status", "login-a")
				if ExitCode(err) != 1 || out != "" || !strings.Contains(err.Error(), "not found") {
					t.Fatalf("pruned record acquired an observed status: %v %s", err, out)
				}
				out, err = run("ls")
				if err != nil || !strings.Contains(out, "amc_saved") {
					t.Fatalf("committed credential disappeared: %v %s", err, out)
				}
				runner.mu.Lock()
				defer runner.mu.Unlock()
				if runner.cancelCalls != 2 || runner.removeCalls != 0 || !runner.committed {
					t.Fatalf("unknown/pruned acknowledgement reached runner or revoked credential: cancel=%d remove=%d committed=%v", runner.cancelCalls, runner.removeCalls, runner.committed)
				}
			})
		})
	}
}
