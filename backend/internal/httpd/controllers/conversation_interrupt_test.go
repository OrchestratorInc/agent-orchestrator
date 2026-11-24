package controllers_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	chatsvc "github.com/OrchestratorInc/agent-orchestrator/backend/internal/service/chat"
)

// targetedInterruptService records which Stop path the route took.
type targetedInterruptService struct {
	*fakeConversationService
	untargeted   int
	targetedTurn string
	targetedErr  error
}

func (s *targetedInterruptService) Interrupt(context.Context, domain.SessionID) error {
	s.untargeted++
	return nil
}

func (s *targetedInterruptService) InterruptTurn(_ context.Context, _ domain.SessionID, turnID string) error {
	s.targetedTurn = turnID
	return s.targetedErr
}

func TestInterruptRouteTargetsTheNamedTurn(t *testing.T) {
	svc := &targetedInterruptService{fakeConversationService: &fakeConversationService{}}
	rec := serveConversations(svc, httptest.NewRequest(http.MethodPost,
		"/sessions/p1-1/conversation/interrupt", bytes.NewBufferString(`{"turnId":" turn-7 "}`)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if svc.targetedTurn != "turn-7" || svc.untargeted != 0 {
		t.Fatalf("targeted=%q untargeted=%d, want turn-7 and 0", svc.targetedTurn, svc.untargeted)
	}
}

// Older clients send no body; their Stop keeps stopping the in-flight turn.
func TestInterruptRouteWithoutABodyStopsTheInFlightTurn(t *testing.T) {
	for _, body := range []string{"", "{}"} {
		svc := &targetedInterruptService{fakeConversationService: &fakeConversationService{}}
		rec := serveConversations(svc, httptest.NewRequest(http.MethodPost,
			"/sessions/p1-1/conversation/interrupt", bytes.NewBufferString(body)))
		if rec.Code != http.StatusNoContent || svc.untargeted != 1 || svc.targetedTurn != "" {
			t.Fatalf("body %q: status=%d untargeted=%d targeted=%q", body, rec.Code, svc.untargeted, svc.targetedTurn)
		}
	}
}

func TestInterruptRouteReportsAFinishedTargetAsNoActiveTurn(t *testing.T) {
	svc := &targetedInterruptService{
		fakeConversationService: &fakeConversationService{},
		targetedErr:             chatsvc.ErrNoActiveTurn,
	}
	rec := serveConversations(svc, httptest.NewRequest(http.MethodPost,
		"/sessions/p1-1/conversation/interrupt", bytes.NewBufferString(`{"turnId":"turn-7"}`)))
	if rec.Code != http.StatusConflict || !bytes.Contains(rec.Body.Bytes(), []byte("CHAT_NO_ACTIVE_TURN")) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestInterruptRouteRejectsMalformedBody(t *testing.T) {
	svc := &targetedInterruptService{fakeConversationService: &fakeConversationService{}}
	rec := serveConversations(svc, httptest.NewRequest(http.MethodPost,
		"/sessions/p1-1/conversation/interrupt", bytes.NewBufferString(`{"turnId":`)))
	if rec.Code != http.StatusBadRequest || svc.untargeted != 0 || svc.targetedTurn != "" {
		t.Fatalf("status=%d untargeted=%d targeted=%q", rec.Code, svc.untargeted, svc.targetedTurn)
	}
}
