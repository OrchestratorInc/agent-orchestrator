package accountsmanager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEnsureStateCreatesPrivateLoopbackConfiguration(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := ensureState(stateDir)
	if err != nil {
		t.Fatalf("ensureState() error = %v", err)
	}
	if state.Port < 1 || state.Port > 65535 {
		t.Fatalf("port = %d", state.Port)
	}
	if state.ControlKey == "" || state.ClientKey == "" || state.ManagementKey == "" || state.RoutingKey == "" {
		t.Fatal("private keys were not generated")
	}
	if state.ControlKey == state.ClientKey || state.ControlKey == state.ManagementKey || state.ClientKey == state.ManagementKey {
		t.Fatal("private keys were not generated independently")
	}

	root := filepath.Join(stateDir, "accounts-manager")
	assertPrivateMode(t, root, 0o700)
	assertPrivateMode(t, filepath.Join(root, "auth"), 0o700)
	assertPrivateMode(t, filepath.Join(root, "control.key"), 0o600)
	assertPrivateMode(t, filepath.Join(root, "management.key"), 0o600)
	assertPrivateMode(t, filepath.Join(root, "routing.key"), 0o600)
	assertPrivateMode(t, filepath.Join(root, "config.yaml"), 0o600)

	b, err := os.ReadFile(filepath.Join(root, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	config := string(b)
	if strings.Contains(config, state.ManagementKey) {
		t.Fatal("management key was persisted in the engine configuration")
	}
	for _, required := range []string{
		"host: 127.0.0.1",
		"auth-dir: " + filepath.Join(root, "auth"),
		"disable-control-panel: true",
		"disable-auto-update-panel: true",
		"enabled: false",
		"request-log: false",
	} {
		if !strings.Contains(config, required) {
			t.Fatalf("config missing %q:\n%s", required, config)
		}
	}
}

func TestSupervisorRejectsRedirectsWithoutForwardingControlKey(t *testing.T) {
	forwarded := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	supervisor := New(Config{})
	if supervisor.requestOK(context.Background(), http.MethodPost, source.URL, "private-control", http.StatusNoContent) {
		t.Error("redirect was accepted")
	}
	select {
	case <-forwarded:
		t.Fatal("control request followed a redirect")
	default:
	}
}

func TestEnsureStateRejectsUnsafeManagementKey(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, path string)
		want   string
	}{
		{
			name: "empty",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "empty",
		},
		{
			name: "group readable",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Chmod(path, 0o640); err != nil {
					t.Fatal(err)
				}
			},
			want: "permissions",
		},
	}
	if runtime.GOOS != "windows" {
		tests = append(tests, struct {
			name   string
			mutate func(t *testing.T, path string)
			want   string
		}{
			name: "symlink",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				target := filepath.Join(filepath.Dir(path), "elsewhere-management")
				if err := os.WriteFile(target, []byte("do-not-read"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
			want: "regular",
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stateDir := t.TempDir()
			if err := os.Chmod(stateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := ensureState(stateDir); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(stateDir, stateDirectoryName, managementKeyFileName)
			tt.mutate(t, path)
			if _, err := ensureState(stateDir); err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("ensureState() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestEnsureStateRejectsSymlinkedControlKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(stateDir, "accounts-manager")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "elsewhere")
	if err := os.WriteFile(target, []byte("do-not-read"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "control.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureState(stateDir); err == nil || !strings.Contains(strings.ToLower(err.Error()), "regular") {
		t.Fatalf("ensureState() error = %v, want symlink rejection", err)
	}
}

func TestTryAttachAuthenticatesIdentityAndRenewsLease(t *testing.T) {
	t.Parallel()

	const controlKey = "control-secret"
	leaseCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/ao/internal/identity", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+controlKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(controlIdentity{Service: serviceName, InstanceID: "instance-1", EngineVersion: "v7.3.8", CredentialProtocol: credentialProtocolVersion})
	})
	mux.HandleFunc("/ao/internal/lease", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+controlKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		leaseCalls++
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port

	s := New(Config{HTTPClient: server.Client(), Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})
	runtimeRecord := RuntimeRecord{PID: os.Getpid(), Port: port, InstanceID: "instance-1"}
	endpoint, ok := s.tryAttach(context.Background(), runtimeRecord, controlKey, "client-secret", "management-secret")
	if !ok {
		t.Fatal("tryAttach() did not accept authenticated runner")
	}
	if endpoint.BaseURL != server.URL || endpoint.ClientToken != "client-secret" || endpoint.ManagementToken != "management-secret" {
		t.Fatalf("endpoint = %#v", endpoint)
	}
	if leaseCalls != 1 {
		t.Fatalf("lease calls = %d, want 1", leaseCalls)
	}
}

func TestTryAttachRejectsOldCredentialProtocol(t *testing.T) {
	for _, version := range []int{0, credentialProtocolVersion - 1, credentialProtocolVersion + 1} {
		leaseCalls := 0
		mux := http.NewServeMux()
		mux.HandleFunc("/ao/internal/identity", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(controlIdentity{Service: serviceName, InstanceID: "old", CredentialProtocol: version})
		})
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		mux.HandleFunc("/ao/internal/lease", func(w http.ResponseWriter, _ *http.Request) { leaseCalls++; w.WriteHeader(http.StatusNoContent) })
		server := httptest.NewServer(mux)
		s := New(Config{HTTPClient: server.Client()})
		_, ok := s.tryAttach(t.Context(), RuntimeRecord{PID: os.Getpid(), Port: server.Listener.Addr().(*net.TCPAddr).Port, InstanceID: "old"}, "control", "client", "management")
		server.Close()
		if ok || leaseCalls != 0 {
			t.Fatal("incompatible runner attached or received a lease")
		}
	}
}

func TestTryAttachRejectsIdentityMismatch(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/ao/internal/identity", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(controlIdentity{Service: serviceName, InstanceID: "someone-else"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port

	s := New(Config{HTTPClient: server.Client()})
	if _, ok := s.tryAttach(context.Background(), RuntimeRecord{PID: os.Getpid(), Port: port, InstanceID: "expected"}, "control", "client", "management"); ok {
		t.Fatal("tryAttach() accepted an identity mismatch")
	}
}

func TestTryAttachRejectsDeadRuntimePIDBeforeContactingPort(t *testing.T) {
	t.Parallel()

	identityCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/ao/internal/identity", func(w http.ResponseWriter, _ *http.Request) {
		identityCalls++
		_ = json.NewEncoder(w).Encode(controlIdentity{Service: serviceName, InstanceID: "stale-instance"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port

	s := New(Config{HTTPClient: server.Client(), processAlive: func(int) bool { return false }})
	if _, ok := s.tryAttach(context.Background(), RuntimeRecord{PID: 424242, Port: port, InstanceID: "stale-instance"}, "control", "client", "management"); ok {
		t.Fatal("tryAttach() accepted a runtime record whose PID is dead")
	}
	if identityCalls != 0 {
		t.Fatalf("identity calls = %d, want 0 for a dead PID", identityCalls)
	}
}

func TestOccupiedPortSelectsAnotherLoopbackPort(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	occupied := listener.Addr().(*net.TCPAddr).Port

	got, err := selectAvailablePort(occupied)
	if err != nil {
		t.Fatal(err)
	}
	if got == occupied {
		t.Fatalf("selected occupied port %d", got)
	}
}

func TestRestartBackoffIsCapped(t *testing.T) {
	t.Parallel()

	wants := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for attempt, want := range wants {
		if got := restartBackoff(attempt); got != want {
			t.Fatalf("restartBackoff(%d) = %s, want %s", attempt, got, want)
		}
	}
}

func TestMissingBinaryDegradesWithoutBlocking(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	s := New(Config{StateDir: stateDir, Binary: filepath.Join(stateDir, "missing")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status := s.Status()
		if status.State == StateDegraded {
			if status.Reason != ReasonBinaryMissing {
				t.Fatalf("reason = %q, want %q", status.Reason, ReasonBinaryMissing)
			}
			if status.EngineVersion != "" {
				t.Fatalf("degraded status exposed engine version %q", status.EngineVersion)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("status did not become degraded: %#v", s.Status())
}

func TestStartReattachesExistingRunnerWithoutStartingBinary(t *testing.T) {
	t.Parallel()

	const controlKey = "reattach-control"
	leaseCalls := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/ao/internal/identity", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+controlKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(controlIdentity{Service: serviceName, InstanceID: "reattach-instance", EngineVersion: "v7.3.8", CredentialProtocol: credentialProtocolVersion})
	})
	mux.HandleFunc("/ao/internal/lease", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+controlKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		select {
		case leaseCalls <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port

	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := ensureState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(state.Root, "control.key"), []byte(controlKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = updateConfigPort(state.ConfigPath, port); err != nil {
		t.Fatal(err)
	}
	recordBytes, _ := json.Marshal(RuntimeRecord{PID: os.Getpid(), Port: port, InstanceID: "reattach-instance", UpstreamVersion: "v7.3.8"})
	if err = writePrivateAtomic(filepath.Join(state.Root, "runtime.json"), recordBytes); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(Config{StateDir: stateDir, HTTPClient: server.Client()})
	s.Start(ctx)
	waitForStatus(t, s, StateReady, time.Second)
	if endpoint, ok := s.Endpoint(); !ok || endpoint.BaseURL != server.URL || endpoint.ManagementToken != state.ManagementKey {
		t.Fatalf("endpoint = %#v, ok = %v", endpoint, ok)
	}
	select {
	case <-leaseCalls:
	case <-time.After(time.Second):
		t.Fatal("replacement daemon did not renew the existing runner lease")
	}
}

func TestAccountsManagerSupervisorEndpointFeedsManagementClient(t *testing.T) {
	t.Parallel()

	const (
		controlKey    = "integration-control-secret"
		clientKey     = "integration-client-secret"
		managementKey = "integration-management-secret"
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/ao/internal/identity", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+controlKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(controlIdentity{Service: serviceName, InstanceID: "integration-instance", EngineVersion: "v7.3.8", CredentialProtocol: credentialProtocolVersion})
	})
	mux.HandleFunc("/ao/internal/lease", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+controlKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc(credentialManagementPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+managementKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"observed_at":"2026-09-20T10:11:12Z","files":[]}`)
	})
	mux.HandleFunc("/v0/management/routing/strategy", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+managementKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"strategy":"round-robin"}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port

	supervisor := New(Config{HTTPClient: server.Client()})
	endpoint, ok := supervisor.tryAttach(context.Background(), RuntimeRecord{PID: os.Getpid(), Port: port, InstanceID: "integration-instance"}, controlKey, clientKey, managementKey)
	if !ok {
		t.Fatal("supervisor did not accept the authenticated runner contract")
	}
	supervisor.setReady(endpoint, "v7.3.8")
	client := NewManagementClient(supervisor, server.Client())
	credentials, err := client.ListCredentials(context.Background())
	if err != nil || len(credentials) != 0 {
		t.Fatalf("ListCredentials() count=%d error=%v, want empty inventory", len(credentials), err)
	}
	strategy, err := client.RoutingStrategy(context.Background())
	if err != nil || strategy != RoutingRoundRobin {
		t.Fatalf("RoutingStrategy()=%q error=%v, want %q", strategy, err, RoutingRoundRobin)
	}

	publicStatus, err := json.Marshal(supervisor.Status())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{server.URL, controlKey, clientKey, managementKey} {
		if strings.Contains(string(publicStatus), secret) {
			t.Fatal("public supervisor status exposed private connection material")
		}
	}
}

func TestMaintainSpawnedDoesNotKillRunnerWhenDaemonContextIsCancelled(t *testing.T) {
	t.Parallel()

	const controlKey = "shutdown-control"
	leaseStarted := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/ao/internal/identity", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+controlKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(controlIdentity{Service: serviceName, InstanceID: "shutdown-instance", CredentialProtocol: credentialProtocolVersion})
	})
	mux.HandleFunc("/ao/internal/lease", func(_ http.ResponseWriter, r *http.Request) {
		close(leaseStarted)
		<-r.Context().Done()
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithCancel(context.Background())
	proc := &recordingManagedProcess{killed: make(chan struct{}, 1)}
	done := make(chan error, 1)
	s := New(Config{HTTPClient: server.Client()})
	go func() {
		done <- s.maintainSpawned(ctx, RuntimeRecord{
			PID:        os.Getpid(),
			Port:       port,
			InstanceID: "shutdown-instance",
		}, privateState{ControlKey: controlKey, ClientKey: "client-key", ManagementKey: "management-key"}, proc, make(chan error))
	}()

	select {
	case <-leaseStarted:
		cancel()
	case <-time.After(leaseInterval + 2*time.Second):
		cancel()
		t.Fatal("lease renewal did not begin")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("maintainSpawned() error = %v, want nil during daemon shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("maintainSpawned did not stop after daemon cancellation")
	}
	select {
	case <-proc.killed:
		t.Fatal("daemon shutdown killed the runner instead of leaving it available for reattach")
	default:
	}
}

func TestCrashedRunnerBecomesDegradedAndRestarts(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(stateDir, "fake-runner")
	if err := os.WriteFile(binary, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	launches := 0
	launch := func(_, _ string) (managedProcess, <-chan error, error) {
		mu.Lock()
		launches++
		mu.Unlock()
		wait := make(chan error, 1)
		wait <- errors.New("crashed")
		return fakeManagedProcess{}, wait, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(Config{StateDir: stateDir, Binary: binary, launch: launch})
	s.Start(ctx)
	waitForStatus(t, s, StateDegraded, time.Second)
	if got := s.Status().Reason; got != ReasonProcessExited {
		t.Fatalf("reason = %q, want %q", got, ReasonProcessExited)
	}
	time.Sleep(1100 * time.Millisecond)
	mu.Lock()
	gotLaunches := launches
	mu.Unlock()
	if gotLaunches < 2 {
		t.Fatalf("launches = %d, want a backoff restart", gotLaunches)
	}
}

type fakeManagedProcess struct{}

func (fakeManagedProcess) Kill() error { return nil }

type recordingManagedProcess struct {
	killed chan struct{}
}

func (p *recordingManagedProcess) Kill() error {
	select {
	case p.killed <- struct{}{}:
	default:
	}
	return nil
}

func waitForStatus(t *testing.T, supervisor *Supervisor, want State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if supervisor.Status().State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("status = %#v, want %q", supervisor.Status(), want)
}

func assertPrivateMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
	}
}
