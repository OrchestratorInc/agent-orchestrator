//go:build linux

package persistenthost

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// Reproduce a stock ZCode Bash-shaped process: new session/group, provider
// exits on EOF before the child, and a scheduled late filesystem side effect.
func TestLinuxZCodeHostReapsDetachedChildAfterProviderEOF(t *testing.T) {
	t.Run("provider EOF", func(t *testing.T) { testLinuxZCodeHostChild(t, false) })
	t.Run("adopted zombie while provider live", func(t *testing.T) { testLinuxZCodeHostChild(t, true) })
}
func testLinuxZCodeHostChild(t *testing.T, interrupt bool) {
	dir := t.TempDir()
	data := t.TempDir()
	control := exec.Command("sleep", "30")
	if err := control.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Process.Kill(); _ = control.Wait() })
	controlIdentity, err := readProcessIdentity(control.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{SessionID: "zcode-detached-child", DataDir: data, Workdir: dir, Protocol: ProtocolZCode, OwnershipFingerprint: "linux-owned-child-v1", Argv: []string{os.Args[0], "-test.run=^TestLinuxDetachedProviderHelper$"}, Env: append(os.Environ(), "AO_LINUX_TREE_HELPER=provider", "AO_LINUX_TREE_DIR="+dir)}
	if interrupt {
		cfg.Env = append(cfg.Env, "AO_LINUX_TREE_INTERRUPT=1")
	}
	transport, err := ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = transport.Stdin.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = Shutdown(ctx, data, cfg.SessionID)
	})
	marker := filepath.Join(dir, "child.json")
	deadline := time.Now().Add(8 * time.Second)
	var markerData struct {
		PID   int    `json:"pid"`
		Birth string `json:"birth"`
	}
	for {
		if data, err := os.ReadFile(marker); err == nil {
			if err = json.Unmarshal(data, &markerData); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached native-shaped child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The fixture contains only test-owned child identity, never provider data.
	child := ownedProcess{pid: markerData.PID, birth: markerData.Birth}
	t.Cleanup(func() {
		current, err := readProcessIdentity(child.pid)
		if err == nil && current.birth == child.birth {
			_ = signalOwnedProcess(current)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if interrupt {
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := readProcessIdentity(child.pid); err != nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if current, err := readProcessIdentity(child.pid); err == nil && current.birth == child.birth {
			t.Fatalf("interrupt left adopted child unreaped while provider live: %s", current.state)
		}
		if _, err := readDescriptor(data, cfg.SessionID); err != nil {
			t.Fatal("provider host exited instead of remaining available", err)
		}
	}
	if err := Shutdown(ctx, data, cfg.SessionID); err != nil {
		t.Fatal(err)
	}
	if current, err := readProcessIdentity(child.pid); err == nil && current.birth == child.birth {
		t.Fatalf("detached child survived/reaping incomplete: pid=%d state=%s", child.pid, current.state)
	}
	current, err := readProcessIdentity(control.Process.Pid)
	if err != nil || current.birth != controlIdentity.birth || current.state == "Z" {
		t.Fatal("unrelated control process was interrupted")
	}
	time.Sleep(2200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "late")); err == nil {
		t.Fatal("detached child produced its scheduled late side effect")
	}
}

func TestLinuxDetachedProviderHelper(t *testing.T) {
	role := os.Getenv("AO_LINUX_TREE_HELPER")
	if role == "" {
		return
	}
	dir := os.Getenv("AO_LINUX_TREE_DIR")
	if role == "provider" {
		child := exec.Command(os.Args[0], "-test.run=^TestLinuxDetachedProviderHelper$")
		childRole := "child"
		if os.Getenv("AO_LINUX_TREE_INTERRUPT") == "1" {
			childRole = "intermediate"
		}
		child.Env = append(os.Environ(), "AO_LINUX_TREE_HELPER="+childRole)
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		// Deliberately do not wait: stdin EOF ends the provider first.
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	if role == "intermediate" {
		child := exec.Command(os.Args[0], "-test.run=^TestLinuxDetachedProviderHelper$")
		child.Env = append(os.Environ(), "AO_LINUX_TREE_HELPER=child")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		return
	}
	identity, err := readProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"pid": identity.pid, "birth": identity.birth})
	if err := os.WriteFile(filepath.Join(dir, "child.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("AO_LINUX_TREE_INTERRUPT") == "1" {
		time.Sleep(200 * time.Millisecond)
		return
	}
	time.Sleep(2 * time.Second)
	if err := os.WriteFile(filepath.Join(dir, "late"), []byte("late "+strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
}
