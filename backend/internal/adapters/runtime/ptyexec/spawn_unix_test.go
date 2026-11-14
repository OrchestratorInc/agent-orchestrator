//go:build !windows

package ptyexec

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCreackPTYCloseIsIdempotent(t *testing.T) {
	p, err := Spawn(context.Background(), []string{"/bin/sh", "-c", "sleep 30"}, nil, 0, 0)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = p.Close()
		_ = p.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("creackPTY.Close did not return: double Close deadlocked on cmd.Wait")
	}
}

func TestCreackPTYResizeSignalsOnIdenticalSize(t *testing.T) {
	p, err := Spawn(context.Background(),
		[]string{"/bin/sh", "-c", `trap 'echo WINCHED' WINCH; while :; do sleep 0.05; done`}, nil, 0, 0)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer p.Close()

	time.Sleep(200 * time.Millisecond)
	if err := p.Resize(24, 80); err != nil {
		t.Fatalf("resize 1: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := p.Resize(24, 80); err != nil {
		t.Fatalf("resize 2: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var out strings.Builder
	buf := make([]byte, 4096)
	for time.Now().Before(deadline) {
		n, err := p.Read(buf)
		if n > 0 {
			out.WriteString(string(buf[:n]))
			if strings.Count(out.String(), "WINCHED") >= 2 {
				return
			}
		}
		if err != nil {
			break
		}
	}
	t.Fatalf("expected 2 WINCHED traps (one per Resize, including the identical one), got output: %q", out.String())
}

func TestCreackPTYSpawnsAtRequestedSize(t *testing.T) {
	p, err := Spawn(context.Background(), []string{"/bin/sh", "-c", "stty size"}, nil, 40, 140)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer p.Close()

	deadline := time.Now().Add(5 * time.Second)
	var out strings.Builder
	buf := make([]byte, 4096)
	for time.Now().Before(deadline) {
		n, readErr := p.Read(buf)
		if n > 0 {
			out.WriteString(string(buf[:n]))
			if strings.Contains(out.String(), "40 140") {
				return
			}
		}
		if readErr != nil {
			break
		}
	}
	t.Fatalf("child did not see the spawn size 40x140, got output: %q", out.String())
}

func TestCreackPTYCloseTermsBeforeKill(t *testing.T) {
	t.Run("cooperative process exits within the grace", func(t *testing.T) {
		p, err := Spawn(context.Background(),
			[]string{"/bin/sh", "-c", `trap 'exit 0' TERM; while :; do sleep 0.05; done`}, nil, 0, 0)
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
		start := time.Now()
		_ = p.Close()
		if elapsed := time.Since(start); elapsed >= detachGrace {
			t.Fatalf("Close took %v: SIGTERM path did not let a cooperative process exit before the kill grace", elapsed)
		}
	})

	t.Run("TERM-ignoring process is killed after the grace", func(t *testing.T) {
		p, err := Spawn(context.Background(),
			[]string{"/bin/sh", "-c", `trap '' TERM; while :; do sleep 0.05; done`}, nil, 0, 0)
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
		done := make(chan struct{})
		go func() {
			_ = p.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Close did not return for a TERM-ignoring process: SIGKILL fallback missing")
		}
	})
}
