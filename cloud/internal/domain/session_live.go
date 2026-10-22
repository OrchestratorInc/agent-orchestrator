package domain

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

// SessionLiveState is the ephemeral, non-durable state a shared session's
// participants see change in real time. It is never part of the event log.
type SessionLiveState struct {
	Viewers []SessionViewer
}
