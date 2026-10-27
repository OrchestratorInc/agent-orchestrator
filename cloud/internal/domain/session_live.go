package domain

import "time"

// SessionViewer is one person who has a session open right now.
type SessionViewer struct {
	UserID string
	// Name is the person's display name, never an email address, since a
	// shared session shows it to people outside the organization.
	Name string
	// AvatarURL is the profile photo from the sign-in provider, empty if absent.
	AvatarURL string
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
	// ChatSettings is what the session's turns run with, set by its owner.
	ChatSettings ChatTurnSettings
	// CanConfigure reports whether the person reading the state may change
	// ChatSettings: the session's owner may, everyone else only sees them.
	CanConfigure bool
	// MergeRequestsVersion changes whenever a merge request in the session's
	// project is asked for or decided, so a client re-reads them at once.
	// Empty while the project has none.
	MergeRequestsVersion string
}

// SessionParticipant is one person in a shared session as the board shows
// them: its creator and everyone a share link let in, whether or not they have
// it open.
type SessionParticipant struct {
	UserID string
	// Name is the display name, never an email address.
	Name string
	// AvatarURL is the profile photo from the sign-in provider, empty if absent.
	AvatarURL string
	// Role is "owner" for the session's creator, "member" for another member
	// of its organization who has it open, else the share grant's "editor" or
	// "viewer".
	Role string
	// Present reports a live stream open on the session now.
	Present bool
	// State is "working" while they hold the composer, "waiting" while they
	// are in line for it, otherwise "idle".
	State string
}
