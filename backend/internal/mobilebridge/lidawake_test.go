//go:build !windows

package mobilebridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLidStatusRequiresFreshOwnLease(t *testing.T) {
	for _, tc := range []struct {
		name       string
		age        float64
		ids        []string
		blocked    string
		wantActive bool
		reason     string
	}{
		{"own lease", 0, []string{"mine"}, "", true, ""},
		{"other daemon", 0, []string{"other"}, "", false, "starting"},
		{"scheduler margin", 15, []string{"mine"}, "", true, ""},
		{"stale", 40, []string{"mine"}, "", false, "helper_unavailable"},
		{"future", -10, []string{"mine"}, "", false, "helper_unavailable"},
		{"safety latch", 0, []string{"mine"}, "low_battery", false, "low_battery"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			st := lidStatus{Version: lidHelperVersion, UpdatedAt: float64(time.Now().UnixMilli())/1000 - tc.age, Active: true, Leases: tc.ids, Blocked: map[string]string{"mine": tc.blocked}}
			data, _ := json.Marshal(st)
			if err := writeWakeFile(filepath.Join(directory, "status.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			l := &LidAwake{supported: true, directory: directory, id: "mine", cancel: func() {}}
			supported, active, reason := l.Status()
			if !supported || active != tc.wantActive || reason != tc.reason {
				t.Fatalf("got (%v,%v,%q)", supported, active, reason)
			}
		})
	}
}

func TestLidLeaseRenewalAndStop(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "leases"), 0700); err != nil {
		t.Fatal(err)
	}
	l := &LidAwake{directory: directory, id: "mine", pid: 42, started: 99}
	for i := uint64(1); i <= 2; i++ {
		if err := l.renew(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(directory, "leases/mine.json"))
		if err != nil {
			t.Fatal(err)
		}
		var lease lidLease
		if err := json.Unmarshal(data, &lease); err != nil {
			t.Fatal(err)
		}
		if lease.Sequence != i || lease.PID != 42 || lease.Started != 99 {
			t.Fatalf("wrong lease: %+v", lease)
		}
	}
	ack, _ := json.Marshal(lidStatus{Version: lidHelperVersion, UpdatedAt: float64(time.Now().UnixMilli()) / 1000})
	if err := writeWakeFile(filepath.Join(directory, "status.json"), ack, 0600); err != nil {
		t.Fatal(err)
	}
	l.Stop()
	if _, err := os.Stat(filepath.Join(directory, "leases/mine.json")); !os.IsNotExist(err) {
		t.Fatalf("lease survives stop: %v", err)
	}
	l.Stop()
}

func TestWakeDirectoryRejectsSymlinkAndPublicPermissions(t *testing.T) {
	directory := t.TempDir()
	link := filepath.Join(directory, "link")
	if err := os.Symlink(directory, link); err != nil {
		t.Fatal(err)
	}
	if err := secureWakeDirectory(link); err == nil {
		t.Fatal("accepted symlink")
	}
	public := filepath.Join(directory, "public")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(public, 0755); err != nil {
		t.Fatal(err)
	}
	if err := secureWakeDirectory(public); err == nil {
		t.Fatal("accepted public directory")
	}
}

func TestWakeSetupAcceptsDaemonParentAndReusesRegisteredBuild(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o750); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "wake")
	if err := secureWakeDirectory(directory); err != nil {
		t.Fatal(err)
	}
	obsolete := filepath.Join(directory, "ao-wake-obsolete")
	if err := os.WriteFile(obsolete, []byte("obsolete"), 0o700); err != nil {
		t.Fatal(err)
	}
	registered, bootstraps := false, 0
	protocol := lidHelperVersion
	probeSupported := true
	execute := func(_ context.Context, command string, args ...string) ([]byte, error) {
		if command == "/bin/launchctl" {
			switch args[0] {
			case "print":
				if registered {
					return nil, nil
				}
				return nil, errors.New("not registered")
			case "bootstrap":
				registered = true
				bootstraps++
				return nil, nil
			default:
				t.Errorf("must never boot out another coordinator: %v", args)
				return nil, errors.New("unexpected command")
			}
		}
		return json.Marshal(map[string]any{"version": protocol, "supported": probeSupported})
	}
	for i, build := range []string{"packaged", "development"} {
		source := filepath.Join(root, build)
		if err := os.WriteFile(source, []byte(build), 0o700); err != nil {
			t.Fatal(err)
		}
		l := &LidAwake{directory: directory, binary: source, execute: execute, supported: true}
		if err := l.setup(); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			for _, incompatible := range []struct {
				version   int
				supported bool
			}{
				{1, true},
				{lidHelperVersion + 1, true},
				// A protocol-compatible older helper may still reject macOS 27.
				{lidHelperVersion, false},
			} {
				protocol, probeSupported = incompatible.version, incompatible.supported
				// A leftover client lease must not hide upgrade guidance behind
				// an unavailable or old-protocol status file.
				l.id = "previous-lease"
				for _, attempt := range []struct {
					name string
					run  func() error
				}{
					{"explicit setup", l.Setup},
					{"restored preference", func() error { return l.Start(false) }},
				} {
					err := attempt.run()
					if !errors.Is(err, errLidHelperUpgrade) || !strings.Contains(err.Error(), "launchctl bootout gui/$(id -u)/com.aoagents.mobile-wake") {
						t.Fatalf("%s lacks upgrade recovery command: %v", attempt.name, err)
					}
					if supported, active, reason := l.Status(); !supported || active || reason != "helper_upgrade_required" {
						t.Fatalf("%s upgrade status: supported=%v active=%v reason=%q", attempt.name, supported, active, reason)
					}
				}
			}
		}
	}
	if bootstraps != 1 {
		t.Fatalf("registered %d times", bootstraps)
	}
	if st, err := os.Stat(root); err != nil || st.Mode().Perm() != 0o750 {
		t.Fatalf("changed daemon parent: %v %v", st, err)
	}
	if _, err := os.Stat(obsolete); !os.IsNotExist(err) {
		t.Fatalf("obsolete helper survives: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	copies := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "ao-wake-") {
			copies++
		}
	}
	if copies != 1 {
		t.Fatalf("helper copies: %d", copies)
	}
}

