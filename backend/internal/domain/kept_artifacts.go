package domain

import "time"

// KeptArtifactSession is a deleted session whose artifacts the person chose to
// keep. Its files stay in the session's artifact directory, view only, until
// they are deleted one by one.
type KeptArtifactSession struct {
	SessionID SessionID
	// ProjectID is the project the session was in. Empty for a session in no
	// project, and once that project is removed.
	ProjectID ProjectID
	// Title is the session's title when it was deleted.
	Title  string
	KeptAt time.Time
}
