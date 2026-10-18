//go:build !windows

package mobilebridge

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"
)

// A stand-in for caffeinate that just stays alive, so the test exercises the
// process lifecycle without touching the machine's power state.
func fakeCaffeinate(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "caffeinate")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestKeepAwakeStartStop(t *testing.T) {
	k := &KeepAwake{binary: fakeCaffeinate(t), ownerPID: os.Getpid()}
	if k.Active() {
		t.Fatal("active before Start")
	}
	if err := k.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	first := k.cmd
	if err := k.Start(); err != nil || k.cmd != first {
		t.Fatal("Start should be idempotent")
	}
	if !k.Active() {
		t.Fatal("not active after Start")
	}
	k.Stop()
	if k.Active() {
		t.Fatal("still active after Stop")
	}
	k.Stop() // idempotent
}

func TestKeepAwakeStartFailsForMissingBinary(t *testing.T) {
	k := &KeepAwake{binary: filepath.Join(t.TempDir(), "missing"), ownerPID: os.Getpid()}
	if err := k.Start(); err == nil {
		t.Fatal("expected Start to fail")
	}
	if k.Active() {
		t.Fatal("active after a failed Start")
	}
}

func TestKeepAwakeRollingDeadlineKeepsLiveAssertion(t *testing.T) {
	k := &KeepAwake{binary: fakeCaffeinate(t), ownerPID: os.Getpid()}
	t.Cleanup(k.Stop)
	deadline := time.Now().Add(15 * time.Second)
	if err := k.ConfigureIdle(deadline, false); err != nil {
		t.Fatal(err)
	}
	if err := k.Start(); err != nil {
		t.Fatal(err)
	}
	k.mu.Lock()
	first := k.cmd
	k.mu.Unlock()
	for i := 1; i <= 2; i++ {
		if err := k.ConfigureIdle(deadline.Add(time.Duration(i)*5*time.Second), false); err != nil {
			t.Fatal(err)
		}
		k.mu.Lock()
		unchanged := k.cmd == first
		k.mu.Unlock()
		if !unchanged {
			t.Fatal("rolling policy renewal replaced a still-fresh idle assertion")
		}
	}
}

func TestKeepAwakeRenewalOverlapsAndCapsRetiringAssertions(t *testing.T) {
	now := time.Now()
	k := &KeepAwake{binary: fakeCaffeinate(t), ownerPID: os.Getpid(), now: func() time.Time { return now }}
	t.Cleanup(k.Stop)
	if err := k.ConfigureIdle(now.Add(15*time.Second), false); err != nil {
		t.Fatal(err)
	}
	if err := k.Start(); err != nil {
		t.Fatal(err)
	}
	k.mu.Lock()
	first, firstDone := k.cmd, k.done
	k.mu.Unlock()
	now = now.Add(5 * time.Second)
	if err := k.ConfigureIdle(now.Add(15*time.Second), false); err != nil {
		t.Fatal(err)
	}
	k.mu.Lock()
	unchanged := k.cmd == first
	k.mu.Unlock()
	if !unchanged {
		t.Fatal("five-second policy tick restarted a fresh process")
	}
	// A tick just before the five-second-remaining boundary still renews;
	// it must not defer replacement until the process is about to expire.
	now = now.Add(4999 * time.Millisecond)
	if err := k.ConfigureIdle(now.Add(15*time.Second), false); err != nil {
		t.Fatal(err)
	}
	k.mu.Lock()
	second, secondDone := k.cmd, k.done
	_, retained := k.retiring[first]
	k.mu.Unlock()
	if second == first || !retained || !slices.Equal(second.Args[1:], []string{"-i", "-w", strconv.Itoa(os.Getpid()), "-t", "15"}) {
		t.Fatal("renewal did not retain a bounded overlapping assertion")
	}
	select {
	case <-firstDone:
		t.Fatal("old assertion ended before its replacement could acquire ownership")
	default:
	}
	// A shorter policy during a handoff must cap every older process too.
	deadline := now.Add(100 * time.Millisecond)
	if err := k.ConfigureIdle(deadline, false); err != nil {
		t.Fatal(err)
	}
	k.mu.Lock()
	for _, retired := range k.retiring {
		if retired.expires.After(deadline) {
			k.mu.Unlock()
			t.Fatal("retiring assertion outlives the shorter policy")
		}
	}
	k.mu.Unlock()
	for _, done := range []chan struct{}{firstDone, secondDone} {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("shortened policy did not retire an overlapping assertion")
		}
	}
	k.Stop()
	if k.Active() {
		t.Fatal("Stop left an owned assertion behind")
	}
}

func TestKeepAwakeFailedReplacementReleasesAllAssertions(t *testing.T) {
	k := &KeepAwake{binary: fakeCaffeinate(t), ownerPID: os.Getpid()}
	t.Cleanup(k.Stop)
	if err := k.Start(); err != nil {
		t.Fatal(err)
	}
	if err := k.ConfigureIdle(time.Now().Add(time.Minute), true); err != nil {
		t.Fatal(err)
	}
	k.binary = filepath.Join(t.TempDir(), "missing")
	if err := k.ConfigureIdle(time.Now().Add(time.Second), false); err == nil {
		t.Fatal("replacement failure was hidden")
	}
	if k.Active() {
		t.Fatal("failed replacement retained the older indefinite assertion")
	}
}
