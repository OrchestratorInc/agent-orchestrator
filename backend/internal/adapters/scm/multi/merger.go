package multi

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type NamedMerger struct {
	Key    string
	Merger ports.SCMMerger
}

type Merger struct {
	byName map[string]ports.SCMMerger
}

func NewMerger(mergers ...NamedMerger) *Merger {
	m := &Merger{
		byName: make(map[string]ports.SCMMerger, len(mergers)),
	}
	for _, nm := range mergers {
		m.byName[nm.Key] = nm.Merger
	}
	return m
}

func (m *Merger) resolve(key string) (ports.SCMMerger, error) {
	if p, ok := m.byName[key]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("scm multi merger: unknown provider %q", key)
}

func (m *Merger) MergePullRequest(ctx context.Context, request ports.SCMMergeRequest) (ports.SCMMergeResult, error) {
	p, err := m.resolve(request.PR.Repo.Provider)
	if err != nil {
		return ports.SCMMergeResult{}, err
	}
	return p.MergePullRequest(ctx, request)
}
