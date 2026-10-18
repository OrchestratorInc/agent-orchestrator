package domain

import (
	"errors"
	"time"
)

// MaxInstructionsBytes bounds one set of standing instructions. Every new
// session's system prompt carries them, so they must stay short.
const MaxInstructionsBytes = 8 << 10

// InstructionsRevision is one saved version of a set of standing
// instructions: the user's own (empty ProjectID), which AO adds to every
// session, or one project's, which AO adds to that project's sessions.
type InstructionsRevision struct {
	ID        int64
	ProjectID ProjectID
	Body      string
	// SessionID is the AO session that saved this revision; nil when the user
	// saved it.
	SessionID *SessionID
	// Dreamed reports that a dreaming session saved this revision.
	Dreamed bool
	// UserLines are the lines of Body the user typed or edited, in this
	// revision or an earlier one. Dreaming must keep them word for word; it
	// may rewrite every other line.
	UserLines []string
	CreatedAt time.Time
}

// SessionCommand is one shell command an agent ran in a chat session, without
// its output. Dreaming learns verified project facts from these, such as which
// test command passes.
type SessionCommand struct {
	Command string
	Failed  bool
}

// ErrInstructionsChanged reports a save that named a revision that is no
// longer the latest.
var ErrInstructionsChanged = errors.New("instructions changed since that revision")
