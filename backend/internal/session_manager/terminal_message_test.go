package sessionmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/freebuff"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSend_FreebuffPreservesMultilineMessageAndSemanticMetadata(t *testing.T) {
	for _, prompt := range []string{
		"First line.\nSecond line.",
		strings.Repeat("Preserve this line: 中文 🚀.\n", 100) + "COMPLETE_FOLLOWUP",
	} {
		st := newFakeStore()
		st.sessions["s1"] = domain.SessionRecord{ID: "s1", Harness: domain.HarnessFreebuff, Mode: domain.SessionModeTUI}
		messenger := &fakeMessenger{}
		m := newSendTestManager(t, freebuff.New(), messenger, st)
		if err := m.SendWithOptions(context.Background(), "s1", prompt, nil, ports.MessageDeliveryOptions{AuthoredByUser: true}); err != nil {
			t.Fatal(err)
		}
		if len(messenger.msgs) != 1 || messenger.msgs[0] != "\x1b[200~"+prompt+"\x1b[201~" {
			t.Fatal("follow-up did not reach the terminal as one complete native paste")
		}
		if got := st.sessions["s1"].Metadata.LatestUserPrompt; got != prompt {
			t.Fatal("terminal framing changed the recorded semantic message")
		}
	}
}

func TestSend_FreebuffEncodesCoordinationEnvelopeOnce(t *testing.T) {
	st := newFakeStore()
	st.sessions["s1"] = domain.SessionRecord{ID: "s1", Harness: domain.HarnessFreebuff,
		Metadata: domain.SessionMetadata{LatestUserPrompt: "previous human task"}}
	messenger := &fakeMessenger{}
	m := newSendTestManager(t, freebuff.New(), messenger, st)
	const deliveryID = "lifecycle:ci-failure"
	const message = "Build failed.\nPreserve the full diagnostic.\nPlease fix it."
	if err := m.send(context.Background(), "s1", message, deliveryID, ports.MessageDeliveryOptions{}); err != nil {
		t.Fatal(err)
	}
	semantic := domain.WrapSessionDelivery(deliveryID, message)
	if len(messenger.msgs) != 1 || messenger.msgs[0] != "\x1b[200~"+semantic+"\x1b[201~" {
		t.Fatal("coordination envelope was lost or encoded more than once")
	}
	if st.sessions["s1"].Metadata.LatestUserPrompt != "previous human task" {
		t.Fatal("coordination delivery replaced the latest human task")
	}
}

func TestSend_FreebuffRejectsEscapeSequencesBeforeWriteOrMetadata(t *testing.T) {
	st := newFakeStore()
	st.sessions["s1"] = domain.SessionRecord{ID: "s1", Harness: domain.HarnessFreebuff,
		Metadata: domain.SessionMetadata{LatestUserPrompt: "previous human task"}}
	messenger := &fakeMessenger{}
	m := newSendTestManager(t, freebuff.New(), messenger, st)
	const prompt = "PRIVATE_FOLLOWUP\x1b[201~\nDo not write."
	err := m.SendWithOptions(context.Background(), "s1", prompt, nil, ports.MessageDeliveryOptions{AuthoredByUser: true})
	if err == nil || len(messenger.msgs) != 0 {
		t.Fatal("invalid terminal payload was accepted")
	}
	if strings.Contains(err.Error(), "PRIVATE_FOLLOWUP") || strings.ContainsRune(err.Error(), '\x1b') {
		t.Fatal("encoding error exposed message contents")
	}
	if st.sessions["s1"].Metadata.LatestUserPrompt != "previous human task" {
		t.Fatal("failed delivery changed the latest human task")
	}
}

func TestSend_FreebuffSafetyRefusalsPrecedeEncoding(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state domain.ActivityState
		want  error
	}{
		{"blocked", domain.ActivityBlocked, ErrAwaitingDecision},
		{"exited", domain.ActivityExited, ErrAgentExited},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["s1"] = domain.SessionRecord{ID: "s1", Harness: domain.HarnessFreebuff,
				Activity: domain.Activity{State: tc.state}}
			messenger := &fakeMessenger{}
			m := newSendTestManager(t, freebuff.New(), messenger, st)
			err := m.SendWithOptions(context.Background(), "s1", "invalid\x1b[201~", nil, ports.MessageDeliveryOptions{AuthoredByUser: true})
			if !errors.Is(err, tc.want) || len(messenger.msgs) != 0 {
				t.Fatalf("delivery = %v, writes = %d; want safety refusal %v", err, len(messenger.msgs), tc.want)
			}
		})
	}
}

