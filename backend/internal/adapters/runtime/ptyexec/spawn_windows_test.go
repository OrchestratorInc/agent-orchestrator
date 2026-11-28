//go:build windows

package ptyexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestSpawnWindowsStreamsOutput(t *testing.T) {
	p, err := Spawn(context.Background(), []string{"cmd.exe", "/D", "/Q", "/K"}, nil, 24, 80)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer p.Close()
	if _, err := p.Write([]byte("echo AO_CONPTY_OK\r\n")); err != nil {
		t.Fatalf("write PTY: %v", err)
	}

	out := readPTYUntil(t, p, "AO_CONPTY_OK", 5*time.Second)
	if !strings.Contains(out, "AO_CONPTY_OK") {
		t.Fatalf("output %q does not contain marker", out)
	}
}

func TestSpawnWindowsRejectsEmptyCommand(t *testing.T) {
	_, err := Spawn(context.Background(), nil, nil, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "empty attach command") {
		t.Fatalf("expected empty attach command error, got %v", err)
	}
}

func TestConPTYCloseIsIdempotent(t *testing.T) {
	p, err := Spawn(context.Background(), []string{"cmd.exe", "/D", "/Q", "/K"}, nil, 24, 80)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = p.Close()
		_ = p.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return")
	}
}

func readPTYUntil(t *testing.T, p ports.Stream, marker string, timeout time.Duration) string {
	t.Helper()
	type result struct {
		out string
		err error
	}
	results := make(chan result, 1)
	go func() {
		var buf bytes.Buffer
		tmp := make([]byte, 4096)
		for {
			n, err := p.Read(tmp)
			if n > 0 {
				buf.Write(tmp[:n])
				if strings.Contains(buf.String(), marker) {
					results <- result{out: buf.String()}
					return
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					results <- result{out: buf.String()}
				} else {
					results <- result{out: buf.String(), err: err}
				}
				return
			}
		}
	}()

	select {
	case res := <-results:
		if res.err != nil {
			t.Fatalf("read PTY: %v (output %q)", res.err, res.out)
		}
		return res.out
	case <-time.After(timeout):
		_ = p.Close()
		t.Fatal("timed out reading PTY output")
		return ""
	}
}
