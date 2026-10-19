package persistenthost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

func TestReconnectOnlyChecksProcessStartIdentity(t *testing.T) {
	identity := processalive.Identify(context.Background(), os.Getpid())
	if identity.StartID == "" {
		t.Skip("current process start identity is unavailable")
	}
	for _, tc := range []struct {
		name     string
		startID  string
		canceled bool
		want     error
	}{
		{"reused pid", "previous-process-incarnation", false, ErrNotRunning},
		{"matching owner", identity.StartID, false, ErrOwnershipInconclusive},
		{"unavailable identity", "previous-process-incarnation", true, ErrOwnershipInconclusive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{SessionID: "modern-owner", DataDir: t.TempDir(), ReconnectOnly: true}
			if err := writeDescriptor(cfg.DataDir, Descriptor{
				Version: ProtocolVersion, SessionID: cfg.SessionID, Address: closedHostAddress(t),
				Token: "stale", PID: os.Getpid(), ProcessStartID: tc.startID, StartedAt: time.Unix(0, 0).UTC(),
			}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			if _, err := ConnectOrStart(ctx, cfg); !errors.Is(err, tc.want) {
				t.Fatalf("reconnect = %v, want %v", err, tc.want)
			}
			if _, err := readDescriptor(cfg.DataDir, cfg.SessionID); err != nil {
				t.Fatalf("reconnect-only changed ownership files: %v", err)
			}
		})
	}
}

