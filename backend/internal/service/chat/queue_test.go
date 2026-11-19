package chat_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func TestQueuedEditPreservesWorkerReports(t *testing.T) {
	const suffix = "\n\n<ao-worker-reports>\n[checkpoint] worker one\n\n[done] worker two\npr_created: https://example.com/pr/1\n</ao-worker-reports>"
	const example = "\n\n<ao-worker-reports>\nliteral user example\n</ao-worker-reports>"
	const stagedFiles = "\n\nAttached files (read these files in the workspace):\n- .ao/attachments/attachment-added.png"
	for _, tc := range []struct{ name, editedSuffix, humanSuffix string }{
		{"human text only", "", ""},
		{"legacy full text", suffix, ""},
		{"legacy trailing newline", suffix + "\n", ""},
		{"legacy trailing whitespace", suffix + "\r\n \t\u00a0", ""},
		{"older client adds a staged file after the recorded reports", suffix + stagedFiles, stagedFiles},
		{"authored report example", example, example},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, provider := steerHarness(t)
			ctx := context.Background()
			turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
				Text: "original" + suffix, ClientMessageID: "report-message", Origin: domain.MessageOriginHuman,
			})
			if err != nil {
				t.Fatal(err)
			}
			zero := int64(0)
			text := "Review café 👋" + tc.editedSuffix
			wantText := "Review café 👋" + tc.humanSuffix + suffix
			edit := chatsvc.QueuedMessageEdit{Text: text, ClientMessageID: "report-edit", ExpectedRevision: &zero}
			for range 2 {
				if err := h.svc.EditQueuedTurn(ctx, testSession, turn.ID, edit); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := h.svc.Snapshot(ctx, testSession)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, msg := range snapshot.Messages {
				if msg.TurnID == turn.ID {
					found = true
					if msg.Text != wantText || msg.Revision != 1 {
						t.Fatalf("saved message lost or duplicated worker reports: %+v", msg)
					}
				}
			}
			if !found {
				t.Fatal("edited message missing")
			}
			if _, err := h.svc.PromoteQueuedTurn(ctx, testSession, turn.ID); err != nil {
				t.Fatal(err)
			}
			if err := h.svc.EditQueuedTurn(ctx, testSession, turn.ID, edit); err != nil {
				t.Fatal(err)
			}
			calls := provider.steers()
			if len(calls) != 1 || calls[0].msg.Text != wantText || strings.Count(calls[0].msg.Text, "[checkpoint]") != 1 {
				t.Fatalf("provider must receive edited text and reports once: %+v", calls)
			}
		})
	}
}

func TestQueuedReportEditRequiresHumanContent(t *testing.T) {
	const suffix = "\n\n<ao-worker-reports>\n[checkpoint] worker\n</ao-worker-reports>"
	for _, text := range []string{"", suffix} {
		h, _ := steerHarness(t)
		ctx := context.Background()
		turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
			Text: "original" + suffix, ClientMessageID: "report-message", Origin: domain.MessageOriginHuman,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := h.svc.EditQueuedTurn(ctx, testSession, turn.ID, chatsvc.QueuedMessageEdit{Text: text}); !errors.Is(err, chatsvc.ErrQueuedTurnTextRequired) {
			t.Fatalf("empty human edit = %v, want text required", err)
		}
		if err := h.svc.EditQueuedTurn(ctx, testSession, turn.ID, chatsvc.QueuedMessageEdit{
			Text: text, Content: []ports.ChatContent{{Type: "image", MIMEType: "image/png", Data: "aGVsbG8="}},
		}); err != nil {
			t.Fatalf("attachment-only edit: %v", err)
		}
		snapshot, err := h.svc.Snapshot(ctx, testSession)
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range snapshot.Messages {
			if msg.TurnID == turn.ID && msg.Text != suffix {
				t.Fatalf("report suffix changed: %q", msg.Text)
			}
		}
	}
}

