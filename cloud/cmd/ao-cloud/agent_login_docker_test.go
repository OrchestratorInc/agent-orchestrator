package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/agentconnect"
	"github.com/aoagents/agent-orchestrator/cloud/internal/config"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

const dockerLoginAttempt = "903a1c93-9300-4f53-92ad-7ee18a0fbcad"

type dockerLoginProviderFake struct {
	sandbox.Provider
	created         sandbox.Spec
	createErr       error
	deadline        time.Time
	lookedUp        string
	lookupErr       error
	found           bool
	deleted         sandbox.ID
	deleteErr       error
	skippedCleanup  bool
	remaining       string
	getErr          error
	workspaceFound  bool
	workspaceErr    error
	workspaceLookup string
}

func (f *dockerLoginProviderFake) DeleteWorkspaceBySession(_ context.Context, id string) (bool, error) {
	f.workspaceLookup = id
	return f.workspaceFound, f.workspaceErr
}

func (f *dockerLoginProviderFake) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Environment, error) {
	f.created = spec
	f.deadline, _ = ctx.Deadline()
	return sandbox.Environment{ID: "auth-container", State: sandbox.StateRunning}, f.createErr
}
func (f *dockerLoginProviderFake) FindBySession(_ context.Context, id string) (sandbox.Environment, bool, error) {
	f.lookedUp = id
	return sandbox.Environment{ID: "auth-container"}, f.found, f.lookupErr
}
func (f *dockerLoginProviderFake) Delete(ctx context.Context, id sandbox.ID) error {
	f.deleted = id
	f.skippedCleanup = sandbox.CleanupSkipped(ctx)
	return f.deleteErr
}
func (f *dockerLoginProviderFake) Get(context.Context, sandbox.ID) (sandbox.Environment, error) {
	if f.getErr != nil {
		return sandbox.Environment{}, f.getErr
	}
	if f.remaining != "" {
		return sandbox.Environment{ID: "auth-container", State: f.remaining}, nil
	}
	return sandbox.Environment{}, sandbox.ErrNotFound
}

// An auth container must enter the native-login branch with only its scoped
// bearer; ordinary worker bootstrap or repository env would expand its authority.
func TestDockerAgentLoginStartsIsolatedNativeMode(t *testing.T) {
	for _, harness := range []string{"codex", "claude-code"} {
		t.Run(harness, func(t *testing.T) {
			provider := &dockerLoginProviderFake{}
			runner := &dockerAgentLoginRunner{provider: provider, publicURL: "https://cloud.test"}
			expires := time.Now().Add(time.Minute)
			handle, err := runner.Start(context.Background(), agentconnect.Launch{ID: dockerLoginAttempt, OrgID: "01cccb27-6449-45d5-ae5d-554e4d49db26", Harness: harness, Token: "attempt-only-token", ExpiresAt: expires})
			if err != nil {
				t.Fatal(err)
			}
			if handle != dockerLoginAttempt || provider.created.SessionID != dockerLoginAttempt || provider.created.OrgID != "01cccb27-6449-45d5-ae5d-554e4d49db26" {
				t.Fatal("auth launch did not retain its private stable identity")
			}
			if provider.created.Labels["ao.purpose"] != "agent-login" || provider.created.Labels["ao.agent_login_id"] != dockerLoginAttempt {
				t.Fatal("missing dedicated auth identity")
			}
			env := provider.created.Environment
			if len(env) != 5 || env["AO_AGENT_LOGIN_ID"] != dockerLoginAttempt || env["AO_AGENT_LOGIN_TOKEN"] != "attempt-only-token" || env["AO_CLOUD_PUBLIC_URL"] != "https://cloud.test" || env["AO_DATA_DIR"] != "/workspace/.ao/agent-login" || env["PATH"] != "/usr/local/bin:/usr/bin:/bin" {
				t.Fatal("native login received unexpected environment")
			}
			if provider.deadline.IsZero() || provider.deadline.After(expires) {
				t.Fatal("container provisioning exceeded attempt expiry")
			}
		})
	}
}

