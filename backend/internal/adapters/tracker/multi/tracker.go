package multi

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var ErrUnknownProvider = errors.New("tracker multi: unknown provider")

type NamedTracker struct {
	Key     string
	Tracker ports.Tracker
}

type Tracker struct {
	byName map[string]ports.Tracker
}

func New(trackers ...NamedTracker) *Tracker {
	m := &Tracker{
		byName: make(map[string]ports.Tracker, len(trackers)),
	}
	for _, nt := range trackers {
		m.byName[nt.Key] = nt.Tracker
	}
	return m
}

func (m *Tracker) resolve(key string) (ports.Tracker, error) {
	if t, ok := m.byName[key]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("%w: provider=%q", ErrUnknownProvider, key)
}

func (m *Tracker) Get(ctx context.Context, id domain.TrackerID) (domain.Issue, error) {
	t, err := m.resolve(string(id.Provider))
	if err != nil {
		return domain.Issue{}, err
	}
	return t.Get(ctx, id)
}

func (m *Tracker) List(ctx context.Context, repo domain.TrackerRepo, filter domain.ListFilter) ([]domain.Issue, error) {
	t, err := m.resolve(string(repo.Provider))
	if err != nil {
		return nil, err
	}
	return t.List(ctx, repo, filter)
}

func (m *Tracker) Preflight(ctx context.Context) error {
	var firstErr error
	for _, t := range m.byName {
		if err := t.Preflight(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

var _ ports.Tracker = (*Tracker)(nil)
