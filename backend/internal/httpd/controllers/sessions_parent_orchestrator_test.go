package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestSessionsAPIParentOrchestratorPersistence 验证真实持久关系经过列表、详情和重启仍一致；删除归属装配或父级校验即失败。
func TestSessionsAPIParentOrchestratorPersistence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := sqlitetest.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"mer", "other"} {
		if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: id, Path: dir, RegisteredAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	want := map[domain.SessionID]domain.SessionID{}
	bindings := map[domain.SessionID]domain.SessionID{}
	create := func(rec domain.SessionRecord) domain.SessionID {
		t.Helper()
		if rec.ProjectID == "" {
			rec.ProjectID = "mer"
		}
		if rec.Kind == "" {
			rec.Kind = domain.KindWorker
		}
		rec.Harness, rec.CreatedAt, rec.UpdatedAt = domain.HarnessCodex, now, now
		created, err := st.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		want[created.ID], bindings[created.ID] = "", rec.OrchestratorSessionID
		return created.ID
	}
	main := create(domain.SessionRecord{Kind: domain.KindOrchestrator})
	independent := create(domain.SessionRecord{Kind: domain.KindOrchestrator, IndependentOrchestrator: true})
	manual := create(domain.SessionRecord{})
	// TUI 创建链没有对话或活动事件，必须仅凭持久创建者恢复主编排器归属。
	tuiMain := create(domain.SessionRecord{Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI})
	tuiWorker := create(domain.SessionRecord{Mode: domain.SessionModeTUI, SpawnParentSessionID: tuiMain})
	want[tuiWorker] = tuiMain
	tuiNested := create(domain.SessionRecord{SpawnParentSessionID: tuiWorker})
	want[tuiNested] = tuiMain
	create(domain.SessionRecord{SpawnParentSessionID: manual})
	create(domain.SessionRecord{SpawnParentSessionID: "missing"})
	create(domain.SessionRecord{ProjectID: "other", SpawnParentSessionID: tuiMain})
	// 预创建转正也必须保存创建者，后续通用更新不能清空该事实。
	prepared := create(domain.SessionRecord{IsTaskPreparation: true})
	preparedRecord, found, err := st.GetSession(ctx, prepared)
	if err != nil || !found {
		t.Fatalf("load preparation: found=%v err=%v", found, err)
	}
	preparedRecord.SpawnParentSessionID = tuiWorker
	if promoted, err := st.PromoteTaskPreparation(ctx, prepared, preparedRecord); err != nil || !promoted {
		t.Fatalf("promote preparation: promoted=%v err=%v", promoted, err)
	}
	preparedRecord.SpawnParentSessionID = ""
	if err := st.UpdateSession(ctx, preparedRecord); err != nil {
		t.Fatal(err)
	}
	want[prepared] = tuiMain
	spawn := func(parent, child domain.SessionID, status domain.ActivityStatus) {
		t.Helper()
		scope := domain.ConversationScopeSession
		if parent == main {
			scope = domain.ConversationScopeProject
		}
		conversation, err := st.CreateConversation(ctx, "conv-"+string(parent), scope, "mer", parent, now)
		if err != nil {
			t.Fatal(err)
		}
		detail, err := json.Marshal(map[string]string{"action": "session.spawned", "sourceSessionId": string(parent), "targetSessionId": string(child)})
		if err != nil {
			t.Fatal(err)
		}
		key := "ao-action:session.spawned:" + string(child)
		if err := st.UpsertActivity(ctx, conversation.ID, "", domain.ConversationActivity{
			ID: key + ":" + string(parent), ProviderItemID: key, Kind: domain.ActivityKindAOAction, Status: status, Detail: detail,
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	mainWorker := create(domain.SessionRecord{})
	spawn(main, mainWorker, domain.ActivityStatusCompleted)
	want[mainWorker] = main
	boundWorker := create(domain.SessionRecord{OrchestratorSessionID: independent})
	want[boundWorker] = independent
	oldIndependentWorker := create(domain.SessionRecord{})
	spawn(independent, oldIndependentWorker, domain.ActivityStatusCompleted)
	want[oldIndependentWorker] = independent
	// 实际创建者优先于显式汇报绑定和历史事件，二者不能覆盖创建链。
	preferred := create(domain.SessionRecord{SpawnParentSessionID: tuiMain, OrchestratorSessionID: independent})
	spawn(independent, preferred, domain.ActivityStatusCompleted)
	want[preferred] = tuiMain
	preferredEvent := create(domain.SessionRecord{SpawnParentSessionID: tuiWorker})
	spawn(independent, preferredEvent, domain.ActivityStatusCompleted)
	want[preferredEvent] = tuiMain
	create(domain.SessionRecord{SpawnParentSessionID: "missing", OrchestratorSessionID: independent})
	nested := create(domain.SessionRecord{})
	spawn(mainWorker, nested, domain.ActivityStatusCompleted)
	want[nested] = main
	spawn(manual, create(domain.SessionRecord{}), domain.ActivityStatusCompleted)
	spawn(main, create(domain.SessionRecord{}), domain.ActivityStatusFailed)
	spawn(main, create(domain.SessionRecord{ProjectID: "other"}), domain.ActivityStatusCompleted)
	create(domain.SessionRecord{OrchestratorSessionID: "missing"})
	create(domain.SessionRecord{OrchestratorSessionID: manual})
	cycleA, cycleB := create(domain.SessionRecord{}), create(domain.SessionRecord{})
	spawn(cycleA, cycleB, domain.ActivityStatusCompleted)
	spawn(cycleB, cycleA, domain.ActivityStatusCompleted)
	ambiguous := create(domain.SessionRecord{})
	spawn(main, ambiguous, domain.ActivityStatusCompleted)
	spawn(independent, ambiguous, domain.ActivityStatusCompleted)
	removed := create(domain.SessionRecord{Kind: domain.KindOrchestrator, IndependentOrchestrator: true})
	spawn(removed, create(domain.SessionRecord{}), domain.ActivityStatusCompleted)
	create(domain.SessionRecord{OrchestratorSessionID: removed})
	create(domain.SessionRecord{SpawnParentSessionID: removed})
	if _, err := st.DeleteSession(ctx, removed); err != nil {
		t.Fatal(err)
	}
	delete(want, removed)
	// 项目对话改绑后仍归原创建者，避免旧 worker 随当前主编排器漂移。
	replacement := create(domain.SessionRecord{Kind: domain.KindOrchestrator})
	if _, err := st.CreateConversation(ctx, "replacement", domain.ConversationScopeProject, "mer", replacement, now); err != nil {
		t.Fatal(err)
	}
	assertView := func(view map[string]any) {
		t.Helper()
		id := domain.SessionID(view["id"].(string))
		parent, exists := view["parentOrchestratorId"]
		if expected, known := want[id]; !known || (expected == "" && exists) || (expected != "" && parent != string(expected)) {
			t.Fatalf("session %s parent = %v (present=%v), want %q", id, parent, exists, expected)
		}
		if binding, _ := view["orchestratorSessionId"].(string); binding != string(bindings[id]) {
			t.Fatalf("session %s reporting binding changed: %q", id, binding)
		}
		if _, exposed := view["spawnParentSessionId"]; exposed {
			t.Fatalf("session %s exposed internal spawn parent", id)
		}
	}
	for _, phase := range []string{"before restart", "after restart"} {
		t.Run(phase, func(t *testing.T) {
			for id, parent := range map[domain.SessionID]domain.SessionID{tuiWorker: tuiMain, tuiNested: tuiWorker, prepared: tuiWorker} {
				rec, found, err := st.GetSession(ctx, id)
				if err != nil || !found || rec.SpawnParentSessionID != parent {
					t.Fatalf("session %s durable creator=%q want=%q found=%v err=%v", id, rec.SpawnParentSessionID, parent, found, err)
				}
			}
			if _, err := st.ConversationForSession(ctx, tuiMain); !errors.Is(err, domain.ErrNoConversation) {
				t.Fatalf("TUI orchestrator unexpectedly has a conversation: %v", err)
			}
			svc := sessionsvc.NewWithDeps(sessionsvc.Deps{Store: st, DataDir: dir})
			server := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{}))
			defer server.Close()
			body, status, _ := doRequest(t, server, http.MethodGet, "/api/v1/sessions", "")
			if status != http.StatusOK {
				t.Fatalf("list: %d %s", status, body)
			}
			var list struct {
				Sessions []map[string]any `json:"sessions"`
			}
			if err := json.Unmarshal(body, &list); err != nil {
				t.Fatal(err)
			}
			if len(list.Sessions) != len(want) {
				t.Fatalf("list size = %d, want %d", len(list.Sessions), len(want))
			}
			for _, view := range list.Sessions {
				assertView(view)
				body, status, _ := doRequest(t, server, http.MethodGet, "/api/v1/sessions/"+view["id"].(string), "")
				if status != http.StatusOK {
					t.Fatalf("get: %d %s", status, body)
				}
				var detail struct {
					Session map[string]any `json:"session"`
				}
				if err := json.Unmarshal(body, &detail); err != nil {
					t.Fatal(err)
				}
				assertView(detail.Session)
			}
		})
		if phase == "before restart" {
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = sqlite.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
