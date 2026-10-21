package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	reportDeliveryTag  = "ao-report-delivery"
	sessionDeliveryTag = "ao-session-delivery"
	// deliveryCheckLen is the number of hex digits in an envelope's check.
	deliveryCheckLen = 16
)

// ErrReportDeliveryUnacknowledged means AO wrote a delivery into an agent's
// terminal but the agent never confirmed receiving it whole. A retry puts
// another full copy in front of the agent, so callers should wait longer
// between such retries (#6931).
var ErrReportDeliveryUnacknowledged = errors.New("delivery was written but not acknowledged")

// WrapReportDelivery marks an AO-authored report batch so provider prompt
// hooks can correlate semantic acceptance with its durable delivery identity.
func WrapReportDelivery(id, message string) string {
	return wrapDelivery(reportDeliveryTag, id, message)
}

// ReportDeliveryID returns the durable identity from an AO report envelope.
// Delivery ids are generated internally and deliberately restricted so a
// malformed prompt cannot manufacture markup or an unbounded correlation key.
func ReportDeliveryID(message string) (string, bool) {
	return deliveryID(message, reportDeliveryTag)
}

// wrapDelivery adds a check of the body to the envelope. Terminals can drop,
// reorder, or insert text while a long prompt is typed, and other text (such
// as the user's unsent draft) can end up in front of the envelope. The check
// lets the hook accept an envelope that is not at the start, but only when its
// body arrived whole.
//
// The check sits on its own line after an unchanged opening tag, so a hook
// from an older AO (for example, an agent still running from the installed app
// while a newer daemon is in use) reads the envelope exactly as before.
func wrapDelivery(tag, id, message string) string {
	return fmt.Sprintf("<%s id=%q>\n%s%s\"/>\n%s\n</%s>", tag, id, deliveryCheckOpen, deliveryCheck(message), message, tag)
}

const deliveryCheckOpen = `<ao-delivery-check sum="`

// deliveryCheck hashes the body with whitespace removed, so harmless changes
// (dropped blank lines, CRLF line endings) still match while lost, reordered,
// or inserted text does not.
func deliveryCheck(body string) string {
	sum := sha256.Sum256([]byte(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, body)))
	return hex.EncodeToString(sum[:])[:deliveryCheckLen]
}

func deliveryID(message, tag string) (string, bool) {
	message = strings.TrimSpace(message)
	open := "<" + tag + ` id="`
	for from := 0; ; {
		at := strings.Index(message[from:], open)
		if at < 0 {
			return "", false
		}
		at += from
		if id, ok := envelopeAt(message, at, open, "</"+tag+">"); ok {
			return id, true
		}
		from = at + len(open)
	}
}

// envelopeAt reads the envelope that opens at message[at:]. AO pastes a
// checked envelope last, so it must end the prompt and its body must match
// its check; text in front of it (such as the user's unsent draft) is allowed,
// while an envelope quoted in the middle of a human prompt is not. An
// envelope without a check (written before it existed) cannot prove its body
// is whole, so it keeps the old rule: trusted only at the start.
func envelopeAt(message string, at int, open, closeTag string) (string, bool) {
	rest := message[at+len(open):]
	end := strings.IndexByte(rest, '"')
	if end <= 0 || end > 128 || !validDeliveryID(rest[:end]) {
		return "", false
	}
	id, rest := rest[:end], rest[end:]
	if !strings.HasPrefix(rest, `">`) {
		return "", false
	}
	body := strings.TrimLeft(rest[len(`">`):], " \t\r\n")
	if !strings.HasPrefix(body, deliveryCheckOpen) {
		if at != 0 {
			return "", false
		}
		return id, true
	}
	body = body[len(deliveryCheckOpen):]
	if len(body) < deliveryCheckLen+3 || body[deliveryCheckLen:deliveryCheckLen+3] != `"/>` {
		return "", false
	}
	check, content := body[:deliveryCheckLen], body[deliveryCheckLen+3:]
	if !strings.HasSuffix(content, closeTag) || deliveryCheck(strings.TrimSuffix(content, closeTag)) != check {
		return "", false
	}
	return id, true
}

