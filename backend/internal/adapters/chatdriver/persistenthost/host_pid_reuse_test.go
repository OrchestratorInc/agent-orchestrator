//go:build !windows

package persistenthost

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func closedAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().String()
	_ = ln.Close()
	return address
}

func TestConnectOrStartReusedPIDIsNotRunning(t *testing.T) {
	cases := map[string]struct {
		startedAt time.Time
		want      error
	}{
		"pid reused after the host was recorded": {time.Now().Add(-time.Hour), ErrNotRunning},
		"host process still the recorded owner":  {time.Now().Add(time.Second), ErrOwnershipInconclusive},
		"descriptor without a start time":        {time.Time{}, ErrOwnershipInconclusive},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			cfg := Config{SessionID: "reused-pid", DataDir: dataDir, Workdir: t.TempDir(), ReconnectOnly: true}
			if err := writeDescriptor(dataDir, Descriptor{
				Version: ProtocolVersion, SessionID: cfg.SessionID, Address: closedAddress(t),
				Token: "token", PID: os.Getpid(), StartedAt: tc.startedAt,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := ConnectOrStart(context.Background(), cfg); !errors.Is(err, tc.want) {
				t.Fatalf("ConnectOrStart error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAcquireHostLockReclaimsLockOfReusedPID(t *testing.T) {
	cases := map[string]struct {
		contents string
		wantErr  error
	}{
		"pid reused after the lock was written": {strconv.Itoa(os.Getpid()) + " " + strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10) + "\n", nil},
		"lock owner still alive":                {strconv.Itoa(os.Getpid()) + " " + strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10) + "\n", ErrHostExists},
		"legacy lock without a start time":      {strconv.Itoa(os.Getpid()) + "\n", ErrHostExists},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			path, err := lockPath(dataDir, "locked")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			release, err := acquireHostLock(dataDir, "locked")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("acquireHostLock error = %v, want %v", err, tc.wantErr)
			}
			if err == nil {
				release()
			}
		})
	}
}
