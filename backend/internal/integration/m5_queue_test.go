package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

// m5VerifyQueue 穿过 Operator HTTP、真实 Chat 队列及 SQLite；删掉人工来源或回执派生会失败。
func m5VerifyQueue(t *testing.T, f *m2ffFixture, d *m5Driver, a domain.WorkflowAttempt) {
	t.Helper()
	ctx := context.Background()
	path := f.attemptPath(a.WorkflowID, a.ID, "interventions")
	conversation := d.Conversations()[len(d.Conversations())-1]
	deadline := time.Now().Add(10 * time.Second)
	for conversation.LastTurnID() == "" {
		if time.Now().After(deadline) {
			t.Fatal("初始 turn 未派发")
		}
		time.Sleep(10 * time.Millisecond)
	}
	running := conversation.LastTurnID()
	queue := func(key string) controllers.WorkflowInterventionResponse {
		return m4HTTP[controllers.WorkflowInterventionEnvelope](t, f, http.MethodPost, path, map[string]any{"mode": "queue", "text": "原排队内容", "clientMessageId": key}).Intervention
	}
	read := func(id string) controllers.WorkflowInterventionResponse {
		rows := m4HTTP[controllers.WorkflowInterventionReceiptsResponse](t, f, http.MethodGet, path, nil)
		for _, row := range rows.Interventions {
			if row.ID == id {
				return row
			}
		}
		t.Fatalf("回执丢失: %s", id)
		return controllers.WorkflowInterventionResponse{}
	}
	first := queue("m5-edit-cancel")
	if first.State != "queued" {
		t.Fatalf("未排队: %+v", first)
	}
	turnPath := "/sessions/" + string(*a.SessionID) + "/conversation/turns/" + first.ChatTurnID
	if result := f.do(http.MethodPost, turnPath+"/queue/edit", m3JSON(t, map[string]any{"text": "编辑后保留身份", "clientMessageId": "m5-queue-edit"}), nil); result.Status != http.StatusNoContent {
		t.Fatalf("现有编辑接口: %d %s", result.Status, result.Raw)
	}
	conv, err := f.store.ConversationForSession(ctx, *a.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	message, err := f.store.QueuedTurnMessage(ctx, conv.ID, first.ChatTurnID)
	if err != nil || message.Text != "编辑后保留身份" || message.Origin != domain.MessageOriginHuman {
		t.Fatalf("队列编辑没有保存真实人工消息: %+v %v", message, err)
	}
	if edited := read(first.ID); edited.State != "queued" || edited.ClientMessageID != first.ClientMessageID {
		t.Fatalf("编辑变更回执身份: %+v", edited)
	}
	if result := f.do(http.MethodPost, turnPath+"/cancel", "", nil); result.Status != http.StatusNoContent {
		t.Fatalf("现有取消接口: %d %s", result.Status, result.Raw)
	}
	if cancelled := read(first.ID); cancelled.State != "cancelled" {
		t.Fatalf("取消后仍排队: %+v", cancelled)
	}
	if replay := queue("m5-edit-cancel"); replay.ID != first.ID || read(first.ID).State != "cancelled" {
		t.Fatalf("同键重放恢复了取消消息或新建回执: %+v", replay)
	}
	second := queue("m5-queue-deliver")
	if second.State != "queued" {
		t.Fatalf("第二条未排队: %+v", second)
	}
	conversation.ReplyAndComplete(running, "当前工作结束")
	deadline = time.Now().Add(10 * time.Second)
	for {
		turn, err := f.store.TurnByID(ctx, second.ChatTurnID)
		if err != nil {
			t.Fatal(err)
		}
		if turn.ProviderTurnID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("队列未真正投递: %+v", turn)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if delivered := read(second.ID); delivered.State != "delivered" || delivered.ProviderTurnID == "" {
		t.Fatalf("投递后仍排队: %+v", delivered)
	}
	if len(d.Conversations()) != 2 {
		t.Fatal("队列管理创建了额外 Session")
	}
}
