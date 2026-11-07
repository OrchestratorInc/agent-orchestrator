package domain

// SessionPreviewRecord contains the durable facts needed to watch an active
// worker's preview without loading its prompts, checkpoints, or agent config.
type SessionPreviewRecord struct {
	ID              SessionID
	WorkspacePath   string
	PreviewURL      string
	PreviewRevision int64
}
