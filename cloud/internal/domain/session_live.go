package domain

import "time"

// SessionViewer is one person who has a session open right now.
type SessionViewer struct {
	UserID string
	// Name is the person's display name, never an email address, since a
	// shared session shows it to people outside the organization.
	Name string
	// Role is "member" for an organization member, otherwise the share
	// grant's "editor" or "viewer".
	Role string
}

// ComposerParticipant is a person named by the composer lease.
type ComposerParticipant struct {
	UserID string
	Name   string
}

// ComposerLease is a shared session's hard lock on talking to its agent:
// only the holder may send or steer.
type ComposerLease struct {
	// Holder is nil while nobody holds the lease.
	Holder *ComposerParticipant
	// Epoch increases with every grant. A send or renewal must present the
	// epoch it was granted.
	Epoch int64
	// ExpiresIn is how long the holder's grant has left, zero when free.
	ExpiresIn time.Duration
	// Waitlist is everyone waiting for the lease, first in line first.
	Waitlist []ComposerParticipant
	// Draft is what the holder is typing, nil while they have typed nothing.
	Draft *ComposerDraft
}

// ComposerDraft is the lease holder's draft as the other participants see it.
type ComposerDraft struct {
	Text string
	// Cursor is the caret's offset into Text, as the writing client counts it.
	Cursor int
}

// SessionLiveState is the ephemeral, non-durable state a shared session's
// participants see change in real time. It is never part of the event log.
type SessionLiveState struct {
	Viewers  []SessionViewer
	Composer ComposerLease
	// CanTakeOver reports whether the person reading the state may take the
	// composer lease from its holder.
	CanTakeOver bool
}
