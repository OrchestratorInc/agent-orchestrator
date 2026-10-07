//go:build !windows

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// TestFakeApprovalACP is launched by the disposable opencode shim below. It
// deliberately leaves the prompt blocked until AO answers the approval.
func TestFakeApprovalACP(t *testing.T) {
	if os.Getenv("AO_E2E_FAKE_APPROVAL_ACP") != "1" {
		return
	}
	logPath := os.Getenv("AO_E2E_ACP_CALLS")
	record := func(method string) {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(2)
		}
		_, _ = fmt.Fprintln(f, method)
		_ = f.Close()
	}
	record(fmt.Sprintf("provider-pid:%d", os.Getpid()))
	var promptID json.RawMessage
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var frame struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &frame) != nil {
			continue
		}
		if frame.Method == "" && string(frame.ID) == `"permission-1"` {
			record("permission-response")
			_, _ = fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"fake-provider-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"approved once"}}}}`)
			_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}`+"\n", promptID)
			continue
		}
		record(frame.Method)
		switch frame.Method {
		case "initialize":
			_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"resume":{}}},"authMethods":[]}}`+"\n", frame.ID)
		case "session/new", "session/load", "session/resume":
			_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"fake-provider-session"}}`+"\n", frame.ID)
		case "session/prompt":
			promptID = append(promptID[:0], frame.ID...)
			record("session/request_permission")
			_, _ = fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","id":"permission-1","method":"session/request_permission","params":{"sessionId":"fake-provider-session","toolCall":{"toolCallId":"tool-1","title":"Approve restart","kind":"edit"},"options":[{"optionId":"allow","name":"Allow","kind":"allow_once"},{"optionId":"reject","name":"Reject","kind":"reject_once"}]}}`)
		default:
			if len(frame.ID) > 0 {
				_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{}}`+"\n", frame.ID)
			}
		}
	}
	os.Exit(0)
}

func TestPendingACPApprovalSurvivesDaemonSIGKILL(t *testing.T) {
	binDir := t.TempDir()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim := "#!/bin/sh\ncase \"$1\" in\n  acp) exec \"$AO_E2E_TEST_BINARY\" -test.run='^TestFakeApprovalACP$' ;;\n  auth) printf '0 credentials\\n' ;;\n  --version) printf '1.0.0\\n' ;;\n  *) exit 2 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OPENCODE_DATA_DIR", t.TempDir())
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	t.Setenv("AO_E2E_TEST_BINARY", testBinary)
	t.Setenv("AO_E2E_FAKE_APPROVAL_ACP", "1")
	callsPath := filepath.Join(t.TempDir(), "provider-calls.log")
	t.Setenv("AO_E2E_ACP_CALLS", callsPath)

	dataDir := t.TempDir()
	d := startDaemon(t, dataDir)
	project := seedProject(t, d, "fake-approval-restart")
	setPermissions(t, d, project, "default")
	session := spawn(t, d, map[string]any{
		"projectId": project, "kind": "worker", "harness": "opencode", "mode": "chat",
		"prompt": "Request approval, then finish.",
	}).Session.ID
	before := d.awaitConversation(session, 30*time.Second, "pending approval before restart", func(s snapshot) bool {
		_, ok := s.pendingApproval()
		return ok
	})
	approval, _ := before.pendingApproval()
	if approval.RequestID == "" || len(approval.decisions()) != 2 || len(before.Turns) != 1 || len(before.Activities) != 1 {
		t.Fatalf("invalid pending approval before restart:\n%s", describe(before))
	}
	var read struct {
		Session struct {
			Status string `json:"status"`
		} `json:"session"`
	}
	d.mustCall("GET", "/sessions/"+session, http.StatusOK, nil, &read)
	if read.Session.Status != "needs_input" {
		t.Fatalf("status before restart = %q, want needs_input", read.Session.Status)
	}
	hostPID := persistentHostPID(t, dataDir, session)
	d.kill()
	if !processAlive(hostPID) {
		t.Fatalf("detached ACP host %d died with daemon", hostPID)
	}
	restarted := startDaemon(t, dataDir)
	restarted.awaitLiveController(session, 30*time.Second)
	after := restarted.conversation(session)
	replayed, pending := after.pendingApproval()
	if !pending || replayed.RequestID != approval.RequestID || len(after.Turns) != 1 || len(after.Activities) != 1 ||
		after.Turns[0].ID != before.Turns[0].ID || terminal(after.Turns[0].State) {
		t.Fatalf("pending approval changed across restart:\n%s", describe(after))
	}
	restarted.mustCall("GET", "/sessions/"+session, http.StatusOK, nil, &read)
	if read.Session.Status != "needs_input" {
		t.Fatalf("status after restart = %q, want needs_input", read.Session.Status)
	}
	restarted.mustCall("POST", "/sessions/"+session+"/conversation/approvals/"+replayed.RequestID+"/resolve",
		http.StatusNoContent, map[string]any{"decisionId": "allow"}, nil)
	finished := restarted.awaitConversation(session, 30*time.Second, "approved turn to finish", func(s snapshot) bool {
		return len(s.Turns) == 1 && terminal(s.Turns[0].State) &&
			(s.Turns[0].State != "completed" || contains(s.assistantText(), "approved once"))
	})
	if finished.Turns[0].State != "completed" || !contains(finished.assistantText(), "approved once") ||
		len(finished.Activities) != 1 || finished.Activities[0].Status != "resolved" {
		t.Fatalf("approved turn failed:\n%s", describe(finished))
	}
	if got := persistentHostPID(t, dataDir, session); got != hostPID {
		t.Fatalf("provider host replaced across restart: %d -> %d", hostPID, got)
	}
	calls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"provider-pid:", "initialize", "session/new", "session/prompt", "session/request_permission", "permission-response"} {
		if got := strings.Count(string(calls), method); got != 1 {
			t.Fatalf("provider method %s called %d times:\n%s", method, got, calls)
		}
	}
	restarted.mustCall("POST", "/sessions/"+session+"/kill", http.StatusOK, nil, nil)
}

// A daemon-only crash can leave the real setup shell alive. Preserve its
// ownership and payload; neither client replay nor manual resume proves it stopped.
func TestFirstTaskWorkspacePreservedAfterDaemonOnlySIGKILL(t *testing.T) {
	binDir := t.TempDir()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim := "#!/bin/sh\ncase \"$1\" in\n  acp) exec \"$AO_E2E_TEST_BINARY\" -test.run='^TestFakeApprovalACP$' ;;\n  auth) printf '0 credentials\\n' ;;\n  --version) printf '1.0.0\\n' ;;\n  *) exit 2 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OPENCODE_DATA_DIR", t.TempDir())
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	t.Setenv("AO_E2E_TEST_BINARY", testBinary)
	t.Setenv("AO_E2E_FAKE_APPROVAL_ACP", "1")
	callsPath := filepath.Join(t.TempDir(), "provider-calls.log")
	t.Setenv("AO_E2E_ACP_CALLS", callsPath)

	dataDir := t.TempDir()
	d := startDaemon(t, dataDir)
	project := seedProject(t, d, "first-task-kill")
	marker := filepath.Join(t.TempDir(), "provisioning")
	release := filepath.Join(t.TempDir(), "release")
	postCreate := fmt.Sprintf("printf '%%s\\n%%s\\n%%s\\n' \"$$\" \"$AO_WORKSPACE_PATH\" \"$AO_SESSION_ID\" > %q; while [ ! -e %q ]; do sleep 0.05; done; printf 'surviving-hook-wrote' > orphan-payload", marker, release)
	d.mustCall("PUT", "/projects/"+project+"/config", http.StatusOK, map[string]any{
		"config": map[string]any{"postCreate": []string{postCreate}},
	}, nil)
	req := map[string]any{
		"projectId": project, "kind": "worker", "harness": "opencode", "mode": "chat",
		"prompt": "Request approval, then finish.", "clientRequestId": "first-task-retry",
	}
	done := make(chan struct{})
	go func() {
		_, _ = d.call("POST", "/sessions", req, nil)
		close(done)
	}()
	var owner []string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		body, err := os.ReadFile(marker)
		if err == nil {
			owner = strings.Split(strings.TrimSpace(string(body)), "\n")
			if len(owner) == 3 && owner[2] != "" {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(owner) != 3 {
		t.Fatalf("real setup never reported its PID/path/session: %v\n%s", owner, d.tailLog())
	}
	hookPID, err := strconv.Atoi(owner[0])
	if err != nil {
		t.Fatal(err)
	}
	pgid := d.pgid
	defer func() {
		// Own only this test's daemon group, including the surviving shell.
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		for deadline := time.Now().Add(5 * time.Second); processAlive(hookPID); {
			if time.Now().After(deadline) {
				t.Errorf("test-owned hook %d remains alive", hookPID)
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	st, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	before, ok, err := st.GetSession(context.Background(), domain.SessionID(owner[2]))
	if err != nil || !ok || before.Metadata.WorkspacePath != owner[1] || before.ClientRequestCommitted {
		t.Fatalf("real hook ownership not published: %+v found=%v error=%v", before, ok, err)
	}
	var projectResponse struct {
		Project struct {
			Path string `json:"path"`
		} `json:"project"`
	}
	d.mustCall("GET", "/projects/"+project, http.StatusOK, nil, &projectResponse)
	assertPreserved := func(dirty bool) {
		t.Helper()
		current, ok, err := st.GetSession(context.Background(), before.ID)
		if err != nil || !ok || current.Metadata.WorkspacePath != before.Metadata.WorkspacePath ||
			current.Metadata.Branch != before.Metadata.Branch || current.IsTerminated || current.ClientRequestCommitted ||
			current.Metadata.ControllerGeneration != "" || current.Metadata.ProviderConversationID != "" {
			t.Fatalf("uncertain row lost ownership or launched: %+v found=%v error=%v", current, ok, err)
		}
		if info, err := os.Stat(owner[1]); err != nil || !info.IsDir() {
			t.Fatalf("actual workspace disappeared: %v", err)
		}
		registrations, err := exec.Command("git", "-C", projectResponse.Project.Path, "worktree", "list", "--porcelain").Output()
		if err != nil || !strings.Contains(string(registrations), "worktree "+owner[1]+"\n") ||
			strings.Count("\n"+string(registrations), "\nworktree ") != 2 {
			t.Fatalf("captured Git registration changed: %v\n%s", err, registrations)
		}
		status, err := exec.Command("git", "-C", owner[1], "status", "--porcelain", "--untracked-files=all").Output()
		if err != nil || (!dirty && len(status) != 0) || (dirty && !strings.Contains(string(status), "orphan-payload")) {
			t.Fatalf("independent dirty/clean oracle failed: dirty=%v status=%q error=%v", dirty, status, err)
		}
		if dirty {
			body, err := os.ReadFile(filepath.Join(owner[1], "orphan-payload"))
			if err != nil || string(body) != "surviving-hook-wrote" {
				t.Fatalf("dirty payload lost: %q %v", body, err)
			}
		}
	}
	assertPreserved(false)
	daemonPID := d.cmd.Process.Pid
	if err := d.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = d.cmd.Process.Wait()
	d.cmd = nil
	d.waitPortFree()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("first task request did not end after daemon-only SIGKILL")
	}
	if !processAlive(hookPID) {
		t.Fatal("test did not retain the controlled surviving hook")
	}
	restarted := startDaemon(t, dataDir)
	// Observe completion of the actual background health pass, not a timed
	// assumption or row-disappearance proxy for physical safety.
	for deadline := time.Now().Add(10 * time.Second); ; {
		log, err := os.ReadFile(restarted.logPath)
		if err == nil && strings.Contains(string(log), "writer stop is unproven") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background health did not report missing writer-stop evidence:\n%s", restarted.tailLog())
		}
		time.Sleep(20 * time.Millisecond)
	}
	assertPreserved(false)
	refuse := func() {
		t.Helper()
		var response apiError
		status, err := restarted.call("POST", "/sessions", req, &response)
		if err != nil || status != http.StatusConflict || response.Code != "CLIENT_REQUEST_INCOMPLETE" {
			t.Fatalf("same request replay was not refused: status=%d response=%+v error=%v", status, response, err)
		}
		status, err = restarted.call("POST", "/sessions/"+owner[2]+"/resume-agent", nil, &response)
		if err != nil || status < 400 || !strings.Contains(response.Message, "writer stop is unproven") {
			t.Fatalf("manual resume did not report missing writer-stop evidence: status=%d response=%+v error=%v", status, response, err)
		}
		status, err = restarted.call("POST", "/sessions/"+owner[2]+"/kill", nil, nil)
		if err != nil || status < 400 {
			t.Fatalf("cleanup removed a possible setup writer: status=%d error=%v", status, err)
		}
	}
	refuse()
	assertPreserved(false)
	if !processAlive(hookPID) {
		t.Fatal("recovery stopped the controlled hook instead of preserving it")
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		if body, _ := os.ReadFile(filepath.Join(owner[1], "orphan-payload")); string(body) == "surviving-hook-wrote" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("surviving hook could not write its captured workspace")
		}
		time.Sleep(20 * time.Millisecond)
	}
	refuse()
	assertPreserved(true)
	var sessions struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	restarted.mustCall("GET", "/sessions", http.StatusOK, nil, &sessions)
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].ID != owner[2] {
		t.Fatalf("replay duplicated or lost the original session: %+v", sessions.Sessions)
	}
	calls, err := os.ReadFile(callsPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("uncertain launch started a provider: %s", calls)
	}
	t.Logf("daemon-only SIGKILL pid=%d; preserved real hook pid=%d, row, path, registration and dirty payload; replay/resume/cleanup refused", daemonPID, hookPID)
}
