package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Break caught: a write behind a long writer kept waiting after its caller was
// gone, so request handlers hung until the writer finished.
func TestStoreWritesStopWaitingWhenTheirContextEnds(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	writes := []struct {
		name  string
		write func(context.Context) error
	}{
		{"session create", func(ctx context.Context) error {
			_, err := s.CreateSession(ctx, sampleRecord("mer"))
			return err
		}},
		{"project upsert", func(ctx context.Context) error {
			return s.UpsertProject(ctx, domain.ProjectRecord{ID: "late", Path: "/tmp/late", RegisteredAt: now})
		}},
		{"app setting", func(ctx context.Context) error {
			return s.SetDefaultSessionMode(ctx, domain.SessionModeTUI, now)
		}},
		// conversationWriter path.
		{"adopt provider turn", func(ctx context.Context) error {
			return s.AdoptProviderTurn(ctx, "conv", "mer-1", "gen", "turn", "provider-turn", now)
		}},
		// Provider event projection holds the writer for its whole transaction.
		{"project provider event", func(ctx context.Context) error {
			_, err := s.ProjectProviderEvent(ctx, "conv", "mer-1", "gen", "event", "method", "{}", now,
				func(context.Context) error { return nil })
			return err
		}},
		// Named results.
		{"cleanup owned controller work", func(ctx context.Context) error {
			_, err := s.CleanupOwnedControllerWork(ctx, "mer-1", "conv", "gen", now)
			return err
		}},
		{"repair incomplete edit", func(ctx context.Context) error {
			_, _, err := s.RepairIncompleteConversationEdit(ctx, "mer-1", "conv", now)
			return err
		}},
		// Generic helper.
		{"reserve edit delivery", func(ctx context.Context) error {
			_, _, err := s.ReserveEditDelivery(ctx, "conv", "msg", "{}", now)
			return err
		}},
	}

	for _, tc := range writes {
		t.Run(tc.name, func(t *testing.T) {
			seqBefore, err := s.LatestSeq(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			release := s.HoldWriter()
			released := false
			defer func() {
				if !released {
					release()
				}
			}()

			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() { result <- tc.write(ctx) }()
			time.Sleep(50 * time.Millisecond) // let the write queue behind the writer
			cancel()

			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("write error = %v, want context canceled", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("write kept waiting for the writer after its context was cancelled")
			}

			release()
			released = true
			seqAfter, err := s.LatestSeq(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if seqAfter != seqBefore {
				t.Fatalf("change_log advanced %d -> %d: a write ran after its caller was gone", seqBefore, seqAfter)
			}
		})
	}

	sessions, err := s.ListSessions(context.Background(), "mer")
	if err != nil || len(sessions) != 0 {
		t.Fatalf("sessions after cancelled writes = %d, err=%v, want none", len(sessions), err)
	}
	if _, found, err := s.GetProject(context.Background(), "late"); err != nil || found {
		t.Fatalf("project written after its caller was gone: found=%v err=%v", found, err)
	}
}

// Break caught: a deadline that expires behind the writer must stop the wait
// the same way a cancel does, and a live context must still complete its write.
func TestStoreWriteHonoursDeadlineBehindWriterAndStillCompletesWhenFree(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "mer")

	release := s.HoldWriter()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := s.CreateSession(ctx, sampleRecord("mer"))
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("CreateSession error = %v, want deadline exceeded", err)
		}
	case <-time.After(2 * time.Second):
		release()
		t.Fatal("CreateSession kept waiting for the writer after its 50ms deadline")
	}
	release()

	created, err := s.CreateSession(context.Background(), sampleRecord("mer"))
	if err != nil || created.ID == "" {
		t.Fatalf("CreateSession with a live context: id=%q err=%v", created.ID, err)
	}
}
