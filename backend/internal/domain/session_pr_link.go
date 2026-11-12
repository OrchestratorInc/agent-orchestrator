package domain

import (
	"errors"
	"time"
)

// SessionPRRelation is how a session relates to a pull request it does not
// own. A related PR is shown on the session for navigation only: it never
// feeds status, CI or review routing, merge automation, or lifecycle, and it
// never changes which session owns the PR.
type SessionPRRelation string

const (
	// SessionPRReviewing marks a PR the session reviewed or is reviewing.
	SessionPRReviewing SessionPRRelation = "reviewing"
	// SessionPRContributing marks a PR the session pushed commits to without
	// owning it, such as another contributor's PR.
	SessionPRContributing SessionPRRelation = "contributing"
)

// Valid reports whether r is a known relation.
func (r SessionPRRelation) Valid() bool {
	return r == SessionPRReviewing || r == SessionPRContributing
}

// MaxSessionPRLinks caps the related PRs one session shows.
const MaxSessionPRLinks = 20

// ErrSessionPRLinkLimit is returned when a new link would exceed
// MaxSessionPRLinks.
var ErrSessionPRLinkLimit = errors.New("session pr link limit reached")

// SessionPRLink is one related, non-owned PR on a session. Ref.URL is its
// identity within the session. Title, State and Author are what the provider
// said when the link was made.
type SessionPRLink struct {
	SessionID SessionID
	Ref       ChangeRequestReference
	Relation  SessionPRRelation
	Title     string
	State     PRState
	Author    string
	CreatedAt time.Time
}
