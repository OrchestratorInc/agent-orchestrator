package httpapi

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

var allTicketScopes = []string{
	"worker:connect",
	"worker:event",
	"worker:turn:claim",
	"worker:turn:poll",
	"worker:turn:complete",
	"worker:credential:read",
	"worker:git",
	"worker:orchestrate",
	"worker:report",
	"worker:transport",
	"worker:notification",
}

// The heartbeat decoder ignores fields this control plane does not know, so a
// worker newer than it (after a rollback) keeps its session alive; every other
// request stays strict.
func TestHeartbeatDecodingToleratesUnknownFields(t *testing.T) {
	body := `{"version":"v","capabilities":["pty"],"busy":true,"addedLater":1}`
	var heartbeat worker.HeartbeatRequest
	request := httptest.NewRequest(http.MethodPost, "/worker/heartbeat", strings.NewReader(body))
	if err := decodeJSONTolerant(httptest.NewRecorder(), request, &heartbeat); err != nil {
		t.Fatalf("tolerant decode: %v", err)
	}
	if heartbeat.Version != "v" || !heartbeat.Busy {
		t.Fatalf("heartbeat = %+v", heartbeat)
	}
	request = httptest.NewRequest(http.MethodPost, "/worker/heartbeat", strings.NewReader(body))
	if err := decodeJSON(httptest.NewRecorder(), request, &heartbeat); err == nil {
		t.Fatal("strict decode accepted an unknown field")
	}
	request = httptest.NewRequest(http.MethodPost, "/worker/heartbeat", strings.NewReader(`{"version":"v"}{}`))
	if err := decodeJSONTolerant(httptest.NewRecorder(), request, &heartbeat); err == nil {
		t.Fatal("tolerant decode accepted trailing data")
	}
}

func TestIssuedWorkerScopes(t *testing.T) {
	cases := []struct {
		name   string
		launch domain.WorkerLaunch
		keeps  []string
		strips []string
	}{
		{
			name:   "orchestrator keeps orchestrate, loses report",
			launch: domain.WorkerLaunch{Kind: "orchestrator"},
			keeps:  []string{"worker:orchestrate", "worker:connect"},
			strips: []string{"worker:report"},
		},
		{
			name:   "child worker keeps report, loses orchestrate",
			launch: domain.WorkerLaunch{Kind: "worker", ParentSessionID: "22222222-2222-4222-8222-222222222222"},
			keeps:  []string{"worker:report", "worker:transport"},
			strips: []string{"worker:orchestrate"},
		},
		{
			name:   "top-level worker loses both",
			launch: domain.WorkerLaunch{Kind: "worker"},
			keeps:  []string{"worker:connect", "worker:git", "worker:notification"},
			strips: []string{"worker:orchestrate", "worker:report"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issued := issuedWorkerScopes(allTicketScopes, tc.launch)
			for _, scope := range tc.keeps {
				if !slices.Contains(issued, scope) {
					t.Fatalf("scope %q was stripped: %v", scope, issued)
				}
			}
			for _, scope := range tc.strips {
				if slices.Contains(issued, scope) {
					t.Fatalf("scope %q survived the strip: %v", scope, issued)
				}
			}
		})
	}
}

func TestIssuedWorkerScopesDoesNotMutateTicket(t *testing.T) {
	before := slices.Clone(allTicketScopes)
	_ = issuedWorkerScopes(allTicketScopes, domain.WorkerLaunch{Kind: "worker"})
	if !slices.Equal(before, allTicketScopes) {
		t.Fatal("issuedWorkerScopes mutated the ticket's scope slice")
	}
}
