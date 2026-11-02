package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// Exercise the generated reads directly: bounding the final snapshot alone does
// not prevent the store from materializing a whole chunk of large payloads.
func TestConversationPageSQLByteBudget(t *testing.T) {
	for _, tc := range []struct{ table, column string }{
		{"conversation_messages", "text"},
		{"conversation_messages", "delivery_content_json"},
		{"conversation_activities", "summary"},
		{"conversation_activities", "detail_json"},
		{"conversation_activities", "command_output"},
		{"conversation_activities", "streamed_text"},
	} {
		t.Run(tc.column, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			s := sqlitetest.MustOpenAt(t, dir)
			seedProject(t, s, "budget")
			rec := sampleRecord("budget")
			rec.Mode = domain.SessionModeChat
			session, err := s.CreateSession(ctx, rec)
			if err != nil {
				t.Fatal(err)
			}
			conv, err := s.CreateConversation(ctx, "budget", domain.ConversationScopeSession, "budget", session.ID, histClock)
			if err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "ao.db"))+"?_pragma=busy_timeout(5000)")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			// 24 characters, but 72 UTF-8 bytes. Each row fits 100 bytes; two do not.
			payload := strings.Repeat("界", 24)
			for i := 1; i <= 4; i++ {
				var query string
				if tc.table == "conversation_messages" {
					query = "INSERT INTO conversation_messages (id,conversation_id,sequence,role,origin,created_at,updated_at," + tc.column + ") VALUES (?,?,?,'assistant','provider',?,?,?)"
				} else {
					query = "INSERT INTO conversation_activities (id,conversation_id,sequence,kind,status,created_at,updated_at," + tc.column + ") VALUES (?,?,?,'command','completed',?,?,?)"
				}
				if _, err := db.ExecContext(ctx, query, fmt.Sprintf("row-%d", i), conv.ID, i, histClock, histClock, payload); err != nil {
					t.Fatal(err)
				}
			}
			q := gen.New(db)
			for _, budget := range []int64{100, 1} { // An oversized first row must still advance the cursor.
				for before := int64(5); before > 1; before-- {
					var sequences []int64
					if tc.table == "conversation_messages" {
						rows, err := q.SelectConversationMessagesPage(ctx, gen.SelectConversationMessagesPageParams{ConversationID: conv.ID, BeforeSequence: before, PageLimit: 200, PageBytes: budget})
						if err != nil {
							t.Fatal(err)
						}
						for _, row := range rows {
							sequences = append(sequences, row.Sequence)
						}
					} else {
						rows, err := q.SelectConversationActivitiesPage(ctx, gen.SelectConversationActivitiesPageParams{ConversationID: conv.ID, BeforeSequence: before, PageLimit: 200, PageBytes: budget})
						if err != nil {
							t.Fatal(err)
						}
						for _, row := range rows {
							sequences = append(sequences, row.Sequence)
						}
					}
					if len(sequences) != 1 || sequences[0] != before-1 {
						t.Fatalf("budget=%d before=%d: sequences=%v, want [%d]", budget, before, sequences, before-1)
					}
				}
			}
		})
	}
}

func TestConversationSnapshotPagesAcrossByteBoundedFetches(t *testing.T) {
	for _, size := range []int{3 << 20, 5 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s, session, conversation := conversationFixture(t)
			ctx := context.Background()
			text := strings.Repeat("x", size)
			for i := 1; i <= 4; i++ {
				id := fmt.Sprintf("large-%d", i)
				created, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", domain.ConversationMessage{ID: id, Text: text, Origin: domain.MessageOriginHuman}, "turn-"+id, histClock.Add(time.Duration(i)*time.Second))
				if err != nil || !created {
					t.Fatalf("append: created=%v err=%v", created, err)
				}
			}
			before := int64(0)
			for want := 4; want >= 1; want-- {
				page, err := s.LoadConversationSnapshotPage(ctx, conversation, before, 200)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Messages) != 1 || page.Messages[0].ID != fmt.Sprintf("large-%d", want) || page.Messages[0].Text != text {
					t.Fatalf("page %d: got %d messages; expected complete large-%d", want, len(page.Messages), want)
				}
				if page.HasMoreBefore != (want > 1) {
					t.Fatalf("page %d hasMore=%v", want, page.HasMoreBefore)
				}
				if before != 0 && page.OldestSequence >= before {
					t.Fatal("cursor did not advance")
				}
				before = page.OldestSequence
			}
		})
	}
}
