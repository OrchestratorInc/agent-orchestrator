package domain

import "github.com/aoagents/agent-orchestrator/backend/pkg/contract"

type KanbanColumn = contract.KanbanColumn

const (
	KanbanBuilding    = contract.KanbanBuilding
	KanbanValidating  = contract.KanbanValidating
	KanbanNeedsReview = contract.KanbanNeedsReview
	KanbanReady       = contract.KanbanReady
	KanbanArchive     = contract.KanbanArchive
)

type DisplayStatus = contract.DisplayStatus

type KanbanPresentation = contract.KanbanPresentation
