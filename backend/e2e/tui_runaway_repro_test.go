//go:build !windows

package e2e

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLIInterruptKeepsClaudeTUISession(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "tuistop")
	setPermissions(t, d, project, "bypass-permissions")
	worker := spawn(t, d, map[string]any{
		"projectId": project, "kind": "worker", "harness": "claude-code", "mode": "tui",
		"prompt": "In this disposable test repo, run the shell command sleep 90, then report DONE. Do not edit files.",
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
	for {
		d.mustCall("GET", "/sessions/"+worker.Session.ID, http.StatusOK, nil, &session)
		if session.Session.Activity.State == "active" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker never became active: %+v", session.Session)
		}
		time.Sleep(time.Second)
	}
	cmd := exec.Command(buildDaemon(t), "session", "interrupt", worker.Session.ID, "--project", project)
	cmd.Env = append(os.Environ(), "AO_DATA_DIR="+d.dataDir, "AO_RUN_FILE="+filepath.Join(d.dataDir, "running.json"))
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "interrupt requested for session "+worker.Session.ID) {
		t.Fatalf("CLI interrupt: err=%v output=%s", err, out)
	}
	time.Sleep(2 * time.Second)
	d.mustCall("GET", "/sessions/"+worker.Session.ID, http.StatusOK, nil, &session)
	if session.Session.IsTerminated || session.Session.Activity.State == "exited" {
		t.Fatalf("interrupt ended the worker: %+v", session.Session)
	}
}
