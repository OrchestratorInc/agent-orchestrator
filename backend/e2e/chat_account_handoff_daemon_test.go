//go:build !windows

package e2e

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Fixture state per account folder for the controlled provider below, keyed by
// the folder the provider runs in (CLAUDE_CONFIG_DIR or CODEX_HOME) so every
// account answers for itself. It lives outside the folders: a Codex account
// folder shares the computer's own files, and would inherit its markers.
const (
	handoffTrigger        = "AO_ACCOUNT_HANDOFF_TRIGGER"
	handoffEmailFile      = "ao-e2e-email"
	handoffUsageFile      = "ao-e2e-usage"     // native limit reading, in percent
	handoffExhaustedFile  = "ao-e2e-exhausted" // the account refuses prompts for quota
	handoffRefuseLoadFile = "ao-e2e-refuse-load"
)

type handoffDelivery struct {
	Harness string `json:"harness"`
	Home    string `json:"home"`
	Text    string `json:"text"`
	Refused bool   `json:"refused"`
}

// TestFakeAccountHandoffProvider is the native ACP/Codex peer for the account
// handoff test. Only this provider boundary is fake: the parent test runs the
// real daemon binary, account service, Session Manager move, SQLite and ticker.
func TestFakeAccountHandoffProvider(t *testing.T) {
	harness := os.Getenv("AO_E2E_HANDOFF_PROVIDER")
	if harness == "" {
		return
	}
	if err := serveAccountHandoffProvider(harness); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func handoffHome(harness string) string {
	if harness == "codex" {
		return os.Getenv("CODEX_HOME")
	}
	return os.Getenv("CLAUDE_CONFIG_DIR")
}

func handoffStatePath(home, name string) string {
	// Codex's account reads may name the folder by its resolved path.
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	return filepath.Join(os.Getenv("AO_E2E_HANDOFF_STATE"), strings.ReplaceAll(home, "/", "_")+"."+name)
}

func handoffHas(home, name string) bool {
	_, err := os.Stat(handoffStatePath(home, name))
	return err == nil
}

func handoffRead(home, name, fallback string) string {
	data, err := os.ReadFile(handoffStatePath(home, name))
	if err != nil {
		return fallback
	}
	return strings.TrimSpace(string(data))
}

// writeNativeTranscript leaves the provider history AO's move preflight looks
// for. Account folders link these directories to the computer's own.
func writeNativeTranscript(harness, home, id string) error {
	path := filepath.Join(home, "projects", "ao-e2e", id+".jsonl")
	if harness == "codex" {
		path = filepath.Join(home, "sessions", "2026", "10", "31", "rollout-2026-10-31T00-00-00-"+id+".jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(`{"type":"session_meta"}`+"\n"), 0o600)
}

func serveAccountHandoffProvider(harness string) error {
	home := handoffHome(harness)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	encoder := json.NewEncoder(os.Stdout)
	write := func(frame any) error { return encoder.Encode(frame) }
	reply := func(id json.RawMessage, result any) error {
		return write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	fail := func(id json.RawMessage, message string) error {
		return write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": message}})
	}
	notify := func(method string, params any) error {
		return write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	}
	native := ""
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				SessionID string                  `json:"sessionId"`
				ThreadID  string                  `json:"threadId"`
				Prompt    []struct{ Text string } `json:"prompt"`
				Input     []struct{ Text string } `json:"input"`
			} `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return err
		}
		if len(request.ID) == 0 {
			continue
		}
		var err error
		switch request.Method {
		case "initialize":
			result := any(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true, "sessionCapabilities": map[string]any{"resume": map[string]any{}}}, "authMethods": []any{}})
			if harness == "codex" {
				result = map[string]any{"userAgent": "ao/handoff-fixture", "codexHome": home}
			}
			err = reply(request.ID, result)
		case "session/new", "thread/start":
			native = uuid.NewString()
			if err = writeNativeTranscript(harness, home, native); err == nil {
				if harness == "codex" {
					err = reply(request.ID, map[string]any{"thread": map[string]any{"id": native, "turns": []any{}}, "model": "fixture-model"})
				} else {
					err = reply(request.ID, map[string]any{"sessionId": native})
				}
			}
		case "session/load", "session/resume", "thread/resume":
			native = request.Params.SessionID + request.Params.ThreadID
			switch {
			case handoffHas(home, handoffRefuseLoadFile):
				err = fail(request.ID, "this account cannot open the conversation")
			case harness == "codex":
				err = reply(request.ID, map[string]any{"thread": map[string]any{"id": native, "turns": []any{}}, "model": "fixture-model"})
			default:
				err = reply(request.ID, map[string]any{"sessionId": native})
			}
		case "thread/read":
			err = reply(request.ID, map[string]any{"thread": map[string]any{"id": native, "turns": []any{}}, "model": "fixture-model"})
		case "model/list":
			err = reply(request.ID, map[string]any{"data": []any{map[string]any{"id": "fixture-model", "displayName": "Fixture model", "isDefault": true}}})
		case "account/read":
			err = reply(request.ID, map[string]any{"account": map[string]any{"type": "chatgpt", "email": handoffRead(home, handoffEmailFile, "device@example.com"), "planType": "pro"}, "requiresOpenaiAuth": true})
		case "account/rateLimits/read":
			used := handoffRead(home, handoffUsageFile, "0")
			err = reply(request.ID, json.RawMessage(`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":`+used+`,"windowDurationMins":300},"secondary":null}}`))
		case "session/prompt", "turn/start":
			var text string
			for _, content := range request.Params.Prompt {
				text += content.Text
			}
			for _, content := range request.Params.Input {
				text += content.Text
			}
			refused := strings.Contains(text, handoffTrigger) && handoffHas(home, handoffExhaustedFile)
			reset := time.Now().Add(10 * time.Minute).Truncate(time.Second).UTC()
			if strings.Contains(text, handoffTrigger) {
				log, openErr := os.OpenFile(os.Getenv("AO_E2E_HANDOFF_CALLS"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
				if openErr != nil {
					return openErr
				}
				err = json.NewEncoder(log).Encode(handoffDelivery{Harness: harness, Home: home, Text: text, Refused: refused})
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
						"title": "You've hit your session limit; resets " + reset.Format(time.RFC3339), "actions": []any{},
					}}}}
				} else if err = notify("session/update", map[string]any{"sessionId": native, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "HANDOFF-RETRY-COMPLETED"}}}); err != nil {
					return err
				}
				err = reply(request.ID, result)
				break
			}
			// Turn ids stay unique across the provider processes of both accounts.
			id := "native-turn-" + uuid.NewString()
			turn := map[string]any{"id": id, "status": "inProgress", "items": []any{}}
			if err = reply(request.ID, map[string]any{"turn": turn}); err != nil {
				return err
			}
			if err = notify("turn/started", map[string]any{"threadId": native, "turn": turn}); err != nil {
				return err
			}
			turn["status"] = "completed"
			if refused {
				if err = notify("account/rateLimits/updated", map[string]any{"rateLimits": map[string]any{"limitId": "codex", "primary": map[string]any{"usedPercent": 100, "resetsAt": reset.Unix()}, "secondary": nil}}); err != nil {
					return err
				}
				turn["status"] = "failed"
				failure := map[string]any{"message": "You've hit your usage limit. Please try again later.", "codexErrorInfo": "usageLimitExceeded"}
				if err = notify("error", map[string]any{"threadId": native, "turnId": id, "willRetry": false, "error": failure}); err != nil {
					return err
				}
			} else if err = notify("item/completed", map[string]any{"threadId": native, "turnId": id, "item": map[string]any{"id": "answer-" + id, "type": "agentMessage", "text": "HANDOFF-RETRY-COMPLETED"}}); err != nil {
				return err
			}
			err = notify("turn/completed", map[string]any{"threadId": native, "turn": turn})
		default:
			err = reply(request.ID, map[string]any{})
		}
		if err != nil {
			return err
		}
	}
	return scanner.Err()
}