func TestSend_TerminalEncodingPreservesEmptyNudgesAndOtherAgents(t *testing.T) {
	for _, tc := range []struct {
		name    string
		harness domain.AgentHarness
		agent   ports.Agent
		message string
	}{
		{"empty Freebuff nudge", domain.HarnessFreebuff, freebuff.New(), ""},
		{"other agent", domain.HarnessClaudeCode, fakeAgent{}, "First line.\nSecond line."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["s1"] = domain.SessionRecord{ID: "s1", Harness: tc.harness}
			messenger := &fakeMessenger{}
			m := newSendTestManager(t, tc.agent, messenger, st)
			if err := m.SendWithOptions(context.Background(), "s1", tc.message, nil, ports.MessageDeliveryOptions{AuthoredByUser: true}); err != nil {
				t.Fatal(err)
			}
			if len(messenger.msgs) != 1 || messenger.msgs[0] != tc.message {
				t.Fatal("encoding changed an Enter-only nudge or an adapter without this protocol")
			}
		})
	}
}

func TestConfirmAfterStartSubmit_FreebuffPendingAttachmentRequiresNativeUserAcceptance(t *testing.T) {
	shortenAfterStartSubmitTimings(t)
	afterStartSubmitTimeout = 250 * time.Millisecond
	data, err := os.ReadFile(filepath.Join("..", "adapters", "agent", "freebuff", "testdata", "pending-text-attachment.ansi.txt"))
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	const prefix = "Runtime paste transport validation only."
	prompt := prefix + strings.Repeat("x", 3131-len(prefix))
	for _, tc := range []struct {
		name       string
		viewport   string
		savedRole  string
		wantEnters int
		wantError  string
	}{
		{"matching attachment saved as native user turn", output, "user", 1, ""},
		{"assistant echo does not acknowledge submission", output, "assistant", afterStartSubmitMaxRetries, "first turn stayed unsent"},
		{"different attachment receives no Enter", strings.Replace(output, "Runtime paste", "Another paste", 1), "", 0, "acceptance could not be confirmed"},
		{"native dialog receives no Enter", "Model request failed. Enter retry · Esc back.", "", 0, "acceptance could not be confirmed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			plugin := freebuff.New()
			cfg := ports.LaunchConfig{
				SessionID:     "s1",
				WorkspacePath: t.TempDir(),
				Env:           map[string]string{"FREEBUFF_CONFIG_DIR": t.TempDir()},
			}
			release, err := plugin.BeginStartup(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			st := newFakeStore()
			st.sessions["s1"] = domain.SessionRecord{
				ID: "s1", Harness: domain.HarnessFreebuff, Mode: domain.SessionModeTUI,
				Activity: domain.Activity{State: domain.ActivityIdle},
			}
			messenger := &fakeMessenger{}
			messenger.onSend = func(id domain.SessionID, message string) {
				if id != "s1" || message != "" {
					t.Fatalf("submission retry wrote session=%q message=%q, want one Enter-only input", id, message)
				}
				if tc.savedRole == "" || len(messenger.msgs) != 1 {
					return
				}
				// Freebuff acknowledges a long paste by saving the entire native
				// attachment as a user message in this workspace's own config.
				state := map[string]any{"sessionState": map[string]any{
					"fileContext": map[string]string{"projectRoot": cfg.WorkspacePath},
					"mainAgentState": map[string]any{"messageHistory": []map[string]any{{
						"role": tc.savedRole, "sentAt": time.Now().UnixMilli(),
						"content": []map[string]string{{"type": "text", "text": "[Pasted Text]\n" + prompt}},
					}}},
				}}
				saved, err := json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				dir := filepath.Join(cfg.Env["FREEBUFF_CONFIG_DIR"], "projects", filepath.Base(cfg.WorkspacePath), "chats", "2026-10-21T00-00-00.000Z")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "run-state.json"), saved, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			m := newSendTestManager(t, plugin, messenger, st)
			m.runtime = &selectingRuntime{viewport: tc.viewport}
			err = m.confirmAfterStartSubmit(ctx, plugin, cfg, ports.RuntimeHandle{}, "s1", prompt)
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("native first-turn confirmation = %v, want %q", err, tc.wantError)
			}
			if len(messenger.msgs) != tc.wantEnters {
				t.Fatalf("submission sent %d Enter retries, want %d", len(messenger.msgs), tc.wantEnters)
			}
		})
	}
}
