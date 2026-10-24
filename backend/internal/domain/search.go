package domain

import "time"

// SearchMessageChange is one chat message row as the search indexer catches
// up on it. UpdatedAt and ID together form the catch-up cursor.
type SearchMessageChange struct {
	ID        string
	Text      string
	Streaming bool
	UpdatedAt time.Time
}

// SearchMessage is a chat message re-read at query time, with the owning
// conversation's facts the palette shows next to a hit.
type SearchMessage struct {
	ID             string
	ConversationID string
	Text           string
	Streaming      bool
	CreatedAt      time.Time
	// SessionID is the session whose view shows the message: the worker a
	// reviewer chat reviews, for a ReviewID message.
	SessionID         SessionID
	ProjectID         ProjectID
	ReviewID          string
	ConversationTitle string
}