func TestLidStopDoesNotBlockStatus(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "leases"), 0o700); err != nil {
		t.Fatal(err)
	}
	l := &LidAwake{directory: directory, supported: true, id: "mine", pid: 42, started: 99}
	if err := l.renew(); err != nil {
		t.Fatal(err)
	}
	publish := func(ids []string) {
		t.Helper()
		data, err := json.Marshal(lidStatus{Version: lidHelperVersion, UpdatedAt: float64(time.Now().UnixMilli()) / 1000, Active: len(ids) > 0, Leases: ids})
		if err != nil {
			t.Fatal(err)
		}
		if err := writeWakeFile(filepath.Join(directory, "status.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	publish([]string{"mine"})
	stopped := make(chan struct{})
	go func() { l.Stop(); close(stopped) }()
	deadline := time.Now().Add(time.Second)
	for {
		_, err := os.Stat(filepath.Join(directory, "leases", "mine.json"))
		if os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stop did not withdraw lease")
		}
		time.Sleep(time.Millisecond)
	}
	statusReturned := make(chan struct{})
	go func() { l.Status(); close(statusReturned) }()
	select {
	case <-statusReturned:
	case <-time.After(time.Second):
		t.Fatal("status blocked behind cleanup polling")
	}
	publish(nil)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not observe acknowledgement")
	}
}

func TestCustomDataDirectoryCannotProbeOrInstallHostWake(t *testing.T) {
	root := t.TempDir()
	l := NewLidAwake(os.Getpid(), filepath.Join(root, "missing-helper"), root)
	supported, active, reason := l.Status()
	if supported || active || reason != "isolated_data_dir" {
		t.Fatalf("custom root accepted: %v %v %s", supported, active, reason)
	}
	if l.directory != "" {
		t.Fatal("custom root selected production wake directory")
	}
}

func TestWakeSetupLockSerializesClients(t *testing.T) {
	name := filepath.Join(t.TempDir(), "setup.lock")
	unlock, err := lockWakeSetup(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if secondUnlock, err := lockWakeSetup(ctx, name); err == nil {
		secondUnlock()
		unlock()
		t.Fatal("second client acquired a held setup lock")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		unlock()
		t.Fatal(err)
	}
	unlock()
	secondUnlock, err := lockWakeSetup(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	secondUnlock()
}

func TestWakeSetupMissingRegisteredPlistReportsUpgradeRecovery(t *testing.T) {
	l := &LidAwake{
		directory: filepath.Join(t.TempDir(), "wake"),
		supported: true,
		execute: func(_ context.Context, command string, args ...string) ([]byte, error) {
			if command != "/bin/launchctl" || len(args) != 2 || args[0] != "print" {
				t.Fatalf("unexpected command against legacy registration: %s %v", command, args)
			}
			return nil, nil // A legacy coordinator is still registered.
		},
	}
	err := l.Setup()
	if !errors.Is(err, errLidHelperUpgrade) || !strings.Contains(err.Error(), "launchctl bootout gui/$(id -u)/com.aoagents.mobile-wake") {
		t.Fatalf("missing legacy recovery guidance: %v", err)
	}
	if supported, active, reason := l.Status(); !supported || active || reason != "helper_upgrade_required" {
		t.Fatalf("legacy registration status: supported=%v active=%v reason=%q", supported, active, reason)
	}
	if _, err := os.Stat(filepath.Join(l.directory, "agent.plist")); !os.IsNotExist(err) {
		t.Fatalf("legacy registration was replaced: %v", err)
	}
}
