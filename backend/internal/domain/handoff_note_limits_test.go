package domain

import "github.com/aoagents/agent-orchestrator/backend/pkg/handoffnote"

// The builder's limits live in handoffnote. The builder tests here check the
// local note against them, so they are named here with handoffnote's values.
const (
	handoffLatestTurnBudget = 10_000
	handoffArtifactLimit    = 50
	handoffChangedFileLimit = 50
	handoffNoteMaxBytes     = handoffnote.MaxBytes
)
