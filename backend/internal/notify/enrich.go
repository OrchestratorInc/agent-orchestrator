package notify

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/approval"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func enrich(intent Intent) (domain.NotificationRecord, error) {
	rec := domain.NotificationRecord{
		SessionID: intent.SessionID,
		ProjectID: intent.ProjectID,
		PRURL:     strings.TrimSpace(intent.PRURL),
		Type:      intent.Type,
		Status:    domain.NotificationUnread,
		CreatedAt: intent.CreatedAt,
		SourceKey: strings.TrimSpace(intent.SourceKey),
	}
	if !intent.Type.Valid() {
		return domain.NotificationRecord{}, domain.ErrInvalidNotificationType
	}
	if intent.Type == domain.NotificationBrowserApproval {
		if rec.SourceKey == "" {
			return domain.NotificationRecord{}, domain.ErrInvalidNotificationRecord
		}
	} else if intent.Type != domain.NotificationNeedsInput && rec.PRURL == "" {
		return domain.NotificationRecord{}, domain.ErrInvalidNotificationRecord
	}
	rec.Title = titleForIntent(intent)
	rec.Body = bodyForIntent(intent)
	if err := rec.Validate(); err != nil {
		return domain.NotificationRecord{}, err
	}
	return rec, nil
}

func titleForIntent(intent Intent) string {
	switch intent.Type {
	case domain.NotificationNeedsInput:
		return fmt.Sprintf("%s needs your input", sessionLabel(intent))
	case domain.NotificationReadyToMerge:
		if title := strings.TrimSpace(intent.PRTitle); title != "" {
			if label := prLabel(intent); label != "PR" {
				return fmt.Sprintf("%s · %s", title, label)
			}
			return title
		}
		return fmt.Sprintf("%s is ready to merge", prLabel(intent))
	case domain.NotificationPRMerged:
		return fmt.Sprintf("%s merged", prLabel(intent))
	case domain.NotificationPRClosedUnmerged:
		return fmt.Sprintf("%s closed", prLabel(intent))
	case domain.NotificationReviewCompleted:
		return fmt.Sprintf("Review complete for %s", prLabel(intent))
	case domain.NotificationReviewChangesRequested:
		return fmt.Sprintf("Review found changes for %s", prLabel(intent))
	case domain.NotificationBrowserApproval:
		return "Approval needed"
	default:
		return "Notification"
	}
}

func bodyForIntent(intent Intent) string {
	switch intent.Type {
	case domain.NotificationNeedsInput:
		return "Your agent is waiting on you to continue."
	case domain.NotificationReadyToMerge:
		if session := sessionLabel(intent); session != "session" {
			return fmt.Sprintf("PR from session %s is ready to merge. CI passed with no blocking review feedback.", session)
		}
		return "CI passed with no blocking review feedback."
	case domain.NotificationPRMerged:
		title := strings.TrimSpace(intent.PRTitle)
		if target := strings.TrimSpace(intent.PRTargetBranch); title != "" && target != "" {
			return fmt.Sprintf("%s is now on %s.", title, target)
		}
		if title != "" {
			return fmt.Sprintf("%s was merged.", title)
		}
		return "The pull request was merged."
	case domain.NotificationPRClosedUnmerged:
		if title := strings.TrimSpace(intent.PRTitle); title != "" {
			return fmt.Sprintf("%s was closed without merging. Reopen it if this wasn't intended.", title)
		}
		return "Closed without merging. Reopen it if this wasn't intended."
	case domain.NotificationReviewCompleted:
		return "Orchestrator finished reviewing this pull request and approved it."
	case domain.NotificationReviewChangesRequested:
		return "Orchestrator finished reviewing this pull request and found changes to address."
	case domain.NotificationBrowserApproval:
		return approvalBody(intent)
	default:
		return ""
	}
}

func sessionLabel(intent Intent) string {
	if v := strings.TrimSpace(intent.SessionDisplayName); v != "" {
		return v
	}
	if intent.SessionID != "" {
		return string(intent.SessionID)
	}
	return "session"
}

func prLabel(intent Intent) string {
	if intent.PRNumber > 0 {
		return fmt.Sprintf("PR #%d", intent.PRNumber)
	}
	if title := strings.TrimSpace(intent.PRTitle); title != "" {
		return "PR " + title
	}
	return "PR"
}

// maxApprovalBody caps an approval banner on a lock screen.
const maxApprovalBody = 140

// approvalVerbs name each approval kind. The banner is built from these, the
// session's name and the host only: never the approval's own summary.
var approvalVerbs = map[string]string{
	approval.CategoryPayment:    "make a payment",
	approval.CategoryMessage:    "send a message",
	approval.CategoryPublish:    "post or publish",
	approval.CategoryDelete:     "delete something",
	approval.CategorySecurity:   "change a security setting",
	approval.CategoryAccount:    "change account details",
	approval.CategorySubmit:     "submit a form",
	approval.CategoryCredential: "use a saved login",
}

// approvalBody reads "<agent> wants to <kind>[ on <host>]" as one plain line.
func approvalBody(intent Intent) string {
	verb, ok := approvalVerbs[strings.TrimSpace(intent.ApprovalCategory)]
	if !ok {
		verb = "run a browser action"
	}
	text := fmt.Sprintf("%s wants to %s", oneLine(sessionLabel(intent)), verb)
	if host := siteHost(intent.ApprovalSite); host != "" {
		text += " on " + host
	}
	if runes := []rune(text); len(runes) > maxApprovalBody {
		text = strings.TrimSpace(string(runes[:maxApprovalBody-1])) + "…"
	}
	return text
}

// oneLine drops line breaks, control and format characters (bidi overrides)
// so untrusted text cannot change how a banner reads.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)), " ")
}

func siteHost(site string) string {
	u, err := url.Parse(strings.TrimSpace(site))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}
