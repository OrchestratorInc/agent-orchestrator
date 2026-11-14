package controllers_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"

	"net/http/httptest"
)

func TestSnapshotExposesTypedFailureBesideTechnicalError(t *testing.T) {
	body := conversationSnapshotBody(t, chatsvc.Snapshot{
		SessionID: domain.SessionID("p1-1"), Mode: domain.SessionModeChat,
		Turns: []domain.ConversationTurn{{
			ID: "turn-1", State: domain.TurnStateFailed, RequestedAt: time.Now().UTC(),
			ErrorMessage: "ACP internal detail", FailureCode: domain.ConversationFailureProviderKeyMissing,
		}},
	})
	turns, ok := body["turns"].([]any)
	if !ok || len(turns) != 1 {
		t.Fatalf("turns = %#v", body["turns"])
	}
	turn := turns[0].(map[string]any)
	if turn["failureCode"] != "provider_key_missing" || turn["errorMessage"] != "ACP internal detail" {
		t.Fatalf("turn = %#v", turn)
	}
}

func TestSnapshotExposesTurnPlanAsStructuredSteps(t *testing.T) {
	body := conversationSnapshotBody(t, chatsvc.Snapshot{
		SessionID: domain.SessionID("p1-1"),
		Mode:      domain.SessionModeChat,
		Turns: []domain.ConversationTurn{{
			ID:          "turn-1",
			State:       domain.TurnStateRunning,
			RequestedAt: time.Now().UTC(),
			Plan: &domain.ConversationPlan{
				Explanation: "three steps",
				Steps: []domain.ConversationPlanStep{
					{Text: "append a line", Status: domain.PlanStepCompleted},
					{Text: "create notes.md", Status: domain.PlanStepInProgress},
					{Text: "run ls", Status: domain.PlanStepPending},
				},
			},
		}},
	})

	turns, ok := body["turns"].([]any)
	if !ok || len(turns) != 1 {
		t.Fatalf("turns = %#v", body["turns"])
	}
	plan, ok := turns[0].(map[string]any)["plan"].(map[string]any)
	if !ok {
		t.Fatalf("turn has no plan: %#v", turns[0])
	}
	if plan["explanation"] != "three steps" {
		t.Errorf("explanation = %#v", plan["explanation"])
	}
	steps, ok := plan["steps"].([]any)
	if !ok || len(steps) != 3 {
		t.Fatalf("steps = %#v", plan["steps"])
	}
	if got := steps[1].(map[string]any)["status"]; got != "in_progress" {
		t.Errorf("step 1 status = %#v, want in_progress", got)
	}
	if got := steps[1].(map[string]any)["text"]; got != "create notes.md" {
		t.Errorf("step 1 text = %#v", got)
	}
}

func TestSnapshotOmitsAbsentPlan(t *testing.T) {
	body := conversationSnapshotBody(t, chatsvc.Snapshot{
		SessionID: domain.SessionID("p1-1"),
		Mode:      domain.SessionModeChat,
		Turns: []domain.ConversationTurn{{
			ID: "turn-1", State: domain.TurnStateCompleted, RequestedAt: time.Now().UTC(),
		}},
	})
	turn := body["turns"].([]any)[0].(map[string]any)
	if _, present := turn["plan"]; present {
		t.Fatalf("a turn with no plan carried one: %#v", turn)
	}
}

func TestSnapshotExposesModelRerouteSeparatelyFromSettings(t *testing.T) {
	at := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	body := conversationSnapshotBody(t, chatsvc.Snapshot{
		SessionID: domain.SessionID("p1-1"),
		Mode:      domain.SessionModeChat,
		Conversation: domain.ConversationRecord{
			Settings: domain.ConversationSettings{Model: "gpt-5.6-sol"},
			ModelReroute: &domain.ConversationModelReroute{
				FromModel:      "gpt-5.6-sol",
				ToModel:        "gpt-5.6-safety",
				Reason:         "highRiskCyberActivity",
				ProviderTurnID: "pt-1",
				At:             at,
			},
		},
	})

	reroute, ok := body["modelReroute"].(map[string]any)
	if !ok {
		t.Fatalf("no modelReroute: %#v", body["modelReroute"])
	}
	if reroute["toModel"] != "gpt-5.6-safety" || reroute["fromModel"] != "gpt-5.6-sol" {
		t.Fatalf("reroute = %#v", reroute)
	}
	if reroute["reason"] != "highRiskCyberActivity" {
		t.Errorf("reason = %#v", reroute["reason"])
	}
	if reroute["providerTurnId"] != "pt-1" {
		t.Errorf("turn = %#v", reroute["providerTurnId"])
	}
	if reroute["at"] != "2026-08-02T12:00:00Z" {
		t.Errorf("at = %#v", reroute["at"])
	}
	settings := body["settings"].(map[string]any)
	if settings["model"] != "gpt-5.6-sol" {
		t.Errorf("settings model = %#v", settings["model"])
	}
}