func validDeliveryID(id string) bool {
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
			(r < '0' || r > '9') && r != ':' && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

const (
	// MaxReportTextCharacters is the hard limit for free-form messages and structured notes.
	MaxReportTextCharacters = 1000
	// ReportBatchFallback is the fixed informational-report fallback deadline.
	ReportBatchFallback = time.Hour
	// ReportSettlementWindow is the fixed window opened by a done report.
	ReportSettlementWindow = 5 * time.Minute
)

// ReportState is an optional worker-originated coordination claim.
type ReportState string

// Supported report states.
const (
	ReportCheckpoint ReportState = "checkpoint"
	ReportNeedsInput ReportState = "needs_input"
	ReportStuck      ReportState = "stuck"
	ReportDone       ReportState = "done"
)

// Valid reports whether s is a supported structured state.
func (s ReportState) Valid() bool {
	switch s {
	case ReportCheckpoint, ReportNeedsInput, ReportStuck, ReportDone:
		return true
	default:
		return false
	}
}

// ReportOutputKind identifies a durable output reference attached to a report.
type ReportOutputKind string

// Supported report output kinds.
const (
	ReportOutputArtifact   ReportOutputKind = "artifact"
	ReportOutputPRCreated  ReportOutputKind = "pr_created"
	ReportOutputPRReviewed ReportOutputKind = "pr_reviewed"
)

// Valid reports whether k is a supported output kind.
func (k ReportOutputKind) Valid() bool {
	switch k {
	case ReportOutputArtifact, ReportOutputPRCreated, ReportOutputPRReviewed:
		return true
	default:
		return false
	}
}

// ReportOutput is an ordered reference to work produced by a worker.
type ReportOutput struct {
	Kind      ReportOutputKind
	Reference string
	Label     string
}

// ReportDeliveryState is the durable outbox lifecycle of a report.
type ReportDeliveryState string

// Report delivery states.
const (
	ReportPending      ReportDeliveryState = "pending"
	ReportClaimed      ReportDeliveryState = "claimed"
	ReportAcknowledged ReportDeliveryState = "acknowledged"
)

// ReportRecord is the durable report and outbox persistence shape. State and
// outputs are independent, and neither changes authoritative session state.
type ReportRecord struct {
	ID        string
	SessionID SessionID
	ProjectID ProjectID
	// SessionDisplayName is resolved at delivery time for presentation. It is
	// intentionally not persisted with the durable report row.
	SessionDisplayName string
	State              ReportState
	Note               string
	Message            string
	Outputs            []ReportOutput
	CreatedAt          time.Time
	DeliveryState      ReportDeliveryState
	AvailableAt        time.Time
	SettlementDeadline time.Time
	RepeatCount        int64
	ClaimToken         string
	ClaimedAt          time.Time
	DeliveryAttempts   int64
	AcknowledgedAt     time.Time
	LastError          string
	DeliveryBatchID    string
}

// ErrInvalidReport reports an invalid report or delivery transition input.
var ErrInvalidReport = errors.New("invalid report")

// Validate checks content, ownership, output, and delivery-state invariants.
func (r ReportRecord) Validate() error {
	if r.ID == "" || r.SessionID == "" || r.ProjectID == "" || r.CreatedAt.IsZero() || r.AvailableAt.IsZero() || r.RepeatCount < 1 {
		return ErrInvalidReport
	}
	if err := ValidateReportContent(r.State, r.Note, r.Message, r.Outputs); err != nil {
		return err
	}
	if r.State == ReportDone {
		if r.SettlementDeadline.IsZero() {
			return ErrInvalidReport
		}
	} else if !r.SettlementDeadline.IsZero() {
		return ErrInvalidReport
	}
	switch r.DeliveryState {
	case ReportPending:
		if r.ClaimToken != "" || !r.ClaimedAt.IsZero() || !r.AcknowledgedAt.IsZero() {
			return ErrInvalidReport
		}
	case ReportClaimed:
		if r.ClaimToken == "" || r.ClaimedAt.IsZero() || !r.AcknowledgedAt.IsZero() {
			return ErrInvalidReport
		}
	case ReportAcknowledged:
		if r.ClaimToken == "" || r.ClaimedAt.IsZero() || r.AcknowledgedAt.IsZero() {
			return ErrInvalidReport
		}
	default:
		return ErrInvalidReport
	}
	if r.DeliveryAttempts < 0 {
		return ErrInvalidReport
	}
	return nil
}

// ValidateReportContent checks the authoritative report payload contract.
func ValidateReportContent(state ReportState, note, message string, outputs []ReportOutput) error {
	if message != "" {
		if state != "" || note != "" || len(outputs) != 0 || strings.TrimSpace(message) == "" || utf8.RuneCountInString(message) > MaxReportTextCharacters {
			return ErrInvalidReport
		}
		return nil
	}
	if state == "" {
		if note != "" || len(outputs) == 0 {
			return ErrInvalidReport
		}
	} else if !state.Valid() || strings.TrimSpace(note) == "" || utf8.RuneCountInString(note) > MaxReportTextCharacters {
		return ErrInvalidReport
	}
	for _, output := range outputs {
		if !output.Kind.Valid() || strings.TrimSpace(output.Reference) == "" {
			return ErrInvalidReport
		}
		if output.Kind == ReportOutputPRCreated || output.Kind == ReportOutputPRReviewed {
			if _, err := ParseChangeRequestURL(output.Reference); err != nil {
				return ErrInvalidReport
			}
		}
	}
	return nil
}

// WrapSessionDelivery preserves AO session-origin direction as coordination in
// terminal prompt hooks, without labeling it as a human prompt or worker report.
func WrapSessionDelivery(id, message string) string {
	return wrapDelivery(sessionDeliveryTag, id, message)
}

// CoordinationDeliveryID recognizes both durable reports and session sends.
func CoordinationDeliveryID(message string) (string, bool) {
	if id, ok := ReportDeliveryID(message); ok {
		return id, true
	}
	return deliveryID(message, sessionDeliveryTag)
}