func TestQueuedEditAttachmentChanges(t *testing.T) {
	zero := int64(0)
	one := int64(1)
	tenMiB := ports.ChatContent{Type: "image", MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(make([]byte, 10<<20))}
	overTenMiB := ports.ChatContent{Type: "image", MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(make([]byte, (10<<20)+1))}
	fiveMiB := ports.ChatContent{Type: "image", MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(make([]byte, 5<<20))}
	overFiveMiB := ports.ChatContent{Type: "image", MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(make([]byte, (5<<20)+1))}
	atLimit := []ports.ChatContent{tenMiB, tenMiB, fiveMiB}
	atLimitData := []string{tenMiB.Data, tenMiB.Data, fiveMiB.Data}
	for _, tc := range []struct {
		name     string
		unedited bool
		initial  []ports.ChatContent
		text     string
		retained *[]int
		revision *int64
		add      []ports.ChatContent
		wantData []string
		wantErr  error
	}{
		{name: "without edit", unedited: true, text: "original", wantData: []string{"aGVsbG8="}},
		{name: "preserve omitted", text: "updated", wantData: []string{"aGVsbG8="}},
		{name: "preserve explicit", text: "updated", retained: &[]int{0}, revision: &zero, wantData: []string{"aGVsbG8="}},
		{name: "remove", text: "updated", retained: &[]int{}, revision: &zero},
		{name: "replace", text: "updated", retained: &[]int{}, revision: &zero, add: []ports.ChatContent{{Type: "image", MIMEType: "image/png", Data: "bmV3"}}, wantData: []string{"bmV3"}},
		{name: "append", text: "updated", add: []ports.ChatContent{{Type: "image", MIMEType: "image/png", Data: "bmV3"}}, wantData: []string{"aGVsbG8=", "bmV3"}},
		{name: "image only", wantData: []string{"aGVsbG8="}},
		{name: "empty after removal", retained: &[]int{}, revision: &zero, wantErr: chatsvc.ErrQueuedTurnTextRequired},
		{name: "stale", text: "updated", retained: &[]int{}, revision: &one, wantErr: chatsvc.ErrQueuedEditConflict},
		{name: "missing revision", text: "updated", retained: &[]int{}, wantErr: chatsvc.ErrQueuedContentInvalid},
		{name: "invalid index", text: "updated", retained: &[]int{1}, revision: &zero, wantErr: chatsvc.ErrQueuedContentInvalid},
		{name: "duplicate index", text: "updated", retained: &[]int{0, 0}, revision: &zero, wantErr: chatsvc.ErrQueuedContentInvalid},
		{name: "negative index", text: "updated", retained: &[]int{-1}, revision: &zero, wantErr: chatsvc.ErrQueuedContentInvalid},
		{name: "preserve at size limit", text: "updated", initial: atLimit, wantData: atLimitData},
		{name: "append at size limit", text: "updated", initial: atLimit[:2], add: atLimit[2:], wantData: atLimitData},
		{name: "append over size limit", text: "updated", initial: atLimit[:2], add: []ports.ChatContent{overFiveMiB}, wantErr: chatsvc.ErrQueuedContentInvalid},
		{name: "append over per-file limit", text: "updated", add: []ports.ChatContent{overTenMiB}, wantErr: chatsvc.ErrQueuedContentInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, provider := steerHarness(t)
			ctx := context.Background()
			initial := tc.initial
			if initial == nil {
				initial = []ports.ChatContent{{Type: "image", MIMEType: "image/png", Data: "aGVsbG8="}}
			}
			turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "original", ClientMessageID: "queued-edit",
				Origin: domain.MessageOriginHuman, Content: initial})
			if err != nil {
				t.Fatal(err)
			}
			if !tc.unedited {
				err = h.svc.EditQueuedTurn(ctx, testSession, turn.ID, chatsvc.QueuedMessageEdit{
					Text: tc.text, Content: tc.add, RetainedContent: tc.retained, ExpectedRevision: tc.revision,
				})
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("edit = %v, want %v", err, tc.wantErr)
			}
			if _, err := h.svc.PromoteQueuedTurn(ctx, testSession, turn.ID); err != nil {
				t.Fatal(err)
			}
			calls := provider.steers()
			if len(calls) != 1 {
				t.Fatalf("provider calls = %d", len(calls))
			}
			wantText, wantData := tc.text, tc.wantData
			if tc.wantErr != nil {
				wantText, wantData = "original", nil
				for _, block := range initial {
					wantData = append(wantData, block.Data)
				}
			}
			if calls[0].msg.Text != wantText || len(calls[0].msg.Content) != len(wantData) {
				t.Fatalf("provider got text %q and %d attachments; want %q and %d", calls[0].msg.Text, len(calls[0].msg.Content), wantText, len(wantData))
			}
			for i, data := range wantData {
				if calls[0].msg.Content[i].Data != data {
					t.Fatalf("attachment %d changed", i)
				}
			}
		})
	}
}