func TestDockerAgentLoginRejectsUnsafeLaunchBeforeProvider(t *testing.T) {
	base := agentconnect.Launch{ID: dockerLoginAttempt, OrgID: "01cccb27-6449-45d5-ae5d-554e4d49db26", Harness: "codex", Token: "attempt-only-token", ExpiresAt: time.Now().Add(time.Minute)}
	for _, field := range []string{"id", "harness", "token", "expired", "http", "userinfo", "query", "path"} {
		t.Run(field, func(t *testing.T) {
			launch := base
			publicURL := "https://cloud.test"
			switch field {
			case "id":
				launch.ID = "../user-session"
			case "harness":
				launch.Harness = "unknown"
			case "token":
				launch.Token = ""
			case "expired":
				launch.ExpiresAt = time.Now().Add(-time.Second)
			case "http":
				publicURL = "http://cloud.test"
			case "userinfo":
				publicURL = "https://user:secret@cloud.test"
			case "query":
				publicURL = "https://cloud.test?token=secret"
			case "path":
				publicURL = "https://cloud.test/somewhere"
			}
			provider := &dockerLoginProviderFake{}
			runner := &dockerAgentLoginRunner{provider: provider, publicURL: publicURL}
			if _, err := runner.Start(context.Background(), launch); err == nil || provider.created.SessionID != "" {
				t.Fatal("unsafe launch reached container provider")
			}
		})
	}
}

func TestDockerAgentLoginFailureRetainsHandleAndRedactsProvider(t *testing.T) {
	provider := &dockerLoginProviderFake{createErr: errors.New("provider echoed attempt-only-token")}
	runner := &dockerAgentLoginRunner{provider: provider, publicURL: "https://cloud.test"}
	handle, err := runner.Start(context.Background(), agentconnect.Launch{ID: dockerLoginAttempt, Harness: "codex", Token: "attempt-only-token", ExpiresAt: time.Now().Add(time.Minute)})
	if err == nil || handle != dockerLoginAttempt || strings.Contains(err.Error(), "attempt-only-token") {
		t.Fatal("failed create lost cleanup identity or exposed private details")
	}
}

// Delete can return nil before deletion is done. Releasing a reservation needs
// independent provider confirmation, and project Cleanup must never execute.
func TestDockerAgentLoginCleanupRequiresConfirmedDeletion(t *testing.T) {
	for _, tc := range []struct {
		name               string
		found              bool
		remaining          string
		getErr, deleteErr  error
		wantFound, wantErr bool
	}{
		{name: "absent"},
		{name: "deleted", found: true, wantFound: true},
		{name: "deleted-state", found: true, remaining: sandbox.StateDeleted, wantFound: true},
		{name: "pending", found: true, remaining: sandbox.StateDeleting, wantErr: true},
		{name: "unknown", found: true, getErr: errors.New("private engine error"), wantErr: true},
		{name: "delete-failed", found: true, deleteErr: errors.New("private engine error"), wantErr: true},
		{name: "delete-missed-but-present", found: true, deleteErr: sandbox.ErrNotFound, remaining: sandbox.StateRunning, wantErr: true},
		{name: "delete-missed-and-confirmed-gone", found: true, deleteErr: sandbox.ErrNotFound, getErr: sandbox.ErrNotFound, wantFound: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &dockerLoginProviderFake{found: tc.found, remaining: tc.remaining, getErr: tc.getErr, deleteErr: tc.deleteErr}
			runner := &dockerAgentLoginRunner{provider: provider}
			found, err := runner.Cleanup(context.Background(), dockerLoginAttempt)
			if found != tc.wantFound || (err != nil) != tc.wantErr {
				t.Fatalf("cleanup found=%v err=%v", found, err)
			}
			if err != nil && strings.Contains(err.Error(), "private engine error") {
				t.Fatal("cleanup exposed private provider error")
			}
			if provider.lookedUp != dockerLoginAttempt {
				t.Fatal("cleanup addressed another session identity")
			}
			if tc.found && (provider.deleted != "auth-container" || !provider.skippedCleanup) {
				t.Fatal("auth cleanup ran project cleanup or deleted another container")
			}
			if !tc.found && provider.deleted != "" {
				t.Fatal("absent container was deleted")
			}
		})
	}
	provider := &dockerLoginProviderFake{}
	runner := &dockerAgentLoginRunner{provider: provider}
	if err := runner.Stop(context.Background(), "user-session"); err == nil || provider.lookedUp != "" {
		t.Fatal("untrusted cleanup handle reached provider")
	}
	if err := runner.Stop(context.Background(), dockerLoginAttempt); err != nil {
		t.Fatal("absent cleanup was not idempotent")
	}
}

