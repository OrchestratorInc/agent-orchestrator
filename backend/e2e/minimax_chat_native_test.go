//go:build !windows

package e2e

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/minimaxacp/testfixture"
)

// This test needs a candidate binary with MiniMax registered using a CI-only Go
// source overlay. It never enables registration through a shipping runtime flag.
// The daemon, HTTP API, SQLite projections, native CLI, tools and history are real;
// only the explicitly configured loopback model endpoint is scripted.
func TestNativeMiniMaxRegisteredChatAPI(t *testing.T) {
	if os.Getenv("AO_NATIVE_MINIMAX_ACP_E2E") != "1" {
		t.Skip("set AO_NATIVE_MINIMAX_ACP_E2E=1 with the CI-only MiniMax registry overlay")
	}
	if _, err := exec.LookPath("mcode"); err != nil {
		t.Fatalf("opt-in native MiniMax qualification requires mcode 0.6.5: %v", err)
	}
	// Compile before isolating HOME, preserving the runner's warmed Go caches.
	buildDaemon(t)
	provider := testfixture.New(t)
	for key, value := range provider.Env() {
		t.Setenv(key, value)
	}
	data := t.TempDir()
	testfixture.CaptureFailureLogs(t, data)
	d := startDaemon(t, data)
	assertMiniMaxDaemonOwnership(t, d)
	var settings struct {
		ChatHarnesses []string `json:"chatHarnesses"`
	}
	d.mustCall("GET", "/settings", http.StatusOK, nil, &settings)
	if !slices.Contains(settings.ChatHarnesses, "minimax-code") {
		t.Fatal("candidate daemon did not register MiniMax; apply the isolated CI source overlay")
	}
	project := seedProject(t, d, "minimax-native-chat")
	var projectRead struct {
		Project struct {
			Path string `json:"path"`
		} `json:"project"`
	}
	d.mustCall("GET", "/projects/"+project, http.StatusOK, nil, &projectRead)
	source := projectRead.Project.Path
	if source == "" {
		t.Fatal("project source path was not returned")
	}
	setMiniMaxProjectRules(d, project, "--AO_E2E_INITIAL_CONTEXT_4831")
	spawned := spawn(t, d, map[string]any{
		"projectId": project, "kind": "worker", "harness": "minimax-code",
		"mode": "chat", "prompt": "ao-seed",
	})
	session := spawned.Session.ID
	if spawned.Session.Mode != "chat" {
		t.Fatalf("explicit Chat request became %q", spawned.Session.Mode)
	}
	initial := awaitMiniMaxTurn(t, d, session, "", false)
	provider.AssertRequestContains(t, "ao-seed", "--AO_E2E_INITIAL_CONTEXT_4831")
	identity := readMiniMaxIdentity(t, data, session)
	if !strings.HasPrefix(identity.NativeID, "mvs_") || identity.Workspace == source {
		t.Fatal("Chat did not persist a native identity and isolated workspace")
	}
	var location struct {
		SessionID     string `json:"sessionId"`
		WorkspacePath string `json:"workspacePath"`
	}
	d.mustCall("GET", "/desktop/sessions/"+session+"/workspace", http.StatusOK, nil, &location)
	if location.SessionID != session || location.WorkspacePath != identity.Workspace {
		t.Fatal("desktop workspace route disagrees with the native Chat owner")
	}
	t.Log("PASS registered settings, explicit Chat spawn, native owner and initial private context")

	turnID, _ := send(t, d, session, "ao-edit", "minimax-edit-once")
	var retry struct {
		TurnID    string `json:"turnId"`
		Duplicate bool   `json:"duplicate"`
	}
	d.mustCall("POST", "/sessions/"+session+"/conversation/messages", http.StatusAccepted,
		map[string]any{"text": "ao-edit", "clientMessageId": "minimax-edit-once"}, &retry)
	// The API deliberately returns no turn ID for a duplicate delivery.
	// Assert one durable turn and one native dispatch, rather than requiring
	// the retry response to repeat an ID that the API contract omits.
	if turnID == "" || !retry.Duplicate || retry.TurnID != "" {
		t.Fatalf("retry response: first=%q duplicate=%v retry=%q", turnID, retry.Duplicate, retry.TurnID)
	}
	edited := awaitMiniMaxTurn(t, d, session, turnID, true)
	if len(edited.Turns) != len(initial.Turns)+1 {
		t.Fatalf("retry created an extra durable turn: %s", describe(edited))
	}
	proof := filepath.Join(identity.Workspace, "ao-native-proof.txt")
	assertMiniMaxFile(t, proof, "native-proof")
	if provider.RequestCount("ao-edit") != 1 {
		t.Fatal("retried API send dispatched the native task more than once")
	}
	t.Log("PASS idempotent API send, streamed native projection and one worktree mutation")

	for _, tc := range []struct {
		prompt, file, decision string
	}{
		{"ao-approve", "approved.txt", "allow_once"},
		{"ao-reject", "rejected.txt", "reject_once"},
	} {
		turnID, _ := send(t, d, session, tc.prompt, tc.prompt)
		pending := d.awaitConversation(session, time.Minute, "MiniMax permission request", func(s snapshot) bool {
			_, ok := s.pendingApproval()
			return ok
		})
		approval, _ := pending.pendingApproval()
		if _, err := os.Stat(provider.ProtectedPath(tc.file)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("protected side effect occurred before the API decision")
		}
		decision := miniMaxDecision(t, approval, tc.decision)
		d.mustCall("POST", "/sessions/"+session+"/conversation/approvals/"+approval.RequestID+"/resolve",
			http.StatusNoContent, map[string]any{"decisionId": decision}, nil)
		awaitMiniMaxTurn(t, d, session, turnID, false)
		_, statErr := os.Stat(provider.ProtectedPath(tc.file))
		if tc.decision == "allow_once" && statErr != nil {
			t.Fatal("approved native operation did not execute")
		}
		if tc.decision == "reject_once" && !errors.Is(statErr, os.ErrNotExist) {
			t.Fatal("rejected native operation executed")
		}
		t.Logf("PASS API %s with native offered decision and side-effect boundary", tc.prompt)
	}

	cancelTurn, _ := send(t, d, session, "ao-cancel", "minimax-cancel")
	witness := waitMiniMaxWitness(t, d, session, filepath.Join(identity.Workspace, "started.json"))
	if !testfixture.SameLinuxProcess(witness.PID, witness.Start) {
		t.Fatal("cancellation barrier was not inside the exact executing native tool")
	}
	cancelledAt := time.Now()
	d.mustCall("POST", "/sessions/"+session+"/conversation/interrupt", http.StatusNoContent, nil, nil)
	stopDeadline := time.Now().Add(5 * time.Second)
	for testfixture.SameLinuxProcess(witness.PID, witness.Start) && time.Now().Before(stopDeadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if testfixture.SameLinuxProcess(witness.PID, witness.Start) {
		t.Fatal("API interrupt left the exact native tool process alive")
	}
	cancelled := d.awaitConversation(session, time.Minute, "native cancelled turn settlement", func(s snapshot) bool {
		return len(s.Turns) > 0 && terminal(s.Turns[len(s.Turns)-1].State)
	})
	last := cancelled.Turns[len(cancelled.Turns)-1]
	if last.ID != cancelTurn || last.State != "interrupted" {
		t.Fatalf("API cancellation settlement disagreed: %s", describe(cancelled))
	}
	followup, _ := send(t, d, session, "ao-after-cancel", "minimax-after-cancel")
	awaitMiniMaxTurn(t, d, session, followup, false)
	t.Log("PASS active native tool API cancellation and usable same-session continuation")

	recoveryTurn, _ := send(t, d, session, "ao-recovery", "minimax-recovery")
	recoveryWitness := waitMiniMaxWitness(t, d, session, filepath.Join(identity.Workspace, "recovery-started.json"))
	if !testfixture.SameLinuxProcess(recoveryWitness.PID, recoveryWitness.Start) {
		t.Fatal("daemon recovery barrier was not inside native work")
	}
	host := persistentHostPID(t, data, session)
	hostStart := miniMaxProcessStart(t, host)
	beforeRestart := d.conversation(session)
	d.kill()
	if !testfixture.SameLinuxProcess(host, hostStart) {
		t.Fatal("detached native host died with the daemon")
	}
	restarted := startDaemon(t, data)
	assertMiniMaxDaemonOwnership(t, restarted)
	restarted.awaitLiveController(session, time.Minute)
	recovered := awaitMiniMaxTurn(t, restarted, session, recoveryTurn, true)
	if persistentHostPID(t, data, session) != host || !testfixture.SameLinuxProcess(host, hostStart) {
		t.Fatal("daemon replacement failed to adopt the exact surviving native host")
	}
	if readMiniMaxIdentity(t, data, session) != identity || provider.RequestCount("ao-recovery") != 1 {
		t.Fatal("daemon recovery changed native identity/workspace or duplicated dispatch")
	}
	assertMiniMaxTimeline(t, beforeRestart, recovered)
	t.Log("PASS actual daemon replacement, detached-host adoption and exactly-once recovered turn")

	restarted.mustCall("POST", "/sessions/"+session+"/kill", http.StatusOK, nil, nil)
	killDeadline := time.Now().Add(10 * time.Second)
	for testfixture.SameLinuxProcess(host, hostStart) && time.Now().Before(killDeadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if testfixture.SameLinuxProcess(host, hostStart) {
		t.Fatal("archive left the exact native host alive")
	}
	var terminated struct {
		Session struct {
			IsTerminated bool `json:"isTerminated"`
		} `json:"session"`
	}
	restarted.mustCall("GET", "/sessions/"+session, http.StatusOK, nil, &terminated)
	if !terminated.Session.IsTerminated {
		t.Fatal("archive did not persist termination before fresh instructions")
	}
	// Introduce this token only after independently confirmed native teardown.
	setMiniMaxProjectRules(restarted, project, "--AO_E2E_FRESH_RESTORE_CONTEXT_9457")
	restarted.mustCall("POST", "/sessions/"+session+"/restore", http.StatusOK, nil, nil)
	restored := restarted.awaitLiveController(session, time.Minute)
	if readMiniMaxIdentity(t, data, session) != identity || restored.ConversationID != initial.ConversationID {
		t.Fatal("restore changed native/AO conversation identity or workspace")
	}
	restoredTurn, _ := send(t, restarted, session, "ao-after-restore", "minimax-after-restore")
	final := awaitMiniMaxTurn(t, restarted, session, restoredTurn, true)
	provider.AssertRequestContains(t, "ao-after-restore", "--AO_E2E_FRESH_RESTORE_CONTEXT_9457")
	provider.AssertRequestContains(t, "ao-after-restore", "ao-seed")
	assertMiniMaxFile(t, proof, "native-proofrestored")
	assertMiniMaxTimeline(t, recovered, final)
	for _, msg := range final.Messages {
		if strings.Contains(msg.Text, "AO_E2E_INITIAL_CONTEXT_4831") || strings.Contains(msg.Text, "AO_E2E_FRESH_RESTORE_CONTEXT_9457") {
			t.Fatal("private standing context entered the AO user/assistant transcript")
		}
	}
	t.Log("PASS archive/exact restore, refreshed private context and continuation editing old worktree proof")

	if remaining := 13*time.Second - time.Since(cancelledAt); remaining > 0 {
		time.Sleep(remaining)
	}
	if _, err := os.Stat(filepath.Join(identity.Workspace, "late.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled tool produced a delayed artifact")
	}
	if _, err := os.Stat(filepath.Join(source, "ao-native-proof.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("native task mutated the project source checkout")
	}
	assertMiniMaxFile(t, filepath.Join(source, "README.md"), "# e2e fixture\n")
	status := exec.Command("git", "status", "--porcelain")
	status.Dir = source
	if out, err := status.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("source checkout changed: %v, %s", err, out)
	}
	t.Log("PASS no delayed cancellation artifact and unchanged project source checkout")
}

func setMiniMaxProjectRules(d *daemon, project, rules string) {
	d.mustCall("PUT", "/projects/"+project+"/config", http.StatusOK, map[string]any{
		"config": map[string]any{
			"agentRules":  rules,
			"agentConfig": map[string]any{"model": "custom_provider:ao/native-test", "permissions": "default"},
		},
	}, nil)
}

func awaitMiniMaxTurn(t *testing.T, d *daemon, session, turnID string, approveFixtureTool bool) snapshot {
	t.Helper()
	answered := map[string]bool{}
	snap := d.awaitConversation(session, time.Minute, "MiniMax native turn settlement", func(s snapshot) bool {
		if len(s.Turns) == 0 {
			return false
		}
		// Only the explicitly authorized fixture worktree mutations use this
		// branch. The separate approval/reject cases never auto-answer here.
		if approval, pending := s.pendingApproval(); approveFixtureTool && pending && !answered[approval.RequestID] {
			if approval.TurnID != turnID {
				t.Fatal("fixture approval belongs to a different Chat turn")
			}
			decision := miniMaxDecision(t, approval, "allow_once")
			d.mustCall("POST", "/sessions/"+session+"/conversation/approvals/"+approval.RequestID+"/resolve",
				http.StatusNoContent, map[string]any{"decisionId": decision}, nil)
			answered[approval.RequestID] = true
		}
		last := s.Turns[len(s.Turns)-1]
		return (turnID == "" || last.ID == turnID) && terminal(last.State) && s.Controller == "ready"
	})
	if snap.Turns[len(snap.Turns)-1].State != "completed" {
		t.Fatalf("native Chat turn failed: %s", describe(snap))
	}
	return snap
}

func miniMaxDecision(t *testing.T, approval activity, kind string) string {
	t.Helper()
	var detail struct {
		Decisions []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"decisions"`
	}
	if err := json.Unmarshal(approval.DetailJSON, &detail); err != nil {
		t.Fatal(err)
	}
	for _, option := range detail.Decisions {
		if option.Kind == kind {
			return option.ID
		}
	}
	t.Fatalf("native pending request did not offer %s", kind)
	return ""
}

type miniMaxWitness struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

func waitMiniMaxWitness(t *testing.T, d *daemon, session, path string) miniMaxWitness {
	t.Helper()
	answered := map[string]bool{}
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		var witness miniMaxWitness
		b, err := os.ReadFile(path)
		if err == nil && json.Unmarshal(b, &witness) == nil && witness.PID > 0 {
			return witness
		}
		snap := d.conversation(session)
		if approval, pending := snap.pendingApproval(); pending && !answered[approval.RequestID] {
			decision := miniMaxDecision(t, approval, "allow_once")
			d.mustCall("POST", "/sessions/"+session+"/conversation/approvals/"+approval.RequestID+"/resolve",
				http.StatusNoContent, map[string]any{"decisionId": decision}, nil)
			answered[approval.RequestID] = true
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("native tool witness never appeared: %s", describe(d.conversation(session)))
	return miniMaxWitness{}
}

type miniMaxIdentity struct {
	NativeID, Workspace string
}

func readMiniMaxIdentity(t *testing.T, data, session string) miniMaxIdentity {
	t.Helper()
	uri := url.URL{Scheme: "file", Path: filepath.Join(data, "ao.db"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var identity miniMaxIdentity
	if err := db.QueryRow(`SELECT provider_conversation_id, workspace_path FROM sessions WHERE id = ?`, session).Scan(&identity.NativeID, &identity.Workspace); err != nil {
		t.Fatal(err)
	}
	return identity
}

func miniMaxProcessStart(t *testing.T, pid int) string {
	t.Helper()
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatal(err)
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		t.Fatal("invalid owned host process stat")
	}
	fields := strings.Fields(string(b)[end+1:])
	if len(fields) < 20 {
		t.Fatal("owned host process stat has no kernel start identity")
	}
	return fields[19]
}

func assertMiniMaxFile(t *testing.T, path, want string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil || string(body) != want {
		t.Fatalf("controlled native file mismatch: %v, got %q, want %q", err, body, want)
	}
}

func assertMiniMaxDaemonOwnership(t *testing.T, d *daemon) {
	t.Helper()
	response, err := httpClient.Get(strings.TrimSuffix(d.baseURL, "/api/v1") + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var ready struct {
		PID            int    `json:"pid"`
		ExecutablePath string `json:"executablePath"`
	}
	if err := json.NewDecoder(response.Body).Decode(&ready); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || ready.PID != d.cmd.Process.Pid || ready.ExecutablePath != buildDaemon(t) {
		t.Fatal("readyz did not identify the owned candidate daemon")
	}
}

func assertMiniMaxTimeline(t *testing.T, before, after snapshot) {
	t.Helper()
	seen := map[int64]string{}
	byID := map[string]message{}
	for _, msg := range after.Messages {
		if old, exists := seen[msg.Sequence]; exists {
			t.Fatalf("Chat timeline sequence duplicated by %s and %s", old, msg.ID)
		}
		seen[msg.Sequence] = msg.ID
		byID[msg.ID] = msg
	}
	for _, msg := range before.Messages {
		recovered, exists := byID[msg.ID]
		if !exists || recovered.Sequence != msg.Sequence || recovered.Role != msg.Role {
			t.Fatal("daemon/native recovery lost or duplicated a persisted Chat message")
		}
	}
}