func TestQueuedEditRetryAfterCommittedResponseIsLost(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
	originalMessage := ports.ChatUserMessage{
		Text: "original", ClientMessageID: "queue-original", Origin: domain.MessageOriginHuman,
		Content: []ports.ChatContent{{Type: "image", MIMEType: "image/png", Data: "b2xk"}},
	}
	turn, err := h.svc.Send(ctx, testSession, originalMessage)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	edit := chatsvc.QueuedMessageEdit{
		ClientMessageID: "queue-edit-receipt", Text: "updated", ExpectedRevision: &zero,
		RetainedContent: &[]int{0},
		Content:         []ports.ChatContent{{Type: "image", MIMEType: "image/png", Data: "bmV3"}},
	}
	// The first request commits, but the caller never receives its success.
	if err := h.svc.EditQueuedTurn(ctx, testSession, turn.ID, edit); err != nil {
		t.Fatal(err)
	}
	committed, err := h.st.QueuedTurnMessage(ctx, conversation.ID, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Revision != 1 {
		t.Fatalf("committed revision = %d, want 1", committed.Revision)
	}
	if duplicate, err := h.svc.Send(ctx, testSession, originalMessage); err != nil || duplicate.ID != "" {
		t.Fatalf("retry original send after queue edit = %+v, err = %v", duplicate, err)
	}
	changedMessage := originalMessage
	changedMessage.Text = "another request"
	if _, err := h.svc.Send(ctx, testSession, changedMessage); !errors.Is(err, domain.ErrClientMessageConflict) {
		t.Fatalf("changed original send after queue edit = %v, want conflict", err)
	}
	// The renderer retries the identical request with its original revision.
	if err := h.svc.EditQueuedTurn(ctx, testSession, turn.ID, edit); err != nil {
		t.Fatalf("retry: %v", err)
	}
	retried, err := h.st.QueuedTurnMessage(ctx, conversation.ID, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Revision != committed.Revision || retried.Text != committed.Text || retried.DeliveryContentJSON != committed.DeliveryContentJSON {
		t.Fatalf("retry mutated the committed message: before=%+v after=%+v", committed, retried)
	}
	for _, change := range []struct {
		name   string
		mutate func(*chatsvc.QueuedMessageEdit)
	}{
		{"text", func(e *chatsvc.QueuedMessageEdit) { e.Text = "different" }},
		{"retained attachments", func(e *chatsvc.QueuedMessageEdit) { e.RetainedContent = &[]int{} }},
		{"native bytes", func(e *chatsvc.QueuedMessageEdit) {
			e.Content = []ports.ChatContent{{Type: "image", MIMEType: "image/png", Data: "eA=="}}
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			changed := edit
			change.mutate(&changed)
			if err := h.svc.EditQueuedTurn(ctx, testSession, turn.ID, changed); !errors.Is(err, store.ErrQueuedEditDeliveryConflict) {
				t.Fatalf("changed request with reused key = %v, want conflict", err)
			}
		})
	}
	if _, err := h.svc.PromoteQueuedTurn(ctx, testSession, turn.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.EditQueuedTurn(ctx, testSession, turn.ID, edit); err != nil {
		t.Fatalf("retry after dispatch: %v", err)
	}
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.EditQueuedTurn(ctx, testSession, turn.ID, edit); err != nil {
		t.Fatalf("retry after controller teardown: %v", err)
	}
	calls := provider.steers()
	if len(calls) != 1 || calls[0].msg.Text != "updated" || len(calls[0].msg.Content) != 2 || calls[0].msg.Content[0].Data != "b2xk" || calls[0].msg.Content[1].Data != "bmV3" {
		t.Fatalf("provider must receive the edited text and attachments exactly once: %+v", calls)
	}
}