func TestDockerAgentLoginCleanupRecoversVolumeWithoutContainer(t *testing.T) {
	provider := &dockerLoginProviderFake{workspaceFound: true}
	runner := &dockerAgentLoginRunner{provider: provider}
	found, err := runner.Cleanup(context.Background(), dockerLoginAttempt)
	if err != nil || found || provider.workspaceLookup != dockerLoginAttempt {
		t.Fatal("volume-only cleanup must remove private state while retaining the uncertain container lease")
	}
}

func TestDockerAgentLoginCleanupRetainsHandleUntilVolumeGone(t *testing.T) {
	for _, containerFound := range []bool{false, true} {
		provider := &dockerLoginProviderFake{found: containerFound, workspaceErr: errors.New("private volume error")}
		runner := &dockerAgentLoginRunner{provider: provider}
		found, err := runner.Cleanup(context.Background(), dockerLoginAttempt)
		if err == nil || found || strings.Contains(err.Error(), "private volume error") {
			t.Fatal("cleanup released a live auth volume or exposed provider details")
		}
		if provider.workspaceLookup != dockerLoginAttempt {
			t.Fatal("auth volume was not independently inspected")
		}
	}
}

func TestDockerAgentLoginFactoryDoesNotContactUnselectedEngine(t *testing.T) {
	for _, cfg := range []config.Config{
		{Environment: "production", SandboxProvider: sandbox.ProviderDocker, DockerHost: "invalid"},
		{Environment: "staging", SandboxProvider: sandbox.ProviderDocker, DockerHost: "invalid"},
		{SandboxProvider: sandbox.ProviderFreestyle, DockerHost: "invalid"},
	} {
		if runner, err := newDockerAgentLoginRunner(cfg); err != nil || runner != nil {
			t.Fatal("unselected Docker auth factory initialized a provider")
		}
	}
}

// Disabling new login must keep old auth-container cleanup available with the
// ordinary HTTP localhost development URL. Enabling admission still needs HTTPS.
func TestDockerAgentLoginFactoryAllowsHTTPOnlyForDisabledAdmission(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleanup-only", true: "enabled"}[enabled], func(t *testing.T) {
			dir, err := os.MkdirTemp("", "ao-auth-docker-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			socket := filepath.Join(dir, "engine.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			var requests atomic.Int32
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/version" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = w.Write([]byte(`{"ApiVersion":"1.44"}`))
			})}
			defer server.Close()
			go func() { _ = server.Serve(listener) }()
			runner, err := newDockerAgentLoginRunner(config.Config{SandboxProvider: sandbox.ProviderDocker, NativeAgentLoginEnabled: enabled, PublicURL: "http://127.0.0.1:8080", DockerHost: "unix://" + socket, DockerWorkerImage: "auth-fixture:local", DockerNamespace: "fixture"})
			if enabled {
				if err == nil || runner != nil || requests.Load() != 0 {
					t.Fatal("HTTP admission was enabled or reached Docker before validation")
				}
			} else if err != nil || runner == nil || requests.Load() != 1 {
				t.Fatalf("disabled local cleanup failed to initialize: %v", err)
			}
		})
	}
}

func TestDockerAgentLoginNamespaceIsSeparateAndBounded(t *testing.T) {
	for _, tc := range []struct {
		base, want string
		valid      bool
	}{
		{"ao-cloud-local", "ao-cloud-local-auth", true},
		{strings.Repeat("a", 58), strings.Repeat("a", 58) + "-auth", true},
		{strings.Repeat("a", 59), "", false},
		{"", "", false},
		{"../user-containers", "", false},
		{"bad namespace", "", false},
	} {
		got, err := dockerAgentLoginNamespace(tc.base)
		if (err == nil) != tc.valid || got != tc.want {
			t.Fatalf("namespace for %q = %q %v", tc.base, got, err)
		}
	}
}
