package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestFindSharedFixtures(t *testing.T) {
	data, err := os.ReadFile("../../../../test/chat-find.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name, Source, Query string
		Markdown            bool
		Blocks              []string
		Ranges              []struct{ Block, Start, End int }
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			blocks := FindBlocks(fixture.Source, fixture.Markdown)
			if !reflect.DeepEqual(blocks, fixture.Blocks) {
				t.Fatalf("blocks = %#v; want %#v", blocks, fixture.Blocks)
			}
			role := domain.MessageRoleUser
			if fixture.Markdown {
				role = domain.MessageRoleAssistant
			} else if !reflect.DeepEqual(FindUserBlocks(fixture.Source), fixture.Blocks) {
				// A person's message is searched as the light Markdown its bubble
				// renders; a plain fixture that subset reshapes has no message form.
				return
			}
			result, err := findMessages(context.Background(), domain.ConversationRecord{ID: "c"}, []domain.ConversationMessage{{ID: "m", Role: role, Origin: domain.MessageOriginHuman, Text: fixture.Source, Revision: 1}}, fixture.Query, "")
			if err != nil {
				t.Fatal(err)
			}
			if result.Total != len(fixture.Ranges) {
				t.Fatalf("total = %d; want %d (%#v)", result.Total, len(fixture.Ranges), result.Occurrences)
			}
			for i, hit := range result.Occurrences {
				want := fixture.Ranges[i]
				if hit.Block != want.Block || hit.Start != want.Start || hit.End != want.End {
					t.Fatalf("hit = %#v; want %#v", hit, want)
				}
			}
		})
	}
}