func TestSnapshotExposesAccountAndReauthDemand(t *testing.T) {
	at := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	body := conversationSnapshotBody(t, chatsvc.Snapshot{
		SessionID: domain.SessionID("p1-1"),
		Mode:      domain.SessionModeChat,
		Conversation: domain.ConversationRecord{
			Account: &domain.ConversationAccount{
				AuthMode:         "chatgpt",
				PlanLabel:        "pro",
				ReauthRequiredAt: &at,
				ReauthReason:     "unauthorized",
			},
		},
	})

	account, ok := body["account"].(map[string]any)
	if !ok {
		t.Fatalf("no account: %#v", body["account"])
	}
	if account["authMode"] != "chatgpt" || account["planLabel"] != "pro" {
		t.Fatalf("account = %#v", account)
	}
	if account["reauthRequiredAt"] != "2026-08-02T12:00:00Z" {
		t.Errorf("reauthRequiredAt = %#v", account["reauthRequiredAt"])
	}
	if account["reauthReason"] != "unauthorized" {
		t.Errorf("reauthReason = %#v", account["reauthReason"])
	}
	if account["authenticationState"] != "required" {
		t.Fatalf("legacy demand state = %#v", account)
	}
}

func TestSnapshotSeparatesVerifiedAuthenticationFromFailureHistory(t *testing.T) {
	failed := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	verified := failed.Add(time.Minute)
	body := conversationSnapshotBody(t, chatsvc.Snapshot{Conversation: domain.ConversationRecord{
		Account: &domain.ConversationAccount{AuthenticationState: "authenticated", AuthVerifiedAt: &verified,
			LastAuthFailureAt: &failed, LastAuthFailureReason: "expired", AuthFailureID: "failure-1"},
	}})
	account := body["account"].(map[string]any)
	if account["authenticationState"] != "authenticated" || account["authVerifiedAt"] != "2026-08-02T12:01:00Z" ||
		account["lastAuthFailureAt"] != "2026-08-02T12:00:00Z" || account["lastAuthFailureReason"] != "expired" || account["authFailureId"] != "failure-1" {
		t.Fatalf("recovered auth facts = %#v", account)
	}
	if _, ok := account["reauthRequiredAt"]; ok {
		t.Fatalf("recovered account still demands credentials: %#v", account)
	}
}

func TestSnapshotExposesThreadState(t *testing.T) {
	archived := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	body := conversationSnapshotBody(t, chatsvc.Snapshot{
		SessionID: domain.SessionID("p1-1"),
		Mode:      domain.SessionModeChat,
		Conversation: domain.ConversationRecord{
			ThreadState: &domain.ConversationThreadState{
				Status:     domain.ThreadStatusActive,
				WaitingOn:  []string{"waiting_on_approval"},
				ArchivedAt: &archived,
			},
		},
	})

	state, ok := body["threadState"].(map[string]any)
	if !ok {
		t.Fatalf("no threadState: %#v", body["threadState"])
	}
	if state["status"] != "active" {
		t.Fatalf("status = %#v", state["status"])
	}
	flags, ok := state["waitingOn"].([]any)
	if !ok || len(flags) != 1 || flags[0] != "waiting_on_approval" {
		t.Fatalf("waitingOn = %#v", state["waitingOn"])
	}
	if state["archivedAt"] != "2026-08-02T12:00:00Z" {
		t.Errorf("archivedAt = %#v", state["archivedAt"])
	}
	if _, present := state["closedAt"]; present {
		t.Error("a thread that was never closed reported a closedAt")
	}
}

func TestSnapshotExposesMCPServerHealth(t *testing.T) {
	body := conversationSnapshotBody(t, chatsvc.Snapshot{
		SessionID: domain.SessionID("p1-1"),
		Mode:      domain.SessionModeChat,
		Conversation: domain.ConversationRecord{
			MCPServers: []domain.ConversationMCPServer{
				{Name: "probe", Status: "ready"},
				{Name: "github", Status: "failed",
					Error: "connection refused", FailureReason: "reauthenticationRequired"},
			},
		},
	})

	servers, ok := body["mcpServers"].([]any)
	if !ok || len(servers) != 2 {
		t.Fatalf("mcpServers = %#v", body["mcpServers"])
	}
	if servers[0].(map[string]any)["name"] != "probe" {
		t.Fatalf("order = %#v", servers)
	}
	failed := servers[1].(map[string]any)
	if failed["failureReason"] != "reauthenticationRequired" || failed["error"] != "connection refused" {
		t.Fatalf("failed server = %#v", failed)
	}
}

