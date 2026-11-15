package domain

import "time"

type SessionInterfaceTransitionPolicy string

const (
	SessionInterfaceTransitionDrain     SessionInterfaceTransitionPolicy = "drain"
	SessionInterfaceTransitionInterrupt SessionInterfaceTransitionPolicy = "interrupt"
)

func (p SessionInterfaceTransitionPolicy) Valid() bool {
	return p == SessionInterfaceTransitionDrain || p == SessionInterfaceTransitionInterrupt
}

type SessionInterfaceTransitionHistoryPolicy string

const (
	SessionInterfaceTransitionHistoryStrict   SessionInterfaceTransitionHistoryPolicy = "strict"
	SessionInterfaceTransitionHistoryProvider SessionInterfaceTransitionHistoryPolicy = "provider_history"
)

func (p SessionInterfaceTransitionHistoryPolicy) Valid() bool {
	return p == SessionInterfaceTransitionHistoryStrict || p == SessionInterfaceTransitionHistoryProvider
}

type SessionInterfaceTransitionPhase string

const (
	SessionInterfaceTransitionRequested      SessionInterfaceTransitionPhase = "requested"
	SessionInterfaceTransitionPreflighting   SessionInterfaceTransitionPhase = "preflighting"
	SessionInterfaceTransitionDraining       SessionInterfaceTransitionPhase = "draining"
	SessionInterfaceTransitionSourceStopping SessionInterfaceTransitionPhase = "source_stopping"
	SessionInterfaceTransitionSourceStopped  SessionInterfaceTransitionPhase = "source_stopped"
	SessionInterfaceTransitionTargetStarting SessionInterfaceTransitionPhase = "target_starting"
	SessionInterfaceTransitionActivating     SessionInterfaceTransitionPhase = "activating"
	SessionInterfaceTransitionCompleted      SessionInterfaceTransitionPhase = "completed"
	SessionInterfaceTransitionFailed         SessionInterfaceTransitionPhase = "failed"
	SessionInterfaceTransitionCancelled      SessionInterfaceTransitionPhase = "cancelled"
	SessionInterfaceTransitionRecovery       SessionInterfaceTransitionPhase = "recovery_required"
)

func (p SessionInterfaceTransitionPhase) Terminal() bool {
	switch p {
	case SessionInterfaceTransitionCompleted,
		SessionInterfaceTransitionFailed,
		SessionInterfaceTransitionCancelled,
		SessionInterfaceTransitionRecovery:
		return true
	default:
		return false
	}
}

const SessionInterfaceTransitionDegradedIdentityReset = "identity-reset"

type SessionInterfaceTransition struct {
	ID                   string                                  `json:"id"`
	SessionID            SessionID                               `json:"sessionId"`
	SourceMode           SessionMode                             `json:"sourceMode" enum:"chat,tui"`
	TargetMode           SessionMode                             `json:"targetMode" enum:"chat,tui"`
	Policy               SessionInterfaceTransitionPolicy        `json:"policy" enum:"drain,interrupt"`
	HistoryPolicy        SessionInterfaceTransitionHistoryPolicy `json:"historyPolicy" enum:"strict,provider_history"`
	Phase                SessionInterfaceTransitionPhase         `json:"phase" enum:"requested,preflighting,draining,source_stopping,source_stopped,target_starting,activating,completed,failed,cancelled,recovery_required"`
	NativeConversationID string                                  `json:"nativeConversationId,omitempty"`
	SourceAccountID      string                                  `json:"sourceAccountId,omitempty"`
	TargetAccountID      string                                  `json:"targetAccountId,omitempty"`
	DegradedKind         string                                  `json:"degradedKind,omitempty"`
	ErrorCode            string                                  `json:"errorCode,omitempty"`
	ErrorDetail          string                                  `json:"errorDetail,omitempty"`
	CreatedAt            time.Time                               `json:"createdAt"`
	UpdatedAt            time.Time                               `json:"updatedAt"`
	CompletedAt          time.Time                               `json:"completedAt,omitempty"`
	NoticeAcknowledgedAt time.Time                               `json:"noticeAcknowledgedAt,omitempty"`
}

func (t SessionInterfaceTransition) Active() bool { return !t.Phase.Terminal() }

func (t SessionInterfaceTransition) AccountMove() bool { return t.TargetAccountID != "" }

func (t SessionInterfaceTransition) AccountMoveAwaitsTarget() bool {
	if !t.AccountMove() {
		return false
	}
	switch t.Phase {
	case SessionInterfaceTransitionSourceStopped,
		SessionInterfaceTransitionTargetStarting,
		SessionInterfaceTransitionActivating:
		return true
	default:
		return false
	}
}

type SessionInterfaceTransitionMessage struct {
	SenderSessionID string    `json:"senderSessionId,omitempty"`
	AuthoredByUser  bool      `json:"authoredByUser,omitempty"`
	ID              int64     `json:"id"`
	TransitionID    string    `json:"transitionId"`
	ClientMessageID string    `json:"clientMessageId"`
	Message         string    `json:"message"`
	CreatedAt       time.Time `json:"createdAt"`
	DeliveredAt     time.Time `json:"deliveredAt,omitempty"`
}