func TestFindPagesAreCompleteAndRevisionBound(t *testing.T) {
	messages := []domain.ConversationMessage{{ID: "m", Role: domain.MessageRoleAssistant, Text: strings.Repeat("needle ", 451), Revision: 1}}
	conversation := domain.ConversationRecord{ID: "c", ActiveBranchID: "branch"}
	first, err := findMessages(context.Background(), conversation, messages, "needle", "")
	if err != nil || first.Total != 451 || len(first.Occurrences) != 200 || first.NextCursor == "" {
		t.Fatalf("first = %#v, %v", first, err)
	}
	second, err := findMessages(context.Background(), conversation, messages, "needle", first.NextCursor)
	if err != nil || second.Total != 451 || len(second.Occurrences) != 200 || second.Occurrences[0].Start != 1400 {
		t.Fatalf("second = %#v, %v", second, err)
	}
	last, err := findMessages(context.Background(), conversation, messages, "needle", second.NextCursor)
	if err != nil || len(last.Occurrences) != 51 || last.NextCursor != "" {
		t.Fatalf("last = %#v, %v", last, err)
	}
	for _, change := range []string{"query", "revision", "branch"} {
		t.Run(change, func(t *testing.T) {
			copyMessages := append([]domain.ConversationMessage(nil), messages...)
			copyConversation, query := conversation, "needle"
			switch change {
			case "query":
				query = "need"
			case "revision":
				copyMessages[0].Revision++
			case "branch":
				copyConversation.ActiveBranchID = "other"
			}
			if _, err := findMessages(context.Background(), copyConversation, copyMessages, query, first.NextCursor); !errors.Is(err, ErrFindCursor) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestFindExcludesHiddenMessageBodies(t *testing.T) {
	messages := []domain.ConversationMessage{
		{ID: "human", Role: domain.MessageRoleUser, Origin: domain.MessageOriginHuman, Text: "needle\n\n<ao-worker-reports>\nsecret\n</ao-worker-reports>"},
		{ID: "automation", Role: domain.MessageRoleUser, Origin: domain.MessageOriginAutomation, Text: "needle secret"},
		{ID: "daemon", Role: domain.MessageRoleAssistant, Origin: domain.MessageOriginDaemon, Text: "needle secret"},
		{ID: "continue", Role: domain.MessageRoleUser, Continuation: true, Text: "needle secret"},
		{ID: "attachment", Role: domain.MessageRoleUser, Text: "hi\n\nAttached files (read these files in the workspace):\n- .ao/attachments/attachment-secret.png"},
	}
	for _, query := range []string{"secret", "needle"} {
		result, err := findMessages(context.Background(), domain.ConversationRecord{}, messages, query, "")
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if query == "needle" {
			want = 1
		}
		if result.Total != want {
			t.Fatalf("%s: %#v", query, result)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := findMessages(ctx, domain.ConversationRecord{}, messages, "needle", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
}

func BenchmarkFindTenThousandMessages(b *testing.B) {
	messages := make([]domain.ConversationMessage, 10000)
	for i := range messages {
		messages[i] = domain.ConversationMessage{ID: "m", Role: domain.MessageRoleAssistant, Text: strings.Repeat("ordinary **text** ", 60) + "needle"}
	}
	for b.Loop() {
		if _, err := findMessages(context.Background(), domain.ConversationRecord{}, messages, "needle", ""); err != nil {
			b.Fatal(err)
		}
	}
}

func TestFindSideReadsOnlyCurrentLaunchAndDispatchedTurns(t *testing.T) {
	memory := newMemorySideStore()
	memory.runID = "run"
	memory.sides["side"] = domain.SideConversation{ID: "side", AppRunID: "run", State: "ready"}
	memory.turns["side"] = []domain.SideTurn{{ID: "done", State: "completed"}, {ID: "queue", State: "queued"}, {ID: "cancel", State: "cancelled"}}
	memory.messages["side"] = []domain.SideMessage{{ID: "visible", TurnID: "done", Text: "needle", Role: "assistant", Sequence: 1}, {ID: "queued", TurnID: "queue", Text: "needle"}, {ID: "cancelled", TurnID: "cancel", Text: "needle"}}
	page, err := memory.findSnapshot(context.Background(), "side")
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != "visible" {
		t.Fatalf("page=%#v, err=%v", page, err)
	}
	memory.runID = "another"
	if _, err := memory.findSnapshot(context.Background(), "side"); !errors.Is(err, ErrFindUnavailable) {
		t.Fatalf("other launch = %v", err)
	}
}

type findTestStore struct {
	Store
	messages []domain.ConversationMessage
	branch   string
}

func (s *findTestStore) ConversationFindSource(context.Context, string) (domain.ConversationRecord, []domain.ConversationMessage, error) {
	return domain.ConversationRecord{ID: "c", ActiveBranchID: s.branch}, s.messages, nil
}
func (s *findTestStore) ConversationFindWindow(context.Context, string, int64) (domain.ConversationFindWindow, error) {
	return domain.ConversationFindWindow{}, nil
}

func TestFindCacheRevalidatesDurableChanges(t *testing.T) {
	store := &findTestStore{branch: "b", messages: []domain.ConversationMessage{
		{ID: "m", Role: domain.MessageRoleAssistant, Text: strings.Repeat("needle ", 451), Revision: 1},
		{ID: "live", Role: domain.MessageRoleAssistant, Text: "needle", Streaming: true, Revision: 1},
	}}
	service := &Service{store: store}
	first, err := service.FindConversation(context.Background(), "c", "needle", "")
	if err != nil || first.Total != 452 {
		t.Fatalf("first=%#v, err=%v", first, err)
	}
	store.messages[1].Text, store.messages[1].Revision = "needle needle", 2
	oldPage, err := service.FindConversation(context.Background(), "c", "needle", first.NextCursor)
	if err != nil || oldPage.Total != 452 || len(oldPage.Occurrences) != 200 {
		t.Fatalf("streaming snapshot page = %#v, %v", oldPage, err)
	}
	fresh, err := service.FindConversation(context.Background(), "c", "needle", "")
	if err != nil || fresh.Total != 453 {
		t.Fatalf("fresh=%#v, %v", fresh, err)
	}
	second, err := service.FindConversation(context.Background(), "c", "needle", fresh.NextCursor)
	if err != nil || len(second.Occurrences) != 200 {
		t.Fatalf("cached page=%#v, %v", second, err)
	}
	store.branch = "other"
	if _, err := service.FindConversation(context.Background(), "c", "needle", first.NextCursor); !errors.Is(err, ErrFindCursor) {
		t.Fatalf("stale branch cursor = %v", err)
	}
	store.branch = "b"
	store.messages[0].Revision++
	if _, err := service.FindConversation(context.Background(), "c", "needle", first.NextCursor); !errors.Is(err, ErrFindCursor) {
		t.Fatalf("stale message cursor = %v", err)
	}
}

func TestFindAnchorsAndPageMetadata(t *testing.T) {
	store := &findTestStore{branch: "b"}
	for i := range 501 {
		store.messages = append(store.messages, domain.ConversationMessage{ID: fmt.Sprintf("m%d", i), Role: domain.MessageRoleAssistant, Text: "needle", Revision: 1, Sequence: int64(i + 1)})
	}
	service := &Service{store: store}
	first, err := service.FindConversation(context.Background(), "c", "needle", "")
	if err != nil || first.Total != 501 || first.Offset != 0 || first.LastCursor == "" {
		t.Fatalf("first=%#v, %v", first, err)
	}
	page, err := service.FindConversation(context.Background(), "c", "needle", "", FindAnchor{MessageID: "m400"})
	if err != nil || page.Offset != 400 || page.InitialIndex != 400 || len(page.Occurrences) != 101 || page.PreviousCursor == "" {
		t.Fatalf("anchor=%#v, %v", page, err)
	}
	previous, err := service.FindConversation(context.Background(), "c", "needle", page.PreviousCursor)
	if err != nil || previous.Offset != 200 || len(previous.Occurrences) != 200 {
		t.Fatalf("previous=%#v, %v", previous, err)
	}
	removed, err := service.FindConversation(context.Background(), "c", "needle", "", FindAnchor{MessageID: "removed", Sequence: 900, PreferPrevious: true})
	if err != nil || removed.InitialIndex != 500 || removed.Offset != 400 {
		t.Fatalf("removed=%#v, %v", removed, err)
	}
}

func TestFindLargeCountsStayPagedAndCacheMemoryBounded(t *testing.T) {
	store := &findTestStore{messages: []domain.ConversationMessage{{ID: "m", Sequence: 1, Role: domain.MessageRoleUser, Text: strings.Repeat("x ", 100005)}}}
	service := &Service{store: store}
	page, err := service.FindConversation(context.Background(), "c", "x", "", FindAnchor{MessageID: "m", Offset: 200000})
	if err != nil || page.Total != 100005 || page.Offset != 100000 || page.InitialIndex != 100000 || len(page.Occurrences) != 5 {
		t.Fatalf("page=%#v, %v", page, err)
	}
	bytes := 0
	for _, entry := range service.findCache {
		bytes += entry.bytes
		if len(entry.messages) != 1 {
			t.Fatal("unexpected cached message count")
		}
	}
	if bytes > maxFindCacheBytes {
		t.Fatalf("cache exceeds byte budget: %d", bytes)
	}
}
