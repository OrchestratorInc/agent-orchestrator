package cli

import (
	"cmp"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type switchCommandCalls struct {
	mu           sync.Mutex
	sessions     string
	switched     []string
	switches     []map[string]any
	sends        []string
	busySends    int
	busyCode     string
	refusedSends int
	switchBody   string
}

func switchCommandServer(t *testing.T, status int, code string) (*httptest.Server, *switchCommandCalls) {
	t.Helper()
	calls := &switchCommandCalls{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.mu.Lock()
		defer calls.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions":
			_, _ = w.Write([]byte(calls.sessions))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/sessions/"):
			_, _ = w.Write([]byte(`{"session":{"id":"` + strings.Split(r.URL.Path, "/")[4] + `","harness":"claude-code"}}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/provider-switch"):
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			calls.switches = append(calls.switches, in)
			calls.switched = append(calls.switched, strings.Split(r.URL.Path, "/")[4])
			if code != "" {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"conflict","code":"` + code + `","message":"refused"}`))
				return
			}
			_, _ = w.Write([]byte(cmp.Or(calls.switchBody, `{"status":"ok"}`)))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/send"):
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if calls.refusedSends < calls.busySends {
				calls.refusedSends++
				w.WriteHeader(http.StatusConflict)
				code := cmp.Or(calls.busyCode, "SESSION_COMPOSER_BUSY")
				_, _ = w.Write([]byte(`{"error":"conflict","code":"` + code + `","message":"busy"}`))
				return
			}
			message, _ := in["message"].(string)
			calls.sends = append(calls.sends, message)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func TestSessionSwitchForcedWithMessage(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, 0, "")
	writeRunFileFor(t, cfg, srv)
	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "switch", "demo-1", "codex", "--force", "-m", "Claude is down. Take over.")
	if err != nil {
		t.Fatalf("session switch failed: %v\nstderr=%s", err, errOut)
	}
	if len(calls.switches) != 1 {
		t.Fatalf("switch calls = %v", calls.switches)
	}
	body := calls.switches[0]
	if body["targetHarness"] != "codex" || body["model"] != nil || body["force"] != true || body["idempotencyKey"] == "" || body["message"] != nil {
		t.Fatalf("switch body = %v", body)
	}
	if len(calls.sends) != 1 || !strings.HasSuffix(calls.sends[0], "Claude is down. Take over.") {
		t.Fatalf("sent messages = %q", calls.sends)
	}
	if !strings.Contains(out, "switched demo-1 to codex") || !strings.Contains(out, "sent message to demo-1") {
		t.Fatalf("output = %q", out)
	}
}

func TestSessionSwitchWaitsForTheNewAgentToTakeTheMessage(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, 0, "")
	calls.busySends = 2
	writeRunFileFor(t, cfg, srv)
	sleeps := 0
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }, Sleep: func(time.Duration) { sleeps++ }},
		"session", "switch", "demo-1", "codex", "-m", "take over")
	if err != nil || len(calls.sends) != 1 || sleeps != 2 {
		t.Fatalf("err = %v sends = %q sleeps = %d", err, calls.sends, sleeps)
	}
}

func TestSessionSwitchToTheRunningAgentSucceeds(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := switchCommandServer(t, http.StatusConflict, "PROVIDER_SWITCH_already_active")
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "switch", "demo-1", "codex")
	if err != nil || !strings.Contains(out, "demo-1 is already on codex") {
		t.Fatalf("out = %q err = %v", out, err)
	}
}

func TestSessionSwitchBusyPointsAtForce(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, http.StatusConflict, "PROVIDER_SWITCH_busy")
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "switch", "demo-1", "codex", "-m", "hi")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v exit = %d", err, ExitCode(err))
	}
	if len(calls.sends) != 0 {
		t.Fatalf("a refused switch sent %q", calls.sends)
	}
}

func TestSessionSwitchNeedsAnAgent(t *testing.T) {
	setConfigEnv(t)
	for _, args := range [][]string{{"demo-1"}, {"demo-1", "codex", "extra"}, {"--from", "claude", "demo-1", "codex"}, {"--from", "codex", "codex"}, {"--from", "claude"},
		{"demo-1", "codex", "--permission", "sometimes"}} {
		_, _, err := executeCLI(t, Deps{}, append([]string{"session", "switch"}, args...)...)
		if ExitCode(err) != 2 {
			t.Fatalf("%v: err = %v exit = %d", args, err, ExitCode(err))
		}
	}
}

