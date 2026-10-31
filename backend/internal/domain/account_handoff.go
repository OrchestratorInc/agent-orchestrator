package domain

import (
	"errors"
	"time"
)

// ChatAccountHandoffPhase is a step of an automatic usage-limit account move.
type ChatAccountHandoffPhase string

// The saga runs selecting -> moving -> moved -> retried. The other ends keep
// the session on its account: stayed applies the usage-limit fallback,
// rolled_back records a move that did not commit, skipped records a turn the
// handoff no longer applies to.
const (
	ChatAccountHandoffSelecting  ChatAccountHandoffPhase = "selecting"
	ChatAccountHandoffMoving     ChatAccountHandoffPhase = "moving"
	ChatAccountHandoffMoved      ChatAccountHandoffPhase = "moved"
	ChatAccountHandoffRetried    ChatAccountHandoffPhase = "retried"
	ChatAccountHandoffStayed     ChatAccountHandoffPhase = "stayed"
	ChatAccountHandoffRolledBack ChatAccountHandoffPhase = "rolled_back"
	ChatAccountHandoffSkipped    ChatAccountHandoffPhase = "skipped"
)

// ChatAccountHandoff is the durable record of one refused turn's handoff.
type ChatAccountHandoff struct {
	SourceTurnID    string
	SessionID       SessionID
	ConversationID  string
	SourceAccountID string
	TargetAccountID string
	TransitionID    string
	Phase           ChatAccountHandoffPhase
	Detail          string
	// SourceResetsAt is when the refused account's limit resets, if known; the
	// session does not move back to it before then.
	SourceResetsAt *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ErrChatAccountHandoffConsumed refuses a handoff retry whose one-time consent
// was already used or withdrawn, or whose session left the target account.
var ErrChatAccountHandoffConsumed = errors.New("the account handoff no longer authorizes a retry")