func TestSnapshotNamesStreamedTextByActivityKind(t *testing.T) {
	for _, tc := range []struct {
		kind    domain.ActivityKind
		wantKey string
	}{
		{domain.ActivityKindReasoning, "text"},
		{domain.ActivityKindCommand, "terminalInput"},
		{domain.ActivityKindMCPTool, "progress"},
		{domain.ActivityKindFileChange, "patchOutput"},
		{domain.ActivityKindSystem, "streamedText"},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			body := conversationSnapshotBody(t, chatsvc.Snapshot{
				SessionID: domain.SessionID("p1-1"),
				Mode:      domain.SessionModeChat,
				Activities: []domain.ConversationActivity{{
					ID:                    "a1",
					Kind:                  tc.kind,
					Status:                domain.ActivityStatusRunning,
					StreamedText:          "streamed",
					StreamedTextTruncated: true,
					CreatedAt:             time.Now().UTC(),
				}},
			})
			activity := body["activities"].([]any)[0].(map[string]any)
			detail, ok := activity["detail"].(map[string]any)
			if !ok {
				t.Fatalf("no detail: %#v", activity)
			}
			if detail[tc.wantKey] != "streamed" {
				t.Fatalf("detail[%q] = %#v (detail = %#v)", tc.wantKey, detail[tc.wantKey], detail)
			}
			if detail[tc.wantKey+"Truncated"] != true {
				t.Errorf("truncation was not reported: %#v", detail)
			}
			if tc.kind == domain.ActivityKindCommand {
				if _, present := detail["output"]; present {
					t.Error("terminal input leaked into output")
				}
			}
		})
	}
}

func reloadMCP(t *testing.T, svc *fakeConversationService) (int, map[string]any) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Sessions:      newFakeSessionService(),
		Conversations: svc,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/api/v1/sessions/p1-1/conversation/mcp/reload", "application/json", nil)
	if err != nil {
		t.Fatalf("POST reload: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp.StatusCode, decoded
}

func TestReloadMCPServersReportsWhatCameBack(t *testing.T) {
	status, body := reloadMCP(t, &fakeConversationService{
		mcpServers: []domain.ConversationMCPServer{{Name: "probe", Status: "ready"}},
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %#v", status, body)
	}
	servers, ok := body["servers"].([]any)
	if !ok || len(servers) != 1 {
		t.Fatalf("servers = %#v", body["servers"])
	}
	if servers[0].(map[string]any)["name"] != "probe" {
		t.Fatalf("server = %#v", servers[0])
	}
}

func TestReloadMCPServersUnsupportedIsAConflict(t *testing.T) {
	status, body := reloadMCP(t, &fakeConversationService{
		reloadErr: chatsvc.ErrMCPReloadUnsupported,
	})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, body = %#v", status, body)
	}
	if code := errorCode(body); code != "CHAT_MCP_RELOAD_UNSUPPORTED" {
		t.Fatalf("code = %q, body = %#v", code, body)
	}
}

func TestReloadMCPServersRefusedWhileBusySaysWhy(t *testing.T) {
	status, body := reloadMCP(t, &fakeConversationService{reloadErr: chatsvc.ErrTurnRunning})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, body = %#v", status, body)
	}
	if code := errorCode(body); code != "CHAT_TURN_RUNNING" {
		t.Fatalf("code = %q", code)
	}
	message, _ := body["message"].(string)
	if message != "stop the agent before reloading its tool servers: it is in the middle of a turn" {
		t.Fatalf("message = %q", message)
	}
}

func errorCode(body map[string]any) string {
	code, _ := body["code"].(string)
	return code
}

func TestSnapshotAdvertisesWhatTheProviderCanDo(t *testing.T) {
	body := conversationSnapshotBody(t, chatsvc.Snapshot{
		SessionID: domain.SessionID("p1-1"),
		Mode:      domain.SessionModeChat,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilitySteer:      true,
			ports.ChatCapabilityCompaction: true,
			ports.ChatCapabilityFork:       false,
		},
	})

	raw, ok := body["capabilities"].([]any)
	if !ok {
		t.Fatalf("capabilities = %#v, want a list", body["capabilities"])
	}
	got := make([]string, 0, len(raw))
	for _, v := range raw {
		got = append(got, v.(string))
	}
	want := []string{"compaction", "steer"}
	if len(got) != len(want) {
		t.Fatalf("capabilities = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("capabilities = %v, want %v", got, want)
		}
	}
}

func TestSnapshotOmitsCapabilitiesWithoutAController(t *testing.T) {
	body := conversationSnapshotBody(t, chatsvc.Snapshot{
		SessionID: domain.SessionID("p1-1"),
		Mode:      domain.SessionModeChat,
	})
	if _, present := body["capabilities"]; present {
		t.Errorf("capabilities present with no controller: %#v", body["capabilities"])
	}
}
