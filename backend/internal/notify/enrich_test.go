package notify

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestEnrichReadyToMergePrioritizesPRContext(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:               domain.NotificationReadyToMerge,
		SessionID:          "sess-1",
		ProjectID:          "proj-1",
		PRURL:              "https://github.com/acme/app/pull/67",
		SessionDisplayName: "Checkout flow",
		PRNumber:           67,
		PRTitle:            "Fix checkout totals",
		CreatedAt:          time.Date(2026, 8, 5, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich ready notification: %v", err)
	}

	if want := "Fix checkout totals · PR #67"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
	if want := "PR from session Checkout flow is ready to merge. CI passed with no blocking review feedback."; rec.Body != want {
		t.Fatalf("body = %q, want %q", rec.Body, want)
	}
}

func TestEnrichReadyToMergeFallsBackWithoutPRTitle(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:      domain.NotificationReadyToMerge,
		SessionID: "sess-1",
		ProjectID: "proj-1",
		PRURL:     "https://github.com/acme/app/pull/67",
		PRNumber:  67,
		CreatedAt: time.Date(2026, 8, 5, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich ready notification: %v", err)
	}

	if want := "PR #67 is ready to merge"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
}

func TestEnrichBrowserApproval(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:               domain.NotificationBrowserApproval,
		SessionID:          "sess-1",
		SessionDisplayName: "Checkout flow",
		SourceKey:          "browser_approval:apr_1",
		ApprovalCategory:   "payment",
		ApprovalSite:       "https://shop.example",
		CreatedAt:          time.Date(2026, 11, 23, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich approval: %v", err)
	}
	if want := "Approval needed"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
	if want := "Checkout flow wants to make a payment on shop.example"; rec.Body != want {
		t.Fatalf("body = %q, want %q", rec.Body, want)
	}
	if rec.SourceKey != "browser_approval:apr_1" || rec.PRURL != "" {
		t.Fatalf("record = %+v", rec)
	}
}

// The push reaches a lock screen, so it names only the kind of action and
// the host: never the agent's summary, which can carry typed text.
func TestEnrichBrowserApprovalCarriesNoAgentText(t *testing.T) {
	t.Parallel()

	for _, category := range []string{"payment", "message", "publish", "delete", "security", "account", "submit", "credential", "script", "unknown-kind", ""} {
		rec, err := enrich(Intent{
			Type: domain.NotificationBrowserApproval, SessionID: "sess-1", SourceKey: "browser_approval:apr_1",
			ApprovalCategory: category, ApprovalSite: "https://mail.example:8443/inbox?q=hunter2", CreatedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("enrich %q: %v", category, err)
		}
		if !strings.HasPrefix(rec.Body, "sess-1 wants to ") || !strings.HasSuffix(rec.Body, " on mail.example:8443") {
			t.Fatalf("category %q body = %q", category, rec.Body)
		}
		if strings.Contains(rec.Title+rec.Body, "hunter2") || utf8.RuneCountInString(rec.Body) > 140 {
			t.Fatalf("category %q leaked or overflowed: %q / %q", category, rec.Title, rec.Body)
		}
	}
}

func TestEnrichBrowserApprovalNeedsSourceKey(t *testing.T) {
	t.Parallel()

	if _, err := enrich(Intent{Type: domain.NotificationBrowserApproval, SessionID: "sess-1", ApprovalCategory: "payment", CreatedAt: time.Now()}); err == nil {
		t.Fatal("an approval notification without its approval id must be refused")
	}
}
