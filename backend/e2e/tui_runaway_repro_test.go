//go:build !windows

package e2e

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCLIInterruptKeepsClaudeTUISession(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "tuistop")
	setPermissions(t, d, project, "bypass-permissions")
	pidFile := filepath.Join(d.dataDir, "stop-turn.pid")
	worker := spawn(t, d, map[string]any{
		"projectId": project, "kind": "worker", "harness": "claude-code", "mode": "tui",
		"prompt": fmt.Sprintf("In this disposable test repo, run exactly this shell command:\n```sh\necho $$ > %q; exec sleep 90\n```\nThen report DONE. Do not edit other files.", pidFile),
	})
	if worker.Session.Mode != "tui" {
		t.Fatalf("mode = %q", worker.Session.Mode)
	}
	deadline := time.Now().Add(30 * time.Second)
	var session struct {
		Session struct {
			IsTerminated bool `json:"isTerminated"`
			Activity     struct {
				State string `json:"state"`
			} `json:"activity"`
			Status string `json:"status"`
		} `json:"session"`
	}
	var pid int
	for {
		d.mustCall("GET", "/sessions/"+worker.Session.ID, http.StatusOK, nil, &session)
		if data, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		if session.Session.Activity.State == "active" && pid > 0 && processAlive(pid) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker never started the sleep process: pid=%d session=%+v\n%s", pid, session.Session, d.tailLog())
		}
		time.Sleep(time.Second)
	}
	cmd := exec.Command(buildDaemon(t), "session", "interrupt", worker.Session.ID, "--project", project)
	cmd.Env = append(os.Environ(), "AO_DATA_DIR="+d.dataDir, "AO_RUN_FILE="+filepath.Join(d.dataDir, "running.json"))
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "interrupt requested for session "+worker.Session.ID) {
		t.Fatalf("CLI interrupt: err=%v output=%s", err, out)
	}
	deadline = time.Now().Add(10 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("interrupt did not stop the sleep process %d", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
	d.mustCall("GET", "/sessions/"+worker.Session.ID, http.StatusOK, nil, &session)
	if session.Session.IsTerminated || session.Session.Activity.State == "exited" {
		t.Fatalf("interrupt ended the worker: %+v", session.Session)
	}
}
