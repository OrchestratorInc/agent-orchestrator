package ports

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// CodexCapacityObservation is the normalized rate-limit data emitted by a
// live Codex conversation. It is used for the conversation quota indicator;
// account selection and switching are handled by the managed provider bridge.
type CodexCapacityObservation struct {
	Plan              *string
	Overall           *domain.CodexCapacityBucket
	AdditionalBuckets []domain.CodexCapacityBucket
	ResetCredits      *domain.CodexResetCreditsSummary
	ObservedAt        time.Time
	Partial           bool
}
