package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type reportDeliverySession struct {
	sentID      domain.SessionID
	sentMessage string
	sentKey     string
	interrupted domain.SessionID
}

func TestSessionConversationInterruptRoutesByMode(t *testing.T) {
	for _, tc := range []struct {
		mode    domain.SessionMode
		wantTUI bool
	}{
		{domain.SessionModeTUI, true},
		{domain.SessionModeChat, false},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			sessions := &fakeSessionLifecycle{}
			svc := sessionConversationService{
				Service:  chatsvc.New(chatsvc.Options{Sessions: reportDeliveryStore{rec: domain.SessionRecord{ID: "ao-1", Mode: tc.mode}}}),
				sessions: sessions,
			}
			err := svc.Interrupt(context.Background(), "ao-1")
			if tc.wantTUI {
				if err != nil || sessions.interrupted != "ao-1" {
					t.Fatalf("TUI interrupt = %v, routed to %q", err, sessions.interrupted)
				}
			} else if !errors.Is(err, chatsvc.ErrNoController) || sessions.interrupted != "" {
				t.Fatalf("Chat interrupt = %v, TUI routed to %q", err, sessions.interrupted)
			}
		})
	}
}

func TestSessionConversationInterruptReportsTUIRefusals(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{sessionmanager.ErrTerminated, "SESSION_TERMINATED"},
		{sessionmanager.ErrSemanticAcceptanceUnsupported, "SESSION_INTERRUPT_UNAVAILABLE"},
	} {
		sessions := &fakeSessionLifecycle{interruptErr: tc.err}
		svc := sessionConversationService{
			Service:  chatsvc.New(chatsvc.Options{Sessions: reportDeliveryStore{rec: domain.SessionRecord{ID: "ao-1", Mode: domain.SessionModeTUI}}}),
			sessions: sessions,
		}
		err := svc.Interrupt(context.Background(), "ao-1")
		var api *apierr.Error
		if !errors.As(err, &api) || api.Code != tc.code {
			t.Fatalf("TUI refusal = %v, want %s", err, tc.code)
		}
	}
}

func (s *reportDeliverySession) SendSemantic(_ context.Context, id domain.SessionID, message, key string) error {
	s.sentID, s.sentMessage, s.sentKey = id, message, key
	return nil
}

func (s *reportDeliverySession) InterruptTUI(_ context.Context, id domain.SessionID) error {
	s.interrupted = id
	return nil
}

type reportDeliveryStore struct{ rec domain.SessionRecord }

func (s reportDeliveryStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return s.rec, true, nil
}

func TestReportSemanticDeliveryUsesAcceptedTUIBoundary(t *testing.T) {
	sessions := &reportDeliverySession{}
	delivery := reportSemanticDelivery{
		sessions: sessions,
		store: reportDeliveryStore{rec: domain.SessionRecord{
			ID: "ao-1", Mode: domain.SessionModeTUI,
		}},
	}
	if err := delivery.Submit(context.Background(), "ao-1", "report context", "report-batch:abc123"); err != nil {
		t.Fatal(err)
	}
	if sessions.sentID != "ao-1" || sessions.sentMessage != "report context" || sessions.sentKey != "report-batch:abc123" {
		t.Fatalf("semantic send = %+v", sessions)
	}
	if err := delivery.Interrupt(context.Background(), "ao-1"); err != nil {
		t.Fatal(err)
	}
	if sessions.interrupted != "ao-1" {
		t.Fatalf("interrupted = %q", sessions.interrupted)
	}
}
