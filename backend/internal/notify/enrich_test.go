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
		ApprovalAction:     `Click "Place order" on shop.example`,
		ApprovalSite:       "https://shop.example",
		CreatedAt:          time.Date(2026, 11, 23, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich approval: %v", err)
	}
	if want := "Approval needed"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
	if want := `Checkout flow wants to click "Place order" on shop.example`; rec.Body != want {
		t.Fatalf("body = %q, want %q", rec.Body, want)
	}
	if rec.SourceKey != "browser_approval:apr_1" || rec.PRURL != "" {
		t.Fatalf("record = %+v", rec)
	}
}

func TestEnrichBrowserApprovalNamesSiteAndCapsUntrustedText(t *testing.T) {
	t.Parallel()

	long := "Click \"" + strings.Repeat("x", 300) + "\"\n‮evil"
	rec, err := enrich(Intent{
		Type: domain.NotificationBrowserApproval, SessionID: "sess-1", SourceKey: "browser_approval:apr_1",
		ApprovalAction: long, ApprovalSite: "https://shop.example", CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("enrich approval: %v", err)
	}
	if n := utf8.RuneCountInString(rec.Body); n > 140 {
		t.Fatalf("body is %d runes, want at most 140: %q", n, rec.Body)
	}
	if strings.ContainsAny(rec.Body, "\n‮") || !strings.HasSuffix(rec.Body, "…") {
		t.Fatalf("body = %q, want one line without format characters, truncated", rec.Body)
	}

	short, err := enrich(Intent{
		Type: domain.NotificationBrowserApproval, SessionID: "sess-1", SourceKey: "browser_approval:apr_2",
		ApprovalAction: "Press Enter", ApprovalSite: "https://mail.example:8443", CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("enrich approval: %v", err)
	}
	if want := "sess-1 wants to press Enter on mail.example:8443"; short.Body != want {
		t.Fatalf("body = %q, want %q", short.Body, want)
	}
}

func TestEnrichBrowserApprovalNeedsSourceKey(t *testing.T) {
	t.Parallel()

	if _, err := enrich(Intent{Type: domain.NotificationBrowserApproval, SessionID: "sess-1", ApprovalAction: "Click", CreatedAt: time.Now()}); err == nil {
		t.Fatal("an approval notification without its approval id must be refused")
	}
}
