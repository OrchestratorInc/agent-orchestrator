package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/go-chi/chi/v5"
)

// startingAgentStore answers agent tickets with ErrWorkerUnavailable until
// liveAfter probes have run, as the store does while the agent starts.
type startingAgentStore struct {
	Store
	liveAfter int32
	probes    atomic.Int32
	issues    atomic.Int32
}

func (s *startingAgentStore) IssueTerminalTicket(context.Context, domain.Principal, string, string, string, string, time.Duration) (string, []string, error) {
	s.issues.Add(1)
	if s.probes.Load() < s.liveAfter {
		return "", nil, postgres.ErrWorkerUnavailable
	}
	return "ticket", []string{"terminal:read"}, nil
}

func (s *startingAgentStore) AgentTerminalLive(context.Context, domain.Principal, string, string) (bool, error) {
	return s.probes.Add(1) >= s.liveAfter, nil
}

func ticketRequest(t *testing.T, ctx context.Context, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	route := chi.NewRouteContext()
	route.URLParams.Add("orgId", "11111111-1111-1111-1111-111111111111")
	route.URLParams.Add("sessionId", "22222222-2222-2222-2222-222222222222")
	return request.WithContext(context.WithValue(ctx, chi.RouteCtxKey, route))
}

func TestAgentTicketWaitsForTheAgentTerminal(t *testing.T) {
	store := &startingAgentStore{liveAfter: 3}
	response := httptest.NewRecorder()
	New(Options{Store: store}).createTerminalTicket(response, ticketRequest(t, context.Background(), `{"kind":"agent"}`))
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s; want the ticket once the terminal is live", response.Code, response.Body.String())
	}
	// One mint up front, then only cheap probes until the terminal is live.
	if got := store.issues.Load(); got != 2 {
		t.Fatalf("IssueTerminalTicket called %d times, want 2", got)
	}
}

func TestAgentTicketStillAnswers409WhenTheClientLeaves(t *testing.T) {
	store := &startingAgentStore{liveAfter: 1 << 30}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	response := httptest.NewRecorder()
	started := time.Now()
	New(Options{Store: store}).createTerminalTicket(response, ticketRequest(t, ctx, `{"kind":"agent"}`))
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.Code)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("handler held the request %v after the client left", elapsed)
	}
}

func TestWorkspaceTicketDoesNotWait(t *testing.T) {
	store := &startingAgentStore{liveAfter: 1 << 30}
	response := httptest.NewRecorder()
	New(Options{Store: store}).createTerminalTicket(response, ticketRequest(t, context.Background(), `{"kind":"workspace"}`))
	if response.Code != http.StatusConflict || store.probes.Load() != 0 {
		t.Fatalf("status = %d probes = %d; want an immediate 409", response.Code, store.probes.Load())
	}
}
