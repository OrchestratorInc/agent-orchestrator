package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const reportDeliveryPrefix = `<ao-report-delivery id="`

func WrapReportDelivery(id, message string) string {
	return fmt.Sprintf("%s%s\">\n%s\n</ao-report-delivery>", reportDeliveryPrefix, id, message)
}

func ReportDeliveryID(message string) (string, bool) {
	return deliveryID(message, reportDeliveryPrefix)
}

func deliveryID(message, prefix string) (string, bool) {
	message = strings.TrimSpace(message)
	if !strings.HasPrefix(message, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(message, prefix)
	end := strings.Index(rest, `">`)
	if end <= 0 || end > 128 {
		return "", false
	}
	id := rest[:end]
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
			(r < '0' || r > '9') && r != ':' && r != '-' && r != '_' {
			return "", false
		}
	}
	return id, true
}

const (
	MaxReportTextCharacters = 1000
	ReportBatchFallback     = time.Hour
	ReportSettlementWindow  = 5 * time.Minute
)

type ReportState string

const (
	ReportCheckpoint ReportState = "checkpoint"
	ReportNeedsInput ReportState = "needs_input"
	ReportStuck      ReportState = "stuck"
	ReportDone       ReportState = "done"
)

func (s ReportState) Valid() bool {
	switch s {
	case ReportCheckpoint, ReportNeedsInput, ReportStuck, ReportDone:
		return true
	default:
		return false
	}
}

type ReportOutputKind string

const (
	ReportOutputArtifact   ReportOutputKind = "artifact"
	ReportOutputPRCreated  ReportOutputKind = "pr_created"
	ReportOutputPRReviewed ReportOutputKind = "pr_reviewed"
)

func (k ReportOutputKind) Valid() bool {
	switch k {
	case ReportOutputArtifact, ReportOutputPRCreated, ReportOutputPRReviewed:
		return true
	default:
		return false
	}
}

type ReportOutput struct {
	Kind      ReportOutputKind
	Reference string
	Label     string
}

type ReportDeliveryState string

const (
	ReportPending      ReportDeliveryState = "pending"
	ReportClaimed      ReportDeliveryState = "claimed"
	ReportAcknowledged ReportDeliveryState = "acknowledged"
)

type ReportRecord struct {
	ID                 string
	SessionID          SessionID
	ProjectID          ProjectID
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

var ErrInvalidReport = errors.New("invalid report")

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

func WrapSessionDelivery(id, message string) string {
	return fmt.Sprintf("<ao-session-delivery id=%q>\n%s\n</ao-session-delivery>", id, message)
}

func CoordinationDeliveryID(message string) (string, bool) {
	if id, ok := ReportDeliveryID(message); ok {
		return id, true
	}
	return deliveryID(message, `<ao-session-delivery id="`)
}
