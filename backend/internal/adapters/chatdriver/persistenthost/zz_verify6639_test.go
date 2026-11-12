package persistenthost

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"testing"
	"time"
)

func closedLoopbackAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close() // nothing listens any more: dials are actively refused
	return addr
}

// Finding 2: the pre-restart host is gone (dial refused) but the recorded pid
// now belongs to an unrelated process (pid reuse, common on Windows).
func TestVerify6639RefusedDialWithReusedPidIsInconclusive(t *testing.T) {
	dataDir := t.TempDir()
	const sessionID = "ccta-1"
	recordedAt := time.Now().Add(-time.Hour) // host.json written before the restart
	unrelated := exec.Command("sleep", "60") // a process started after the old host died
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unrelated.Process.Kill(); _ = unrelated.Wait() }()
	addr := closedLoopbackAddr(t)
	if err := writeDescriptor(dataDir, Descriptor{Version: ProtocolVersion, SessionID: sessionID, Address: addr, Token: "t", PID: unrelated.Process.Pid, StartedAt: recordedAt}); err != nil {
		t.Fatal(err)
	}
	_, err := ConnectOrStart(context.Background(), Config{SessionID: sessionID, DataDir: dataDir, Workdir: t.TempDir(), ReconnectOnly: true})
	t.Logf("pid reused by unrelated process, dial refused -> err = %v", err)
	t.Logf("errors.Is(err, ErrOwnershipInconclusive) = %v", errors.Is(err, ErrOwnershipInconclusive))
}

// Control: same refused dial with a dead pid is treated as dead (falls through).
func TestVerify6639RefusedDialWithDeadPidIsDead(t *testing.T) {
	dataDir := t.TempDir()
	const sessionID = "ccta-2"
	gone := exec.Command("true")
	_ = gone.Run()
	addr := closedLoopbackAddr(t)
	if err := writeDescriptor(dataDir, Descriptor{Version: ProtocolVersion, SessionID: sessionID, Address: addr, Token: "t", PID: gone.Process.Pid, StartedAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	_, err := ConnectOrStart(context.Background(), Config{SessionID: sessionID, DataDir: dataDir, Workdir: t.TempDir(), ReconnectOnly: true})
	t.Logf("dead pid, dial refused -> err = %v (ErrNotRunning=%v, inconclusive=%v)", err, errors.Is(err, ErrNotRunning), errors.Is(err, ErrOwnershipInconclusive))
}
