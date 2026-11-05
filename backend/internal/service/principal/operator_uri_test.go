package principal_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// TestOperatorApprovalBindsFullQuery 拒绝路径歧义，且无法将已批准的 query 换成另一个读取范围。
func TestOperatorApprovalBindsFullQuery(t *testing.T) {
	path := "/api/v1/workflows/w1/messages?limit=10&offset=0"
	if !principal.IsOperatorOperation(http.MethodGet, path) {
		t.Fatal("approved read route is unavailable")
	}
	for _, invalid := range []string{
		"https://example.com" + path, path + "#ignored", "/api/v1/workflows/../messages",
		"/api/v1/workflows/w1%2Fother/messages", "/api/v1/workflows/w1%5cother/messages",
		"/api/v1/workflows/w1/attempts/a1/bind/../interventions",
	} {
		if principal.IsOperatorOperation(http.MethodGet, invalid) || principal.IsOperatorOperation(http.MethodPost, invalid) {
			t.Fatalf("ambiguous operator URI accepted: %s", invalid)
		}
	}
	f := newApprovalFixture(t)
	digest := principal.PayloadDigest(nil)
	a, err := f.store.CreateWithPayload(http.MethodGet, path, digest, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.VerifyAndConfirm(context.Background(), a.ID, a.Code, digest,
		consentCheck(func(context.Context, principal.Approval) error { return nil })); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Consume(a.ID, http.MethodGet, "/api/v1/workflows/w1/messages?limit=100&offset=0", digest); err == nil {
		t.Fatal("approved query was replaced")
	}
	if _, err := f.store.Consume(a.ID, http.MethodGet, path, digest); err != nil {
		t.Fatal(err)
	}
}
