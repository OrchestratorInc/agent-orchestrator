package ptyexec

import (
	"testing"
	"time"
)

func TestWaitForExitBoundsThePostKillWait(t *testing.T) {
	never := make(chan struct{})
	start := time.Now()
	waitForExit(never, 50*time.Millisecond)
	elapsed := time.Since(start)
	if elapsed < 40*time.Millisecond {
		t.Fatalf("waitForExit returned after %v; expected it to wait out the grace", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("waitForExit blocked for %v; expected a bounded return after grace", elapsed)
	}
}

func TestWaitForExitReturnsOnDone(t *testing.T) {
	done := make(chan struct{})
	close(done)
	start := time.Now()
	waitForExit(done, 5*time.Second)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waitForExit took %v on a closed channel; expected an immediate return", elapsed)
	}
}
