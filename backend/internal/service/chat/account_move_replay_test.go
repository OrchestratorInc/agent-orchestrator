package chat_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// These scenarios reproduce what a user saw after moving a Claude chat to
// another account once the first one hit its usage limit (2026-10-26). The
// fake agent below speaks the same ACP frames as claude-agent-acp:
//   - a live usage limit settles the prompt with end_turn plus an AIR
//     sessionFailure in the response _meta;
//   - the transcript keeps the agent's own user uuid, not AO's message id;
//   - session/load replays user content block by block (images included) and
//     restores the usage limit as a session_info_update without a turn id.
// A move restarts the provider on the other account and replays that history.

const accountMoveLimitTitle = "You've hit your session limit · resets 5pm (Asia/Calcutta)"

const (
	accountMoveExhausted = "dhruv@untrivial.ai"
	accountMoveFresh     = "device-claude"
)

type accountMoveEntry struct {
	UUID   string `json:"uuid"`
	Text   string `json:"text"`
	Images int    `json:"images"`
	Failed bool   `json:"failed"`
	Answer string `json:"answer,omitempty"`
}

func TestAccountMoveProviderHelper(t *testing.T) {
	if os.Getenv("AO_TEST_ACCOUNT_MOVE") != "1" {
		return
	}
	transcriptPath := os.Getenv("AO_TEST_ACCOUNT_MOVE_TRANSCRIPT")
	exhausted := os.Getenv("AO_TEST_ACCOUNT_MOVE_ACCOUNT") == accountMoveExhausted
	var entries []accountMoveEntry
	if raw, err := os.ReadFile(transcriptPath); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			var entry accountMoveEntry
			if line != "" && json.Unmarshal([]byte(line), &entry) == nil {
				entries = append(entries, entry)
			}
		}
	}
	out := bufio.NewWriter(os.Stdout)
	write := func(frame map[string]any) {
		frame["jsonrpc"] = "2.0"
		raw, _ := json.Marshal(frame)
		_, _ = out.Write(append(raw, '\n'))
		_ = out.Flush()
	}
	update := func(sessionID string, body map[string]any) {
		write(map[string]any{"method": "session/update", "params": map[string]any{"sessionId": sessionID, "update": body}})
	}
	failureMeta := func(id string, turnScoped bool) map[string]any {
		failure := map[string]any{
			"id": id, "revision": 1, "category": "limit", "severity": "error",
			"title": accountMoveLimitTitle, "actions": []any{},
		}
		if turnScoped {
			failure["turnId"] = strings.TrimSuffix(id, ":error")
		}
		return map[string]any{"jetbrains": map[string]any{"air": map[string]any{"version": 1, "sessionFailure": failure}}}
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || len(request.ID) == 0 {
			continue
		}
		id := request.ID
		switch request.Method {
		case "initialize":
			write(map[string]any{"id": id, "result": map[string]any{
				"protocolVersion": 1,
				"agentCapabilities": map[string]any{
					"loadSession": true, "promptCapabilities": map[string]any{"image": true},
				},
				"authMethods": []any{},
			}})
		case "session/new":
			write(map[string]any{"id": id, "result": map[string]any{"sessionId": "move-provider"}})
		case "session/load":
			var params struct {
				SessionID string `json:"sessionId"`
			}
			_ = json.Unmarshal(request.Params, &params)
			for _, entry := range entries {
				messageID := entry.UUID
				update(params.SessionID, map[string]any{
					"sessionUpdate": "user_message_chunk", "messageId": messageID,
					"content": map[string]any{"type": "text", "text": entry.Text},
				})
				for range entry.Images {
					update(params.SessionID, map[string]any{
						"sessionUpdate": "user_message_chunk", "messageId": messageID,
						"content": map[string]any{"type": "image", "data": "iVBORw0KGgo=", "mimeType": "image/png"},
					})
				}
				if entry.Failed {
					update(params.SessionID, map[string]any{
						"sessionUpdate": "session_info_update", "_meta": failureMeta(entry.UUID+":error", false),
					})
					continue
				}
				update(params.SessionID, map[string]any{
					"sessionUpdate": "agent_message_chunk", "messageId": "msg-" + entry.UUID,
					"content": map[string]any{"type": "text", "text": entry.Answer},
				})
			}
			write(map[string]any{"id": id, "result": map[string]any{}})
		case "session/prompt":
			var params struct {
				SessionID string `json:"sessionId"`
				Prompt    []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"prompt"`
			}
			_ = json.Unmarshal(request.Params, &params)
			entry := accountMoveEntry{UUID: fmt.Sprintf("native-user-%d", len(entries)+1)}
			for _, block := range params.Prompt {
				switch block.Type {
				case "text":
					entry.Text += block.Text
				case "image":
					entry.Images++
				}
			}
			if exhausted {
				entry.Failed = true
			} else {
				entry.Answer = "answered on the other account"
				update(params.SessionID, map[string]any{
					"sessionUpdate": "agent_message_chunk", "messageId": "msg-" + entry.UUID,
					"content": map[string]any{"type": "text", "text": entry.Answer},
				})
			}
			entries = append(entries, entry)
			line, _ := json.Marshal(entry)
			file, err := os.OpenFile(transcriptPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err == nil {
				_, _ = file.Write(append(line, '\n'))
				_ = file.Close()
			}
			result := map[string]any{"stopReason": "end_turn"}
			if entry.Failed {
				result["_meta"] = failureMeta(entry.UUID+":error", true)
			}
			write(map[string]any{"id": id, "result": result})
		default:
			write(map[string]any{"id": id, "error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	os.Exit(0)
}

// claudeLikePromptResponseFailure mirrors claudeacp's AIR prompt-response
// failure hook, which is unexported there.
func claudeLikePromptResponseFailure(response acpsdk.PromptResponse) error {
	if response.StopReason != acpsdk.StopReasonEndTurn {
		return nil
	}
	jetbrains, _ := response.Meta["jetbrains"].(map[string]any)
	air, _ := jetbrains["air"].(map[string]any)
	failure, _ := air["sessionFailure"].(map[string]any)
	title, _ := failure["title"].(string)
	if failure == nil || failure["severity"] != "error" || strings.TrimSpace(title) == "" {
		return nil
	}
	return ports.NewChatProviderFailure(strings.TrimSpace(title), "", nil)
}

type accountMoveLab struct {
	t     *testing.T
	ctx   context.Context
	store interface {
		BindSessionAgentAccount(context.Context, domain.SessionID, domain.AgentAccountProvider, string, time.Time) error
	}
	snapshot  func() chatsvc.ConversationRows
	session   domain.SessionID
	start     ports.ChatControllerStart
	account   *atomic.Value
	dataDir   string
	newSvc    func() *chatsvc.Service
	first     *chatsvc.Service
	firstConv string
}

func newAccountMoveLab(t *testing.T) *accountMoveLab {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	dir := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dir)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: string(testProject), Path: dir, RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Add(-time.Hour)
	record, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, CreatedAt: created, UpdatedAt: created,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := record.ID
	// The session starts on the account that is about to run out.
	if err := st.BindSessionAgentAccount(ctx, session, domain.AgentAccountProviderClaude, accountMoveExhausted, created); err != nil {
		t.Fatal(err)
	}
	dataDir, workspace := t.TempDir(), t.TempDir()
	transcript := filepath.Join(dataDir, "native-transcript.jsonl")
	account := &atomic.Value{}
	account.Store(accountMoveExhausted)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := acp.Config{
		Harness:               domain.HarnessClaudeCode,
		PromptResponseFailure: claudeLikePromptResponseFailure,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true, ports.ChatCapabilityResume: true,
			ports.ChatCapabilityInterrupt: true, ports.ChatCapabilityPromptReplay: true,
			ports.ChatCapabilityApprovals: true,
		},
		Probe: func(context.Context) error { return nil },
		Launch: func(context.Context, acp.LaunchConfig) (acp.Launch, error) {
			return acp.Launch{
				Command: os.Args[0], Args: []string{"-test.run=^TestAccountMoveProviderHelper$"},
				Env: map[string]string{
					"AO_TEST_ACCOUNT_MOVE": "1", "AO_TEST_ACCOUNT_MOVE_TRANSCRIPT": transcript,
					"AO_TEST_ACCOUNT_MOVE_ACCOUNT": account.Load().(string),
				},
			}, nil
		},
	}
	var ids atomic.Uint64
	var mu sync.Mutex
	newSvc := func() *chatsvc.Service {
		mu.Lock()
		defer mu.Unlock()
		svc := chatsvc.New(chatsvc.Options{
			Store: st, Reader: fullSnapshotReader(st), Sessions: st,
			Drivers: fakeRegistry{driver: acp.New(cfg, logger)}, Activity: &recordingActivity{}, Log: logger,
			NewID: func() string { return fmt.Sprintf("move-%d", ids.Add(1)) }, DataDir: dataDir,
		})
		t.Cleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopCancel()
			svc.StopAll(stopCtx)
		})
		return svc
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer stopCancel()
		_ = persistenthost.Shutdown(stopCtx, dataDir, string(session))
	})
	lab := &accountMoveLab{
		t: t, ctx: ctx, store: st, session: session, account: account, dataDir: dataDir, newSvc: newSvc,
		start: ports.ChatControllerStart{
			SessionID: session, ProjectID: testProject, Kind: domain.KindWorker,
			Harness: domain.HarnessClaudeCode, DataDir: dataDir, WorkspacePath: workspace,
		},
	}
	lab.snapshot = func() chatsvc.ConversationRows {
		rows, err := st.LoadConversationSnapshot(ctx, lab.firstConv)
		if err != nil {
			t.Fatal(err)
		}
		return chatsvc.ConversationRows{Turns: rows.Turns, Messages: rows.Messages, Activities: rows.Activities}
	}
	return lab
}

// hitLimit sends one prompt on the exhausted account and waits for AO to
// record it as the usage-limit failure the user saw before moving.
func (lab *accountMoveLab) hitLimit(message ports.ChatUserMessage) {
	t := lab.t
	lab.first = lab.newSvc()
	controller, err := lab.first.Start(lab.ctx, lab.start)
	if err != nil {
		t.Fatal(err)
	}
	lab.firstConv = controller.ConversationID()
	lab.start.ProviderConversationID = controller.ProviderConversationID()
	if _, err := controller.Send(lab.ctx, message); err != nil {
		t.Fatal(err)
	}
	awaitACPReplay(lab.ctx, t, "usage-limit failure recorded", func() bool {
		rows := lab.snapshot()
		return len(rows.Turns) == 1 && rows.Turns[0].State == domain.TurnStateFailed
	})
	rows := lab.snapshot()
	if got := rows.Turns[0].ErrorMessage; got != accountMoveLimitTitle || rows.Turns[0].UsageLimit == nil {
		t.Fatalf("before the move the turn must be a usage-limit failure, got error %q usageLimit %+v", got, rows.Turns[0].UsageLimit)
	}
}

// moveAccount does what an account move does to the provider: stop the
// exhausted account's agent, bind the session to the other account, and
// resume the same native conversation there, which replays its transcript.
func (lab *accountMoveLab) moveAccount() {
	t := lab.t
	stopCtx, cancel := context.WithTimeout(lab.ctx, 5*time.Second)
	lab.first.StopAll(stopCtx)
	cancel()
	if err := persistenthost.Shutdown(lab.ctx, lab.dataDir, string(lab.session)); err != nil {
		t.Fatalf("stop the exhausted account's agent: %v", err)
	}
	if err := lab.store.BindSessionAgentAccount(lab.ctx, lab.session, domain.AgentAccountProviderClaude, accountMoveFresh, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	lab.account.Store(accountMoveFresh)
	resume := lab.start
	resume.HistoryMode = ports.ChatHistoryRequired
	if _, err := lab.newSvc().Start(lab.ctx, resume); err != nil {
		t.Fatalf("resume on the other account: %v", err)
	}
}

func activitySummaries(rows chatsvc.ConversationRows) []string {
	out := make([]string, 0, len(rows.Activities))
	for _, activity := range rows.Activities {
		out = append(out, fmt.Sprintf("%s/%s %q", activity.Kind, activity.Status, activity.Summary))
	}
	return out
}

// Image 1: after the move the usage-limit card turned into a red
// "You've hit your session limit … failed" row plus "The agent ran into a problem".
func TestAccountMoveKeepsUsageLimitFailureIntact(t *testing.T) {
	lab := newAccountMoveLab(t)
	lab.hitLimit(ports.ChatUserMessage{Text: "ok merged and?"})
	lab.moveAccount()

	rows := lab.snapshot()
	if len(rows.Turns) != 1 {
		t.Fatalf("the move must not add turns, got %d", len(rows.Turns))
	}
	turn := rows.Turns[0]
	if turn.State != domain.TurnStateFailed || turn.ErrorMessage != accountMoveLimitTitle {
		t.Errorf("the move rewrote the failed turn: state %s, error %q (want %q)", turn.State, turn.ErrorMessage, accountMoveLimitTitle)
	}
	if turn.UsageLimit == nil {
		t.Errorf("the move dropped the usage limit, so the chat shows a generic failure")
	}
	for _, activity := range rows.Activities {
		if activity.Summary == accountMoveLimitTitle {
			t.Errorf("the move added a second copy of the limit as an activity: %s", strings.Join(activitySummaries(rows), ", "))
			break
		}
	}
}

// Image 4: a prompt with images came back as a second "recovered" turn with
// "outcome unknown" and the prompt text ending in [Image][Image].
func TestAccountMoveDoesNotDuplicateImagePrompt(t *testing.T) {
	lab := newAccountMoveLab(t)
	prompt := "Also analyze these two images.\n\nAttached files (read these files in the workspace):\n- a.png\n- b.png"
	lab.hitLimit(ports.ChatUserMessage{Text: prompt, Content: []ports.ChatContent{
		{Type: "image", Data: "iVBORw0KGgo=", MIMEType: "image/png"},
		{Type: "image", Data: "iVBORw0KGgo=", MIMEType: "image/png"},
	}})
	lab.moveAccount()

	rows := lab.snapshot()
	var states []string
	for _, turn := range rows.Turns {
		states = append(states, string(turn.State))
	}
	if len(rows.Turns) != 1 {
		t.Errorf("the move replayed the image prompt as another turn: states %v", states)
	}
	users := 0
	for _, message := range rows.Messages {
		if message.Role != domain.MessageRoleUser {
			continue
		}
		users++
		if strings.Contains(message.Text, "[Image]") {
			t.Errorf("the replayed copy of the prompt is in the chat: %q", message.Text)
		}
	}
	if users != 1 {
		t.Errorf("the prompt must appear once, got %d user messages", users)
	}
	if len(rows.Turns) > 0 && rows.Turns[0].ErrorMessage != accountMoveLimitTitle {
		t.Errorf("the failed image turn lost its usage limit: %q", rows.Turns[0].ErrorMessage)
	}
}

// After moving, the usage-limit notice still said "Available in 1h 22m" and
// only offered "Auto-resume at reset", which AO refuses once the account changed.
func TestAccountMoveMakesUsageLimitResumableNow(t *testing.T) {
	lab := newAccountMoveLab(t)
	lab.hitLimit(ports.ChatUserMessage{Text: "ok merged and?"})
	before, _ := json.Marshal(lab.snapshot().Turns[0].UsageLimit)
	if strings.Contains(string(before), `"accountChanged":true`) {
		t.Fatalf("a limit on the session's own account must not read as moved: %s", before)
	}
	lab.moveAccount()

	rows := lab.snapshot()
	if len(rows.Turns) == 0 || rows.Turns[0].UsageLimit == nil {
		t.Fatalf("the usage limit is gone after the move; turns %+v", rows.Turns)
	}
	after, _ := json.Marshal(rows.Turns[0].UsageLimit)
	if !strings.Contains(string(after), `"accountChanged":true`) {
		t.Errorf("after moving to another account the limit must say so, so the chat can resume now: %s", after)
	}
}
