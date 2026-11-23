package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/connectors"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type fakeConnectorStore struct {
	sessions map[domain.SessionID]domain.SessionRecord
	projects []domain.ProjectRecord
	fail     error
}

func (f fakeConnectorStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := f.sessions[id]
	return rec, ok, f.fail
}

func (f fakeConnectorStore) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	return f.projects, nil
}

type fakeConnectorSessions struct {
	servers func(context.Context, sessionmanager.SessionConnectorsInput) []ports.ChatMCPServerConfig
}

func (f *fakeConnectorSessions) SetSessionConnectors(servers func(context.Context, sessionmanager.SessionConnectorsInput) []ports.ChatMCPServerConfig) {
	f.servers = servers
}

func TestWireConnectorsGivesASessionTheConnectorsOfItsProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	svc := connectors.New(t.TempDir(), "http://127.0.0.1:3011")
	ctx := context.Background()
	for _, in := range []connectors.Input{
		{Key: "everywhere", Transport: connectors.TransportHTTP, URL: "https://mcp.example.com/mcp"},
		{Key: "other", Transport: connectors.TransportHTTP, URL: "https://other.example.com/mcp", ProjectID: "p2"},
	} {
		if _, err := svc.Create(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	sessions := &fakeConnectorSessions{}
	wireConnectors(svc, fakeConnectorStore{
		sessions: map[domain.SessionID]domain.SessionRecord{"mer-1": {ProjectID: "p1"}},
		projects: []domain.ProjectRecord{{ID: "p1", Path: "/one"}, {ID: "gone", Path: "/gone", ArchivedAt: time.Now()}},
	}, sessions)
	got := sessions.servers(ctx, sessionmanager.SessionConnectorsInput{SessionID: "mer-1", ProjectID: "p1", Harness: domain.HarnessCodex})
	if len(got) != 1 || got[0].Name != "everywhere" || got[0].URL != "http://127.0.0.1:3011/mcp/s/mer-1/connectors/everywhere" ||
		got[0].Headers["Authorization"] == "" {
		t.Fatalf("servers = %+v", got)
	}
	used, err := svc.SessionConnectors(ctx, "mer-1")
	if err != nil || len(used) != 1 || used[0].Key != "everywhere" {
		t.Fatalf("session connectors = %+v, %v", used, err)
	}
}

func TestASessionReadThatFailsDoesNotCloseTheSessionForItsConnectors(t *testing.T) {
	ctx := context.Background()
	ask := connectors.ProposalInput{SessionID: "mer-1", Name: "docs", Transport: connectors.TransportHTTP, URL: "https://docs.example.com/mcp"}
	busy := connectors.New(t.TempDir(), "http://127.0.0.1:3011")
	sessions := &fakeConnectorSessions{}
	wireConnectors(busy, fakeConnectorStore{fail: errors.New("database is locked")}, sessions)
	if _, err := busy.Create(ctx, connectors.Input{Key: "scoped", Transport: connectors.TransportHTTP, URL: "http://127.0.0.1:1/mcp", ProjectID: "p1"}); err != nil {
		t.Fatal(err)
	}
	entry := sessions.servers(ctx, sessionmanager.SessionConnectorsInput{SessionID: "mer-1", ProjectID: "p1", Harness: domain.HarnessCodex})[0]
	req := httptest.NewRequest(http.MethodPost, entry.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", entry.Headers["Authorization"])
	answer := httptest.NewRecorder()
	busy.ServeProxy(answer, req, "mer-1", "scoped")
	if answer.Code != http.StatusOK || strings.Contains(answer.Body.String(), "has no connector named") || !strings.Contains(answer.Body.String(), "did not answer") {
		t.Fatalf("a call of a session whose read failed = %d %s", answer.Code, answer.Body.String())
	}
	if _, err := busy.Propose(ctx, ask); err == nil {
		t.Fatal("a proposal took the unknown project as its project")
	}
	gone := connectors.New(t.TempDir(), "http://127.0.0.1:3011")
	wireConnectors(gone, fakeConnectorStore{}, &fakeConnectorSessions{})
	if _, err := gone.Propose(ctx, ask); err == nil {
		t.Fatal("a session that does not exist was accepted")
	}
}