func TestSessionListFiltersByAgentQuietly(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/sessions" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"sessions":[
			{"id":"demo-2","projectId":"demo","kind":"worker","harness":"claude-code","activity":{"state":"active"}},
			{"id":"demo-1","projectId":"demo","kind":"worker","harness":"claude-code","activity":{"state":"idle"}},
			{"id":"demo-3","projectId":"demo","kind":"worker","harness":"codex","activity":{"state":"idle"}}]}`))
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "ls", "--agent", "Claude-Code", "-q")
	if err != nil || out != "demo-1\ndemo-2\n" {
		t.Fatalf("out = %q err = %v", out, err)
	}
	out, _, err = executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "ls", "--agent", "claude", "-q")
	if err != nil || out != "demo-1\ndemo-2\n" {
		t.Fatalf("ls --agent claude: out = %q err = %v", out, err)
	}
	_, _, err = executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "ls", "--agent", "claude-c", "-q")
	if ExitCode(err) != 2 || !strings.Contains(err.Error(), "did you mean claude-code") {
		t.Fatalf("ls --agent claude-c: err = %v exit = %d", err, ExitCode(err))
	}
}

func TestSessionSwitchRejectsAnUnknownAgentLocally(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, 0, "")
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "switch", "demo-1", "codx")
	if ExitCode(err) != 2 || len(calls.switches) != 0 {
		t.Fatalf("err = %v exit = %d switches = %d", err, ExitCode(err), len(calls.switches))
	}
}

func TestSendRetriesWhileTheSessionIsSwitching(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, 0, "")
	calls.busySends, calls.busyCode = 2, "CHAT_INTERFACE_TRANSITION"
	writeRunFileFor(t, cfg, srv)
	sleeps := 0
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }, Sleep: func(time.Duration) { sleeps++ }},
		"send", "--session", "demo-1", "--message", "hello")
	if err != nil || len(calls.sends) != 1 || sleeps != 2 {
		t.Fatalf("err = %v sends = %q sleeps = %d", err, calls.sends, sleeps)
	}
}

func TestSessionSwitchRefusalIsNotReportedAsMaybeCompleted(t *testing.T) {
	for _, tc := range []struct {
		status    int
		code      string
		uncertain bool
	}{
		{http.StatusServiceUnavailable, "PROVIDER_SWITCH_unavailable", false},
		{http.StatusConflict, "PROVIDER_SWITCH_recovery_required", false},
		{http.StatusInternalServerError, "INTERNAL_ERROR", true},
	} {
		cfg := setConfigEnv(t)
		srv, _ := switchCommandServer(t, tc.status, tc.code)
		writeRunFileFor(t, cfg, srv)
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "switch", "demo-1", "codex")
		if err == nil || strings.Contains(err.Error(), "may still have completed") != tc.uncertain {
			t.Fatalf("%s: err = %v, want uncertain=%v", tc.code, err, tc.uncertain)
		}
	}
}

func TestSessionSwitchWithNoDaemonSaysNothingChanged(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return false }}, "session", "switch", "demo-1", "codex")
	if err == nil || strings.Contains(err.Error(), "may still have completed") {
		t.Fatalf("err = %v", err)
	}
}

func TestSessionSwitchSaysWhatItStopped(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, 0, "")
	calls.switchBody = `{"status":"ok","stoppedTurn":true,"cancelledQueuedMessages":2,"failedPendingApprovals":true}`
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "switch", "demo-1", "codex", "--force")
	if err != nil || !strings.Contains(out, "switched demo-1 to codex (stopped the running turn, cancelled 2 queued messages, declined the pending approval)") {
		t.Fatalf("out = %q err = %v", out, err)
	}
}

func TestSessionSwitchFromSwitchesEverySessionOnThatAgent(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, 0, "")
	calls.sessions = `{"sessions":[
		{"id":"demo-1","projectId":"demo","kind":"worker","harness":"claude-code"},
		{"id":"demo-2","projectId":"demo","kind":"orchestrator","harness":"claude-code"},
		{"id":"demo-3","projectId":"demo","kind":"worker","harness":"codex"}]}`
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "switch", "--from", "claude", "codex", "--force", "-m", "take over")
	if err != nil {
		t.Fatalf("err = %v out = %q", err, out)
	}
	switched := append([]string(nil), calls.switched...)
	slices.Sort(switched)
	if !slices.Equal(switched, []string{"demo-1", "demo-2"}) || len(calls.sends) != 2 {
		t.Fatalf("switched = %v sends = %q", switched, calls.sends)
	}
	for _, want := range []string{"switched demo-1 to codex", "switched demo-2 to codex", "sent message to demo-1", "switched 2 sessions from claude-code to codex"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q lacks %q", out, want)
		}
	}
}

func TestSessionSwitchFromReportsTheSessionsItCouldNotSwitch(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, http.StatusConflict, "PROVIDER_SWITCH_busy")
	calls.sessions = `{"sessions":[{"id":"demo-1","projectId":"demo","kind":"worker","harness":"claude-code"}]}`
	writeRunFileFor(t, cfg, srv)
	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "switch", "--from", "claude-code", "codex")
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "1 of 1 sessions on claude-code were not switched") || !strings.Contains(errOut, "demo-1: refused") {
		t.Fatalf("err = %v exit = %d stderr = %q", err, ExitCode(err), errOut)
	}
}

func TestSessionSwitchFromCountsAnUnsentMessageAsSwitched(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, 0, "")
	calls.sessions = `{"sessions":[{"id":"demo-1","projectId":"demo","kind":"worker","harness":"claude-code"}]}`
	calls.busySends, calls.busyCode = 1, "SEND_FAILED"
	writeRunFileFor(t, cfg, srv)
	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "switch", "--from", "claude", "codex", "-m", "take over")
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "switched 1 sessions from claude-code to codex, but 1 did not get the message") ||
		!strings.Contains(errOut, "demo-1: switched, but the message was not sent") {
		t.Fatalf("err = %v exit = %d stderr = %q", err, ExitCode(err), errOut)
	}
}

func TestSessionSwitchChangesTheModelOfTheRunningAgent(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, 0, "")
	calls.switchBody = `{"status":"ok","settingsUpdated":true}`
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "switch", "demo-1", "--model", "opus", "--effort", "high")
	if err != nil {
		t.Fatal(err)
	}
	if body := calls.switches[0]; body["targetHarness"] != "claude-code" || body["model"] != "opus" || body["effort"] != "high" {
		t.Fatalf("switch body = %v", body)
	}
	if !strings.Contains(out, "updated demo-1 on claude-code: model opus, effort high") {
		t.Fatalf("output = %q", out)
	}
}

func TestSessionSwitchToAnotherAgentWithItsSettings(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, 0, "")
	calls.switchBody = `{"status":"ok","warnings":["gpt-x is not in codex's model list; the agent may refuse it"]}`
	writeRunFileFor(t, cfg, srv)
	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "switch", "demo-1", "codex", "--model", "gpt-x", "--effort", "low", "--permission", "accept-edits")
	if err != nil {
		t.Fatal(err)
	}
	if body := calls.switches[0]; body["targetHarness"] != "codex" || body["model"] != "gpt-x" || body["effort"] != "low" || body["permissionMode"] != "accept-edits" {
		t.Fatalf("switch body = %v", body)
	}
	if !strings.Contains(out, "switched demo-1 to codex with model gpt-x, effort low, permission mode accept-edits") ||
		!strings.Contains(errOut, "warning: gpt-x is not in codex's model list") {
		t.Fatalf("out = %q stderr = %q", out, errOut)
	}
}

func TestSessionSwitchFromAppliesSettingsToEverySessionOnThatAgent(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, 0, "")
	calls.switchBody = `{"status":"ok","settingsUpdated":true}`
	calls.sessions = `{"sessions":[
		{"id":"demo-1","projectId":"demo","kind":"worker","harness":"claude-code"},
		{"id":"demo-2","projectId":"demo","kind":"worker","harness":"claude-code"}]}`
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "switch", "--from", "claude", "--model", "opus")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range calls.switches {
		if body["targetHarness"] != "claude-code" || body["model"] != "opus" {
			t.Fatalf("switch body = %v", body)
		}
	}
	if len(calls.switches) != 2 || !strings.Contains(out, "applied model opus to 2 sessions on claude-code") {
		t.Fatalf("switches = %d out = %q", len(calls.switches), out)
	}
}
