//go:build !windows

package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const usageLimitTrigger = "AO_USAGE_LIMIT_TRIGGER"

type usageLimitDelivery struct {
	Harness  string    `json:"harness"`
	Text     string    `json:"text"`
	At       time.Time `json:"at"`
	ResetsAt time.Time `json:"resetsAt,omitempty"`
}

// TestFakeUsageLimitProvider is a native ACP/Codex peer launched by disposable
// executable shims. Only this provider boundary is fake: the parent tests run
// the real daemon binary, detached hosts, SQLite, HTTP, CDC, and startup ticker.
func TestFakeUsageLimitProvider(t *testing.T) {
	harness := os.Getenv("AO_E2E_USAGE_PROVIDER")
	if harness == "" {
		return
	}
	if err := serveUsageLimitProvider(harness); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func serveUsageLimitProvider(harness string) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	encoder := json.NewEncoder(os.Stdout)
	write := func(frame any) error { return encoder.Encode(frame) }
	reply := func(id json.RawMessage, result any) error {
		return write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	notify := func(method string, params any) error {
		return write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	}
	const nativeSession = "usage-limit-native-session"
	const nativeThread = "usage-limit-native-thread"
	attempts := make(map[string]int)
	resets := make(map[string]time.Time)
	turns := 0
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Prompt []struct{ Text string } `json:"prompt"`
				Input  []struct{ Text string } `json:"input"`
			} `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return err
		}
		if len(request.ID) == 0 {
			continue
		}
		switch request.Method {
		case "initialize":
			result := any(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"sessionCapabilities": map[string]any{"resume": map[string]any{}}}, "authMethods": []any{}})
			if harness == "codex" {
				result = map[string]any{"userAgent": "ao/quota-fixture", "codexHome": os.Getenv("CODEX_HOME")}
			}
			if err := reply(request.ID, result); err != nil {
				return err
			}
		case "session/new", "session/load", "session/resume":
			if err := reply(request.ID, map[string]any{"sessionId": nativeSession}); err != nil {
				return err
			}
		case "model/list":
			if err := reply(request.ID, map[string]any{"data": []any{map[string]any{"id": "fixture-model", "displayName": "Fixture model", "isDefault": true}}}); err != nil {
				return err
			}
		case "account/read":
			if err := reply(request.ID, map[string]any{"account": map[string]any{"type": "apiKey"}, "requiresOpenaiAuth": true}); err != nil {
				return err
			}
		case "account/rateLimits/read":
			if err := reply(request.ID, map[string]any{"rateLimits": map[string]any{"limitId": "codex", "primary": map[string]any{"usedPercent": 0}, "secondary": nil}}); err != nil {
				return err
			}
		case "thread/start", "thread/resume", "thread/read":
			if err := reply(request.ID, map[string]any{"thread": map[string]any{"id": nativeThread, "turns": []any{}}, "model": "fixture-model"}); err != nil {
				return err
			}
		case "session/prompt", "turn/start":
			var text string
			for _, content := range request.Params.Prompt {
				text += content.Text
			}
			for _, content := range request.Params.Input {
				text += content.Text
			}
			triggered := strings.Contains(text, usageLimitTrigger)
			refused := false
			if triggered {
				attempts[text]++
				refused = attempts[text] == 1
				if refused {
					delay := 2000
					fields := strings.Fields(text)
					for i, field := range fields {
						if field == usageLimitTrigger && i+1 < len(fields) {
							delay, _ = strconv.Atoi(fields[i+1])
							break
						}
					}
					// Native Codex reset metadata has Unix-second precision. Round up
					// so a two-second request never resumes earlier than two seconds.
					resets[text] = time.Now().Add(time.Duration(delay)*time.Millisecond + time.Second - time.Nanosecond).Truncate(time.Second).UTC()
				}
				log, err := os.OpenFile(os.Getenv("AO_E2E_USAGE_CALLS"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
				if err != nil {
					return err
				}
				err = json.NewEncoder(log).Encode(usageLimitDelivery{Harness: harness, Text: text, At: time.Now().UTC(), ResetsAt: resets[text]})
				_ = log.Close()
				if err != nil {
					return err
				}
			}
			if harness == "claude-code" {
				result := map[string]any{"stopReason": "end_turn"}
				if refused {
					result["_meta"] = map[string]any{"jetbrains": map[string]any{"air": map[string]any{"version": 1, "sessionFailure": map[string]any{
						"id": "quota-refusal", "revision": 1, "category": "limit", "severity": "error",
						"title": "You've hit your session limit; resets " + resets[text].Format(time.RFC3339), "actions": []any{},
					}}}}
				} else if err := notify("session/update", map[string]any{"sessionId": nativeSession, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "AUTOMATIC-RESUME-COMPLETED"}}}); err != nil {
					return err
				}
				if err := reply(request.ID, result); err != nil {
					return err
				}
				continue
			}
			turns++
			id := fmt.Sprintf("native-turn-%d", turns)
			turn := map[string]any{"id": id, "status": "inProgress", "items": []any{}}
			if err := reply(request.ID, map[string]any{"turn": turn}); err != nil {
				return err
			}
			if err := notify("turn/started", map[string]any{"threadId": nativeThread, "turn": turn}); err != nil {
				return err
			}
			turn["status"] = "completed"
			if refused {
				if err := notify("account/rateLimits/updated", map[string]any{"rateLimits": map[string]any{"limitId": "codex", "primary": map[string]any{"usedPercent": 100, "resetsAt": resets[text].Unix()}, "secondary": nil}}); err != nil {
					return err
				}
				turn["status"] = "failed"
				failure := map[string]any{"message": "You've hit your usage limit. Please try again later.", "codexErrorInfo": "usageLimitExceeded"}
				// Exercise the native terminal-notification fallback: the final
				// failed completion deliberately has no attached TurnError.
				if err := notify("error", map[string]any{"threadId": nativeThread, "turnId": id, "willRetry": false, "error": failure}); err != nil {
					return err
				}
			} else if err := notify("item/completed", map[string]any{"threadId": nativeThread, "turnId": id, "item": map[string]any{"id": "answer-" + id, "type": "agentMessage", "text": "AUTOMATIC-RESUME-COMPLETED"}}); err != nil {
				return err
			}
			if err := notify("turn/completed", map[string]any{"threadId": nativeThread, "turn": turn}); err != nil {
				return err
			}
		default:
			if err := reply(request.ID, map[string]any{}); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

type usageLimitDaemonFixture struct {
	daemon    *daemon
	sessions  map[string]string
	callsPath string
	mu        sync.Mutex
	markers   map[string]string
}

func newUsageLimitDaemonFixture(t *testing.T) *usageLimitDaemonFixture {
	t.Helper()
	binDir := t.TempDir()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{
		"codex":      "#!/bin/sh\nwhile [ \"$1\" = '-c' ]; do shift 2; done\ncase \"$1\" in\n app-server)\n  if [ \"$2\" = 'generate-json-schema' ]; then\n   while [ \"$1\" != '--out' ]; do shift; done\n   printf '%s\\n' '{\"enum\":[\"account/read\",\"account/rateLimits/read\",\"account/updated\"]}' > \"$2/fixture.json\"\n   exit 0\n  fi\n  export AO_E2E_USAGE_PROVIDER=codex; exec \"$AO_E2E_TEST_BINARY\" -test.run='^TestFakeUsageLimitProvider$' ;;\n login) printf 'Logged in using an API key\\n' ;;\n --version) printf 'codex-cli 0.162.1\\n' ;;\n *) exit 2 ;;\nesac\n",
		"claude":     "#!/bin/sh\ncase \"$1\" in\n auth) printf '%s\\n' '{\"loggedIn\":true,\"authMethod\":\"claude.ai\",\"apiProvider\":\"firstParty\"}' ;;\n --version) printf '2.1.12 (Claude Code)\\n' ;;\n *) exit 2 ;;\nesac\n",
		"claude-acp": "#!/bin/sh\nexport AO_E2E_USAGE_PROVIDER=claude-code\nexec \"$AO_E2E_TEST_BINARY\" -test.run='^TestFakeUsageLimitProvider$'\n",
	} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AO_E2E_TEST_BINARY", testBinary)
	t.Setenv("AO_CLAUDE_ACP_COMMAND", filepath.Join(binDir, "claude-acp"))
	t.Setenv("CLAUDE_MODEL_CONFIG", `{"availableModels":["fixture-model"]}`)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	codexHome := t.TempDir()
	// The real daemon enrolls and validates its isolated device account through
	// the native account/read peer above. This value is intentionally not a key.
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"OPENAI_API_KEY":"fixture-not-a-real-api-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
		t.Setenv(name, "")
	}
	callsPath := filepath.Join(t.TempDir(), "provider-deliveries.jsonl")
	t.Setenv("AO_E2E_USAGE_CALLS", callsPath)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "usage-limit-daemon")
	fixture := &usageLimitDaemonFixture{daemon: d, callsPath: callsPath, sessions: make(map[string]string), markers: make(map[string]string)}
	for _, harness := range []string{"claude-code", "codex"} {
		session := spawn(t, d, map[string]any{"projectId": project, "kind": "worker", "harness": harness, "mode": "chat", "prompt": "Complete the warmup."}).Session.ID
		d.awaitConversation(session, 30*time.Second, harness+" fixture warmup", func(s snapshot) bool {
			return len(s.Turns) == 1 && s.Turns[0].State == "completed" && s.Controller == "ready"
		})
		fixture.sessions[harness] = session
	}
	return fixture
}

type usageLimitTriggerResult struct {
	SessionID string `json:"sessionId"`
	TurnID    string `json:"turnId"`
}

func (f *usageLimitDaemonFixture) trigger(session string, delay int) (usageLimitTriggerResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	marker := fmt.Sprintf("%s %d %d", usageLimitTrigger, delay, time.Now().UnixNano())
	result := usageLimitTriggerResult{SessionID: session}
	var raw json.RawMessage
	status, err := f.daemon.call("POST", "/sessions/"+session+"/conversation/messages", map[string]any{"text": marker, "clientMessageId": marker}, &raw)
	if err != nil {
		return result, fmt.Errorf("trigger request: %w", err)
	}
	if status != http.StatusAccepted {
		return result, fmt.Errorf("trigger status=%d response=%s", status, raw)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	f.markers[session] = marker
	return result, nil
}

type usageLimitFixtureStats struct {
	Deliveries       int        `json:"deliveries"`
	RetryDeliveries  int        `json:"retryDeliveries"`
	ResetsAt         *time.Time `json:"resetsAt,omitempty"`
	RetryDeliveredAt *time.Time `json:"retryDeliveredAt,omitempty"`
}

func (f *usageLimitDaemonFixture) stats(session string) (usageLimitFixtureStats, error) {
	f.mu.Lock()
	marker := f.markers[session]
	f.mu.Unlock()
	var stats usageLimitFixtureStats
	if marker == "" {
		return stats, nil
	}
	data, err := os.ReadFile(f.callsPath)
	if os.IsNotExist(err) {
		return stats, nil
	}
	if err != nil {
		return stats, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		var delivery usageLimitDelivery
		if json.Unmarshal([]byte(line), &delivery) != nil || !strings.Contains(delivery.Text, marker) {
			continue
		}
		stats.Deliveries++
		if stats.ResetsAt == nil {
			reset := delivery.ResetsAt
			stats.ResetsAt = &reset
		} else {
			at := delivery.At
			stats.RetryDeliveredAt = &at
			stats.RetryDeliveries++
		}
	}
	return stats, nil
}

type usageLimitDaemonSnapshot struct {
	Controller string `json:"controller"`
	Turns      []struct {
		ID            string `json:"id"`
		State         string `json:"state"`
		RetryOfTurnID string `json:"retryOfTurnId"`
		UsageLimit    *struct {
			ResetsAt   time.Time `json:"resetsAt"`
			AutoResume bool      `json:"autoResume"`
		} `json:"usageLimit"`
	} `json:"turns"`
}

func TestUsageLimitDaemonAutomaticallyResumesBothProviders(t *testing.T) {
	f := newUsageLimitDaemonFixture(t)
	for _, harness := range []string{"claude-code", "codex"} {
		t.Run(harness, func(t *testing.T) {
			session := f.sessions[harness]
			triggered, err := f.trigger(session, 2000)
			if err != nil {
				t.Fatal(err)
			}
			var before usageLimitDaemonSnapshot
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				f.daemon.mustCall("GET", "/sessions/"+session+"/conversation", http.StatusOK, nil, &before)
				if len(before.Turns) == 2 && before.Turns[1].State == "failed" && before.Turns[1].UsageLimit != nil && before.Controller == "ready" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if len(before.Turns) != 2 || before.Turns[1].ID != triggered.TurnID || before.Turns[1].UsageLimit == nil || before.Turns[1].UsageLimit.ResetsAt.IsZero() {
				t.Fatalf("native quota did not become durable failure: %+v\n%s", before, f.daemon.tailLog())
			}
			reset := before.Turns[1].UsageLimit.ResetsAt
			if !reset.After(time.Now()) {
				t.Fatal("quota fixture deadline elapsed before HTTP opt-in")
			}
			f.daemon.mustCall("PATCH", "/sessions/"+session+"/conversation/turns/"+triggered.TurnID+"/auto-resume", http.StatusNoContent, map[string]any{"enabled": true}, nil)
			for time.Now().Before(reset) {
				stats, err := f.stats(session)
				if err != nil || stats.Deliveries != 1 || stats.RetryDeliveries != 0 {
					t.Fatalf("dispatch before native reset: %+v, %v", stats, err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			deadline = time.Now().Add(10 * time.Second)
			var after usageLimitDaemonSnapshot
			for time.Now().Before(deadline) {
				f.daemon.mustCall("GET", "/sessions/"+session+"/conversation", http.StatusOK, nil, &after)
				if len(after.Turns) == 3 && after.Turns[2].State == "completed" {
					break
				}
				time.Sleep(30 * time.Millisecond)
			}
			if len(after.Turns) != 3 || after.Turns[2].RetryOfTurnID != triggered.TurnID || after.Turns[2].State != "completed" || after.Turns[1].UsageLimit == nil || after.Turns[1].UsageLimit.AutoResume {
				t.Fatalf("daemon ticker did not consume consent for one retry: %+v\n%s", after, f.daemon.tailLog())
			}
			// Observe another scheduler tick; consent must not dispatch again.
			time.Sleep(1200 * time.Millisecond)
			stats, err := f.stats(session)
			if err != nil || stats.Deliveries != 2 || stats.RetryDeliveries != 1 || stats.RetryDeliveredAt == nil || stats.RetryDeliveredAt.Before(reset) {
				t.Fatalf("native retry count/deadline = %+v, %v", stats, err)
			}
			t.Logf("%s: HTTP consent -> production daemon ticker -> exactly one native retry at %s (reset %s)", harness, stats.RetryDeliveredAt.Format(time.RFC3339Nano), reset.Format(time.RFC3339))
		})
	}
}

// TestUsageLimitDaemonBrowserFixture keeps the same real daemon available to
// Playwright or a local preview for at most 30 minutes (run with -timeout=35m).
// The coordinator only triggers provider refusals and reports delivery evidence;
// it never schedules or retries a turn.
func TestUsageLimitDaemonBrowserFixture(t *testing.T) {
	if os.Getenv("AO_E2E_USAGE_LIMIT_BROWSER") != "1" {
		t.Skip("set AO_E2E_USAGE_LIMIT_BROWSER=1 for the connected browser fixture")
	}
	f := newUsageLimitDaemonFixture(t)
	done := make(chan struct{})
	var stopOnce sync.Once
	var baseURL string
	metadata := func() map[string]any {
		return map[string]any{"baseUrl": strings.TrimSuffix(f.daemon.baseURL, "/api/v1"), "fixtureBaseUrl": baseURL, "sessions": f.sessions}
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/test/shutdown" {
			w.WriteHeader(http.StatusNoContent)
			stopOnce.Do(func() { close(done) })
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/test/usage-limit" {
			_ = json.NewEncoder(w).Encode(metadata())
			return
		}
		session := strings.TrimPrefix(r.URL.Path, "/test/usage-limit/")
		valid := false
		for _, id := range f.sessions {
			valid = valid || id == session
		}
		if !valid {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			stats, err := f.stats(session)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(stats)
			return
		}
		if r.Method == http.MethodPost {
			var request struct {
				DelayMS int `json:"delayMs"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.DelayMS < 2000 || request.DelayMS > 60000 {
				http.Error(w, "delayMs must be between 2000 and 60000", http.StatusBadRequest)
				return
			}
			result, err := f.trigger(session, request.DelayMS)
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			_ = json.NewEncoder(w).Encode(result)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	baseURL = "http://" + server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	encoded, _ := json.Marshal(metadata())
	fmt.Printf("AO_USAGE_LIMIT_FIXTURE=%s\n", encoded)
	select {
	case <-done:
	case <-time.After(30 * time.Minute):
		t.Fatal("connected fixture timed out before graceful shutdown")
	}
}
