//go:build !windows

package terminal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAttachmentStreamsRealTmuxPane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	t.Setenv("TERM", "xterm-256color")

	name := "ao-term-it-" + strconv.Itoa(os.Getpid())
	rt := tmux.New(tmux.Options{Timeout: 10 * time.Second})
	handle, err := rt.Create(context.Background(), ports.RuntimeConfig{
		SessionID:     domain.SessionID(name),
		WorkspacePath: t.TempDir(),
		Argv:          []string{"sh", "-lc", "printf AO_READY\\n; exec sh -i"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = rt.Destroy(context.Background(), handle) })

	var got safeBytes
	a := newAttachment(name, handle, rt, nil, got.add, nil, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.run(ctx)

	eventually(t, 3*time.Second, func() bool { return a.writeLeased([]byte("echo AO_MARKER_42\n"), nil) == nil })
	eventually(t, 5*time.Second, func() bool { return strings.Contains(got.string(), "AO_MARKER_42") })

	if err := rt.Destroy(context.Background(), handle); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return a.isExited() })
}

func TestAttachmentExitsOnDestroyUnderDetachOnDestroyOff(t *testing.T) {
	systemTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	t.Setenv("TERM", "xterm-256color")

	dir := t.TempDir()
	conf := filepath.Join(dir, "tmux.conf")
	if err := os.WriteFile(conf, []byte("set -g detach-on-destroy off\n"), 0o600); err != nil {
		t.Fatalf("write tmux.conf: %v", err)
	}
	wrapper := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\nexec '" + systemTmux + "' -f '" + conf + "' \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatalf("write tmux wrapper: %v", err)
	}
	socket := "ao-term-dod-it-" + strconv.Itoa(os.Getpid())
	t.Cleanup(func() { _ = exec.Command(wrapper, "-L", socket, "kill-server").Run() })

	decoy := "user-session-" + strconv.Itoa(os.Getpid())
	if out, err := exec.Command(wrapper, "-L", socket, "new-session", "-d", "-s", decoy, "sh", "-c", "sleep 300").CombinedOutput(); err != nil {
		t.Fatalf("create decoy session: %v: %s", err, out)
	}

	name := "ao-term-dod-" + strconv.Itoa(os.Getpid())
	rt := tmux.New(tmux.Options{Binary: wrapper, LegacyBinary: wrapper, SocketName: socket, Timeout: 10 * time.Second})
	handle, err := rt.Create(context.Background(), ports.RuntimeConfig{
		SessionID:     domain.SessionID(name),
		WorkspacePath: t.TempDir(),
		Argv:          []string{"sh", "-lc", "printf AO_READY\\n; exec sh -i"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = rt.Destroy(context.Background(), handle) })

	var got safeBytes
	a := newAttachment(name, handle, rt, nil, got.add, nil, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.run(ctx)
	eventually(t, 5*time.Second, func() bool { return strings.Contains(got.string(), "AO_READY") })

	if err := rt.Destroy(context.Background(), handle); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return a.isExited() })
}

func TestAttachmentReattachAdoptsNewSize(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	t.Setenv("TERM", "xterm-256color")

	name := "ao-term-size-it-" + strconv.Itoa(os.Getpid())
	rt := tmux.New(tmux.Options{Timeout: 10 * time.Second})
	handle, err := rt.Create(context.Background(), ports.RuntimeConfig{
		SessionID:     domain.SessionID(name),
		WorkspacePath: t.TempDir(),
		Argv:          []string{"sh", "-lc", "printf AO_READY\\n; exec sh -i"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = rt.Destroy(context.Background(), handle) })

	attachAt := func(rows, cols uint16) (*attachment, *safeBytes, <-chan struct{}, context.CancelFunc) {
		var got safeBytes
		opened := make(chan struct{})
		a := newAttachment(name, handle, rt, func() { close(opened) }, got.add, nil, testLogger())
		if err := a.resize(rows, cols); err != nil {
			t.Fatalf("record size: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		go a.run(ctx)
		return a, &got, opened, cancel
	}

	a, _, openedA, cancelA := attachAt(37, 115)
	select {
	case <-openedA:
	case <-time.After(10 * time.Second):
		t.Fatal("client A did not attach")
	}
	a.close()
	cancelA()

	b, gotB, openedB, cancelB := attachAt(40, 148)
	defer cancelB()
	defer b.close()
	select {
	case <-openedB:
	case <-time.After(10 * time.Second):
		t.Fatal("client B did not attach")
	}

	var captured string
	gotWidth := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_ = b.writeLeased([]byte("echo SIZE:$(stty size)\n"), nil)
		time.Sleep(250 * time.Millisecond)
		captured = gotB.string()
		i := strings.LastIndex(captured, "SIZE:")
		if i < 0 {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(captured[i:], "SIZE:"))
		if len(fields) < 2 {
			continue
		}
		cols, err := strconv.Atoi(strings.TrimFunc(fields[1], func(r rune) bool { return r < '0' || r > '9' }))
		if err != nil {
			continue
		}
		if cols > 130 {
			gotWidth = true
			break
		}
	}
	if !gotWidth {
		t.Fatalf("reattached pane never reported B's width (cols>130) within 30s; captured:\n%q", captured)
	}
}
