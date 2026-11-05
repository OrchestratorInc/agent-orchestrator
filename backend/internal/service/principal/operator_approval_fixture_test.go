package principal_test

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// approvalFixture is a store with a controllable clock.
type approvalFixture struct {
	store   *principal.ApprovalStore
	now     time.Time
	tick    func(time.Duration)
	token   string
	trust   principal.ApprovalOperatorTrust
	factory int
}

func newApprovalFixture(t *testing.T) *approvalFixture {
	t.Helper()
	f := &approvalFixture{
		now:   time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC),
		token: strings.Repeat("a", 64),
	}
	f.store = principal.NewApprovalStore(principal.ApprovalStoreOptions{
		TTL:   10 * time.Second,
		Clock: func() time.Time { return f.now },
		NewID: func() string {
			f.factory++
			return "approval-" + string(rune('a'+f.factory-1))
		},
		NewCode: func() string {
			return "CODE" + string(rune('A'+f.factory-1))
		},
	})
	f.trust = principal.ApprovalOperatorTrust{Approvals: f.store}
	f.tick = func(d time.Duration) { f.now = f.now.Add(d) }
	return f
}

const approvalBindPath = "/api/v1/workflows/w1/attempts/a1/bind"

// approvalRequest builds a request carrying an approval header and the binding the handler would
// have recorded for it.
func approvalRequest(approvalID, method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:54321"
	if approvalID != "" {
		req.Header.Set(principal.HeaderOperatorApproval, approvalID)
	}
	return principal.WithApprovalBinding(req, method, path, principal.PayloadDigest([]byte(body)))
}