type handoffFixture struct {
	t         *testing.T
	daemon    *daemon
	db        *sql.DB
	project   string
	callsPath string
	device    map[string]string // harness -> the computer's own account folder
	spare     map[string]string // harness -> the saved account's folder
	spareID   map[string]string
}

func newHandoffFixture(t *testing.T) *handoffFixture {
	t.Helper()
	binDir := t.TempDir()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	claude := "#!/bin/sh\n" +
		"dir=$(cd \"$CLAUDE_CONFIG_DIR\" 2>/dev/null && pwd -P || printf '%s' \"$CLAUDE_CONFIG_DIR\")\n" +
		"state=\"$AO_E2E_HANDOFF_STATE/$(printf '%s' \"$dir\" | tr '/' '_')\"\n" +
		"email=$(cat \"$state." + handoffEmailFile + "\" 2>/dev/null || echo device@example.com)\n" +
		"used=$(cat \"$state." + handoffUsageFile + "\" 2>/dev/null || echo 0)\n" +
		"case \"$1\" in\n" +
		" auth) printf '{\"loggedIn\":true,\"authMethod\":\"claude.ai\",\"apiProvider\":\"firstParty\",\"email\":\"%s\",\"subscriptionType\":\"max\"}\\n' \"$email\" ;;\n" +
		" --version) printf '2.1.12 (Claude Code)\\n' ;;\n" +
		" --output-format) read line; printf '{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"ao-usage\",\"response\":{\"rate_limits\":{\"five_hour\":{\"utilization\":%s,\"resets_at\":\"2030-01-01T00:00:00Z\"}}}}}\\n' \"$used\" ;;\n" +
		" *) exit 2 ;;\n" +
		"esac\n"
	for name, script := range map[string]string{
		"codex":      "#!/bin/sh\nwhile [ \"$1\" = '-c' ]; do shift 2; done\ncase \"$1\" in\n app-server)\n  if [ \"$2\" = 'generate-json-schema' ]; then\n   while [ \"$1\" != '--out' ]; do shift; done\n   printf '%s\\n' '{\"enum\":[\"account/read\",\"account/rateLimits/read\",\"account/updated\"]}' > \"$2/fixture.json\"\n   exit 0\n  fi\n  export AO_E2E_HANDOFF_PROVIDER=codex; exec \"$AO_E2E_TEST_BINARY\" -test.run='^TestFakeAccountHandoffProvider$' ;;\n login) printf 'Logged in using an API key\\n' ;;\n --version) printf 'codex-cli 0.162.1\\n' ;;\n *) exit 2 ;;\nesac\n",
		"claude":     claude,
		"claude-acp": "#!/bin/sh\nexport AO_E2E_HANDOFF_PROVIDER=claude-code\nexec \"$AO_E2E_TEST_BINARY\" -test.run='^TestFakeAccountHandoffProvider$'\n",
	} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AO_E2E_TEST_BINARY", testBinary)
	t.Setenv("AO_CLAUDE_ACP_COMMAND", filepath.Join(binDir, "claude-acp"))
	t.Setenv("CLAUDE_MODEL_CONFIG", `{"availableModels":["fixture-model"]}`)
	f := &handoffFixture{t: t, device: map[string]string{}, spare: map[string]string{}, spareID: map[string]string{}}
	f.device["claude-code"], f.device["codex"] = t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", f.device["claude-code"])
	t.Setenv("CODEX_HOME", f.device["codex"])
	// The daemon's launch readiness checks the computer's own Codex login file;
	// this value is intentionally not a key.
	if err := os.WriteFile(filepath.Join(f.device["codex"], "auth.json"), []byte(`{"OPENAI_API_KEY":"fixture-not-a-real-api-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "OPENAI_API_KEY", "CODEX_API_KEY"} {
		t.Setenv(name, "")
	}
	t.Setenv("AO_E2E_HANDOFF_STATE", t.TempDir())
	f.callsPath = filepath.Join(t.TempDir(), "provider-deliveries.jsonl")
	t.Setenv("AO_E2E_HANDOFF_CALLS", f.callsPath)
	dataDir := t.TempDir()
	f.daemon = startDaemon(t, dataDir)
	f.db, err = sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db")+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Close() })
	f.project = seedProject(t, f.daemon, "account-handoff-daemon")
	// A second signed-in account per provider. Sign-in runs the CLI's own
	// browser login, so the test records the account the way it would land.
	for harness, provider := range map[string]string{"claude-code": "claude", "codex": "codex"} {
		home := filepath.Join(t.TempDir(), provider+"-spare")
		f.spare[harness], f.spareID[harness] = home, "spare-"+provider
		f.write(home, handoffEmailFile, "spare@example.com")
		if _, err := f.db.Exec(`INSERT INTO agent_accounts (id, provider, home, email, plan, created_at) VALUES (?, ?, ?, 'spare@example.com', 'max', ?)`,
			f.spareID[harness], provider, home, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	f.daemon.mustCall("PATCH", "/settings/account-handoff", http.StatusOK, map[string]any{"enabled": true}, nil)
	return f
}

func (f *handoffFixture) write(home, name, value string) {
	f.t.Helper()
	// The state key is the resolved folder, so the folder must exist first.
	if err := os.MkdirAll(home, 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(handoffStatePath(home, name), []byte(value), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *handoffFixture) remove(home, name string) {
	f.t.Helper()
	if err := os.Remove(handoffStatePath(home, name)); err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
}

func (f *handoffFixture) spawn(harness string) string {
	f.t.Helper()
	session := spawn(f.t, f.daemon, map[string]any{"projectId": f.project, "kind": "worker", "harness": harness, "mode": "chat", "prompt": "Complete the warmup."}).Session.ID
	f.daemon.awaitConversation(session, 30*time.Second, harness+" warmup", func(s snapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == "completed" && s.Controller == "ready"
	})
	return session
}

func (f *handoffFixture) trigger(session string) (string, string) {
	f.t.Helper()
	marker := fmt.Sprintf("%s %d", handoffTrigger, time.Now().UnixNano())
	var result struct {
		TurnID string `json:"turnId"`
	}
	f.daemon.mustCall("POST", "/sessions/"+session+"/conversation/messages", http.StatusAccepted, map[string]any{"text": marker, "clientMessageId": marker}, &result)
	return marker, result.TurnID
}

func (f *handoffFixture) account(session, harness string) string {
	f.t.Helper()
	var accounts struct {
		Claude string `json:"claude"`
		Codex  string `json:"codex"`
	}
	f.daemon.mustCall("GET", "/agents/accounts/sessions/"+session, http.StatusOK, nil, &accounts)
	if harness == "codex" {
		return accounts.Codex
	}
	return accounts.Claude
}

func (f *handoffFixture) handoffPhase(turnID string) string {
	var phase string
	if err := f.db.QueryRow(`SELECT phase FROM chat_account_handoffs WHERE source_turn_id = ?`, turnID).Scan(&phase); err != nil && err != sql.ErrNoRows {
		f.t.Fatal(err)
	}
	return phase
}

func (f *handoffFixture) awaitPhase(turnID, want string, timeout time.Duration) {
	f.t.Helper()
	deadline := time.Now().Add(timeout)
	phase := ""
	for time.Now().Before(deadline) {
		if phase = f.handoffPhase(turnID); phase == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	f.t.Fatalf("handoff phase = %q, want %q\n%s", phase, want, f.daemon.tailLog())
}

func (f *handoffFixture) deliveries(marker string) []handoffDelivery {
	f.t.Helper()
	data, err := os.ReadFile(f.callsPath)
	if err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
	var out []handoffDelivery
	for _, line := range strings.Split(string(data), "\n") {
		var delivery handoffDelivery
		if json.Unmarshal([]byte(line), &delivery) == nil && strings.Contains(delivery.Text, marker) {
			out = append(out, delivery)
		}
	}
	return out
}

type handoffSnapshot struct {
	Controller string `json:"controller"`
	Turns      []struct {
		ID            string `json:"id"`
		State         string `json:"state"`
		RetryOfTurnID string `json:"retryOfTurnId"`
		UsageLimit    *struct {
			AutoResume bool `json:"autoResume"`
		} `json:"usageLimit"`
	} `json:"turns"`
	Activities []struct {
		TurnID string          `json:"turnId"`
		Detail json.RawMessage `json:"detail"`
	} `json:"activities"`
}

func (f *handoffFixture) snapshot(session string) handoffSnapshot {
	f.t.Helper()
	var s handoffSnapshot
	f.daemon.mustCall("GET", "/sessions/"+session+"/conversation", http.StatusOK, nil, &s)
	return s
}

func TestAccountHandoffDaemonBothProviders(t *testing.T) {
	f := newHandoffFixture(t)
	harnesses := []string{"claude-code", "codex"}
	if only := os.Getenv("AO_E2E_HANDOFF_HARNESS"); only != "" {
		harnesses = []string{only}
	}
	for _, harness := range harnesses {
		device, spare, spareID := f.device[harness], f.spare[harness], f.spareID[harness]
		deviceID := map[string]string{"claude-code": "device-claude", "codex": "device-codex"}[harness]

		t.Run(harness+"/moves-and-retries-once", func(t *testing.T) {
			f.write(device, handoffExhaustedFile, "1")
			f.write(spare, handoffUsageFile, "10")
			f.remove(spare, handoffRefuseLoadFile)
			session := f.spawn(harness)
			marker, turnID := f.trigger(session)
			f.awaitPhase(turnID, "retried", 90*time.Second)
			var s handoffSnapshot
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				if s = f.snapshot(session); len(s.Turns) == 3 && s.Turns[2].State == "completed" {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if len(s.Turns) != 3 || s.Turns[1].ID != turnID || s.Turns[1].State != "failed" || s.Turns[2].RetryOfTurnID != turnID || s.Turns[2].State != "completed" {
				t.Fatalf("history after handoff = %+v\ndeliveries = %+v (device %s, spare %s)\n%s", s.Turns, f.deliveries(marker), device, spare, f.daemon.tailLog())
			}
			notes := 0
			for _, activity := range s.Activities {
				if activity.TurnID == turnID && strings.Contains(string(activity.Detail), "account.limit_handoff") && strings.Contains(string(activity.Detail), spareID) {
					notes++
				}
			}
			if notes != 1 {
				t.Fatalf("handoff notes on the refused turn = %d: %+v", notes, s.Activities)
			}
			if got := f.account(session, harness); got != spareID {
				t.Fatalf("session account = %q, want %q", got, spareID)
			}
			// Further ticks must not dispatch the prompt again.
			time.Sleep(2500 * time.Millisecond)
			calls := f.deliveries(marker)
			if len(calls) != 2 || !calls[0].Refused || calls[0].Home != device || calls[1].Refused || calls[1].Home != spare {
				t.Fatalf("native deliveries = %+v", calls)
			}
			t.Logf("%s: refused on %s -> moved -> exactly one retry on %s", harness, deviceID, spareID)
		})

		t.Run(harness+"/no-candidate-keeps-account", func(t *testing.T) {
			f.write(device, handoffExhaustedFile, "1")
			f.write(spare, handoffUsageFile, "100")
			session := f.spawn(harness)
			marker, turnID := f.trigger(session)
			f.awaitPhase(turnID, "stayed", 60*time.Second)
			// It stayed because the spare's native reading showed no room, not
			// because the spare could not be read or signed in.
			if log, _ := os.ReadFile(f.daemon.logPath); !strings.Contains(string(log), spareID+":no verified room") {
				t.Fatalf("the spare was not rejected for its limits\n%s", f.daemon.tailLog())
			}
			s := f.snapshot(session)
			if len(s.Turns) != 2 || s.Turns[1].UsageLimit == nil || !s.Turns[1].UsageLimit.AutoResume {
				t.Fatalf("no-candidate fallback did not arm auto-resume: %+v", s.Turns)
			}
			if got := f.account(session, harness); got != deviceID {
				t.Fatalf("session account = %q, want %q", got, deviceID)
			}
			if calls := f.deliveries(marker); len(calls) != 1 {
				t.Fatalf("native deliveries = %+v", calls)
			}
			f.daemon.mustCall("POST", "/sessions/"+session+"/kill", http.StatusOK, nil, nil)
		})

		t.Run(harness+"/rollback-keeps-account", func(t *testing.T) {
			f.write(device, handoffExhaustedFile, "1")
			f.write(spare, handoffUsageFile, "10")
			f.write(spare, handoffRefuseLoadFile, "1")
			defer f.remove(spare, handoffRefuseLoadFile)
			session := f.spawn(harness)
			marker, turnID := f.trigger(session)
			f.awaitPhase(turnID, "rolled_back", 120*time.Second)
			if got := f.account(session, harness); got != deviceID {
				t.Fatalf("session account after rollback = %q, want %q", got, deviceID)
			}
			s := f.daemon.awaitConversation(session, 60*time.Second, "controller back on the old account", func(s snapshot) bool { return s.Controller == "ready" })
			if len(s.Turns) != 2 {
				t.Fatalf("turns after rollback = %d", len(s.Turns))
			}
			if hs := f.snapshot(session); hs.Turns[1].UsageLimit == nil || !hs.Turns[1].UsageLimit.AutoResume {
				t.Fatalf("rollback did not fall back to auto-resume: %+v", hs.Turns)
			}
			if calls := f.deliveries(marker); len(calls) != 1 || !calls[0].Refused {
				t.Fatalf("native deliveries = %+v", calls)
			}
			f.daemon.mustCall("POST", "/sessions/"+session+"/kill", http.StatusOK, nil, nil)
		})
	}
}
