package conpty

import (
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestInteractiveTerminalEnvDropsLegacyWorkflowLease 删除最终过滤会泄露任一层合并的租约。
func TestInteractiveTerminalEnvDropsLegacyWorkflowLease(t *testing.T) {
	got := interactiveTerminalEnv(
		[]string{"AO_WORKFLOW_LEASE=fake-parent"},
		map[string]string{"aO_wOrKfLoW_lEaSe": "fake-configured", "AO_SESSION_ID": "session-1"},
		[]string{"ao_workflow_lease=fake-assignment"},
	)
	for _, entry := range got {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "AO_WORKFLOW_LEASE") {
			t.Fatal("PTY host environment retained the legacy lease")
		}
	}
	if !slices.Contains(got, "AO_SESSION_ID=session-1") {
		t.Fatal("lease cleanup removed the Session identity")
	}
}

// TestPTYChildDropsLegacyWorkflowLease 删除宿主最终过滤会让旧 host 环境泄露给真实子进程。
func TestPTYChildDropsLegacyWorkflowLease(t *testing.T) {
	t.Setenv("AO_WORKFLOW_LEASE", "fake-legacy-host")
	t.Setenv("AO_TEST_WORKFLOW_LEASE_PROBE", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := newConPTY(t.TempDir(), exe, []string{"-test.run=^TestPTYWorkflowLeaseProbe$"}, 80, 25)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() { _, _ = io.Copy(io.Discard, conn) }()
	select {
	case <-conn.Done():
		if code, exited := conn.ExitCode(); !exited || code != 0 {
			t.Fatalf("PTY lease probe exit=%d exited=%v", code, exited)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("PTY lease probe timed out")
	}
}

// TestPTYWorkflowLeaseProbe 仅在本文件启动的子进程验证假租约，不输出环境值。
func TestPTYWorkflowLeaseProbe(t *testing.T) {
	if os.Getenv("AO_TEST_WORKFLOW_LEASE_PROBE") != "1" {
		return
	}
	if _, present := os.LookupEnv("AO_WORKFLOW_LEASE"); present {
		t.Fatal("child inherited the legacy workflow lease")
	}
}