func TestAcquireHostLockChecksProcessStartIdentity(t *testing.T) {
	identity := processalive.Identify(context.Background(), os.Getpid())
	if identity.StartID == "" {
		t.Skip("current process start identity is unavailable")
	}
	for _, tc := range []struct {
		name    string
		startID string
		want    error
	}{
		{"reused pid", "previous-process-incarnation", nil},
		{"matching owner", identity.StartID, ErrHostExists},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			const sessionID = "modern-lock"
			if err := writeDescriptor(dataDir, Descriptor{
				Version: ProtocolVersion, SessionID: sessionID, Address: closedHostAddress(t),
				Token: "old", PID: os.Getpid(), StartedAt: time.Unix(0, 0).UTC(),
			}); err != nil {
				t.Fatal(err)
			}
			owner, err := json.Marshal(hostLockOwner{
				PID: os.Getpid(), ProcessStartID: tc.startID, StartedAt: time.Unix(0, 0).UTC(),
			})
			if err != nil {
				t.Fatal(err)
			}
			path, err := lockPath(dataDir, sessionID)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, owner, 0o600); err != nil {
				t.Fatal(err)
			}
			release, err := acquireHostLock(dataDir, sessionID)
			if release != nil {
				defer release()
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("acquire lock = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReconnectOnlyChecksLegacyProcessStart(t *testing.T) {
	identity := processalive.Identify(context.Background(), os.Getpid())
	wantOldRecord := ErrNotRunning
	if runtime.GOOS == "linux" {
		wantOldRecord = ErrOwnershipInconclusive
	}
	for _, tc := range []struct {
		name      string
		startedAt time.Time
		want      error
	}{
		{"old publication timestamp", time.Unix(0, 0).UTC(), wantOldRecord},
		{"unknown start time", time.Time{}, ErrOwnershipInconclusive},
		{"possible current owner", time.Now().UTC(), ErrOwnershipInconclusive},
		{"same second as process start", identity.StartedAt.Truncate(time.Second), ErrOwnershipInconclusive},
		{"coarse timestamp near process start", identity.StartedAt.Truncate(time.Second).Add(-time.Second), ErrOwnershipInconclusive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{SessionID: "legacy-owner", DataDir: t.TempDir(), ReconnectOnly: true}
			if err := writeDescriptor(cfg.DataDir, Descriptor{
				Version: ProtocolVersion, SessionID: cfg.SessionID, Address: closedHostAddress(t),
				Token: "stale", PID: os.Getpid(), StartedAt: tc.startedAt,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := ConnectOrStart(context.Background(), cfg); !errors.Is(err, tc.want) {
				t.Fatalf("reconnect = %v, want %v", err, tc.want)
			}
			if _, err := readDescriptor(cfg.DataDir, cfg.SessionID); err != nil {
				t.Fatalf("reconnect-only changed ownership files: %v", err)
			}
		})
	}
}

func TestAcquireHostLockChecksLegacyProcessStart(t *testing.T) {
	var wantOldRecord error
	if runtime.GOOS == "linux" {
		wantOldRecord = ErrHostExists
	}
	for _, tc := range []struct {
		name     string
		modified time.Time
		want     error
	}{
		{"old publication timestamp", time.Unix(0, 0).UTC(), wantOldRecord},
		{"newer lock with older descriptor", time.Now().UTC(), ErrHostExists},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			const sessionID = "legacy-lock"
			if err := writeDescriptor(dataDir, Descriptor{
				Version: ProtocolVersion, SessionID: sessionID, Address: closedHostAddress(t),
				Token: "old", PID: os.Getpid(), StartedAt: time.Unix(0, 0).UTC(),
			}); err != nil {
				t.Fatal(err)
			}
			path, err := lockPath(dataDir, sessionID)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, tc.modified, tc.modified); err != nil {
				t.Fatal(err)
			}
			release, err := acquireHostLock(dataDir, sessionID)
			if release != nil {
				defer release()
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("acquire lock = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestConnectOrStartRecoversReusedPIDWithoutDisturbingOtherSession(t *testing.T) {
	dataDir := t.TempDir()
	liveCfg := Config{
		SessionID: "other-live-session", DataDir: dataDir, Workdir: t.TempDir(),
		Env:  append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1"),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	live, err := ConnectOrStart(context.Background(), liveCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = live.Stdin.Close()
		_ = Shutdown(context.Background(), dataDir, liveCfg.SessionID)
	})
	liveDescriptor := awaitDescriptor(t, dataDir, liveCfg.SessionID)
	livePID := requestProviderPID(t, live, 1, "pid")

	staleCfg := liveCfg
	staleCfg.SessionID = "rebooted-session"
	staleDescriptor := Descriptor{
		Version: ProtocolVersion, SessionID: staleCfg.SessionID, Address: closedHostAddress(t),
		Token: "stale", PID: os.Getpid(), StartedAt: time.Unix(0, 0).UTC(),
	}
	lockOwner := []byte(fmt.Sprintf("%d\n", os.Getpid()))
	if runtime.GOOS == "linux" {
		// Linux wall-clock start times cannot prove that a live legacy PID was reused.
		staleDescriptor.ProcessStartID = "previous-process-incarnation"
		lockOwner, err = json.Marshal(hostLockOwner{
			PID: os.Getpid(), ProcessStartID: staleDescriptor.ProcessStartID, StartedAt: staleDescriptor.StartedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := writeDescriptor(dataDir, staleDescriptor); err != nil {
		t.Fatal(err)
	}
	path, err := lockPath(dataDir, staleCfg.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, lockOwner, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Unix(0, 0), time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}

	replacement, err := ConnectOrStart(context.Background(), staleCfg)
	if err != nil {
		t.Fatalf("recover session with reused host pid: %v", err)
	}
	t.Cleanup(func() {
		_ = replacement.Stdin.Close()
		_ = Shutdown(context.Background(), dataDir, staleCfg.SessionID)
	})
	if replacement.Reconnected {
		t.Fatal("replacement reported attaching to the dead host")
	}
	if pid := requestProviderPID(t, replacement, 1, "pid"); pid == os.Getpid() || pid == livePID {
		t.Fatalf("replacement provider pid = %d, reused unrelated owner", pid)
	}
	current, err := readDescriptor(dataDir, liveCfg.SessionID)
	if err != nil || current.PID != liveDescriptor.PID || current.Token != liveDescriptor.Token {
		t.Fatalf("other session ownership changed: pid=%d error=%v", current.PID, err)
	}
	if got := requestProviderPID(t, live, 2, "pid"); got != livePID {
		t.Fatalf("other session provider changed: %d -> %d", livePID, got)
	}
}

func TestAcquireHostLockSerializesStaleReclamation(t *testing.T) {
	dataDir := t.TempDir()
	const sessionID = "concurrent-recovery"
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path, _ := lockPath(dataDir, sessionID)
	owner, err := json.Marshal(hostLockOwner{PID: os.Getpid(), ProcessStartID: "previous-process"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, owner, 0o600); err != nil {
		t.Fatal(err)
	}
	type result struct {
		release func()
		err     error
	}
	const starters = 8
	start := make(chan struct{})
	results := make(chan result, starters)
	for range starters {
		go func() {
			<-start
			release, err := acquireHostLock(dataDir, sessionID)
			results <- result{release: release, err: err}
		}()
	}
	close(start)
	acquired := 0
	for range starters {
		got := <-results
		if got.err == nil {
			acquired++
			t.Cleanup(got.release)
		} else if !errors.Is(got.err, ErrHostExists) {
			t.Fatalf("contending recovery: %v", got.err)
		}
	}
	if acquired != 1 {
		t.Fatalf("%d starters acquired session ownership, want one", acquired)
	}
}

func TestReusedPIDDoesNotOwnMemoryAccountingOrShutdown(t *testing.T) {
	dataDir := t.TempDir()
	const sessionID = "reused-owner"
	if err := writeDescriptor(dataDir, Descriptor{
		Version: ProtocolVersion, SessionID: sessionID, Address: closedHostAddress(t),
		Token: "stale", PID: os.Getpid(), ProcessStartID: "previous-process",
	}); err != nil {
		t.Fatal(err)
	}
	if pid, live := HostPID(dataDir, sessionID); live || pid != 0 {
		t.Fatalf("unrelated PID counted as live host: pid=%d live=%v", pid, live)
	}
	if err := Shutdown(context.Background(), dataDir, sessionID); err != nil {
		t.Fatalf("shutdown of already-dead owner: %v", err)
	}
	if !processalive.Alive(os.Getpid()) {
		t.Fatal("recovery stopped the unrelated process")
	}
}

func TestConnectOrStartAdoptsLegacyDescriptor(t *testing.T) {
	cfg := Config{
		SessionID: "legacy-live", DataDir: t.TempDir(), Workdir: t.TempDir(),
		Env:  append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1"),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	first, err := ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = first.Stdin.Close()
		_ = Shutdown(context.Background(), cfg.DataDir, cfg.SessionID)
	})
	pid := requestProviderPID(t, first, 1, "pid")
	descriptor, err := readDescriptor(cfg.DataDir, cfg.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	descriptor.ProcessStartID = ""
	if err := writeDescriptor(cfg.DataDir, descriptor); err != nil {
		t.Fatal(err)
	}
	path, _ := lockPath(cfg.DataDir, cfg.SessionID)
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", descriptor.PID)), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = first.Stdin.Close()
	cfg.Prepare = func(context.Context) (PreparedProvider, error) {
		return PreparedProvider{}, errors.New("live legacy adoption must not prepare another provider")
	}
	second, err := ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatalf("adopt legacy host: %v", err)
	}
	t.Cleanup(func() { _ = second.Stdin.Close() })
	if !second.Reconnected || requestProviderPID(t, second, 2, "pid") != pid {
		t.Fatal("legacy host adoption replaced the provider")
	}
}

func closedHostAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}
