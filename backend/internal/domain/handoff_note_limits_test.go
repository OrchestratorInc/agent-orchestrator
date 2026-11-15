package domain

import "github.com/aoagents/agent-orchestrator/backend/pkg/handoffnote"

const (
	handoffLatestTurnBudget = 10_000
	handoffArtifactLimit    = 50
	handoffChangedFileLimit = 50
	handoffNoteMaxBytes     = handoffnote.MaxBytes
)
