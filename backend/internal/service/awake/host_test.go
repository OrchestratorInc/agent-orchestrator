//go:build !windows

package awake

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fallbackAssertionHost struct {
	*fakeHost
	adapter *HostAdapter
}

func (h *fallbackAssertionHost) Start() error { return h.adapter.Start() }
func (h *fallbackAssertionHost) Stop()        { h.adapter.Stop() }
func (h *fallbackAssertionHost) Active() bool { return h.adapter.Active() }
func (h *fallbackAssertionHost) Capabilities() []domain.AwakeCapability {
	return h.adapter.Capabilities()
}
func (h *fallbackAssertionHost) ConfigureIdle(deadline time.Time, display bool) error {
	return h.adapter.ConfigureIdle(deadline, display)
}

func TestBoundSessionTicksKeepFallbackAssertion(t *testing.T) {
	directory := t.TempDir()
	ready := filepath.Join(directory, "ready")
	binary := filepath.Join(directory, "caffeinate")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf ready > \"$AO_AWAKE_TEST_READY\"\nexec /bin/sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	t.Setenv("AO_AWAKE_TEST_READY", ready)
	adapter := NewHost(os.Getpid(), "", directory)
	if adapter.nativeGuard {
		t.Fatal("isolated fixture unexpectedly enabled native assertions")
	}
	t.Cleanup(adapter.Stop)
	s, _, host, _, _ := fixture(t)
	s.host = &fallbackAssertionHost{fakeHost: host, adapter: adapter}
	s.now = time.Now
	start(t, s, domain.StartAwakeRequest{Scope: "session", SessionID: "work", Mode: "indefinite"})
	until := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("fake assertion process did not start")
		}
		time.Sleep(time.Millisecond)
	}
	// A second launch now fails deterministically. Fresh policy deadlines must
	// not reconfigure the still-fresh process, even though every tick differs.
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		tick(t, s)
		if !adapter.Active() {
			t.Fatal("live bound-session reconciliation dropped the fallback assertion")
		}
	}
}
