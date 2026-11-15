package session

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

type stackInfo struct {
	Blocked       bool
	BottomOfStack bool
}

func buildStacks(prs []domain.PRFacts) map[string]stackInfo {
	positions := contract.BuildStacks(toContractPRFacts(prs))
	out := make(map[string]stackInfo, len(prs))
	for url, position := range positions {
		out[url] = stackInfo{
			Blocked:       position.Blocked,
			BottomOfStack: position.BottomOfStack,
		}
	}
	return out
}
