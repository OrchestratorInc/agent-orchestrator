package pr

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type lifecycle interface {
	ApplyPRObservation(ctx context.Context, id domain.SessionID, o ports.PRObservation) error
}

type Manager struct {
	writer    ports.PRWriter
	lifecycle lifecycle
	clock     func() time.Time
}

type Deps struct {
	Writer    ports.PRWriter
	Lifecycle lifecycle
	Clock     func() time.Time
}

func New(d Deps) *Manager {
	m := &Manager{writer: d.Writer, lifecycle: d.Lifecycle, clock: d.Clock}
	if m.clock == nil {
		m.clock = time.Now
	}
	return m
}

func (m *Manager) ApplyObservation(ctx context.Context, id domain.SessionID, o ports.PRObservation) error {
	if !o.Fetched {
		return nil
	}
	if err := m.write(ctx, id, o); err != nil {
		return err
	}
	if m.lifecycle == nil {
		return nil
	}
	return m.lifecycle.ApplyPRObservation(ctx, id, o)
}

func (m *Manager) write(ctx context.Context, id domain.SessionID, o ports.PRObservation) error {
	now := m.clock()
	row := domain.PullRequest{URL: o.URL, SessionID: id, Number: o.Number, Draft: o.Draft, Merged: o.Merged, Closed: o.Closed, CI: o.CI, Review: o.Review, Mergeability: o.Mergeability, UpdatedAt: now}
	checks := make([]domain.PullRequestCheck, len(o.Checks))
	for i, c := range o.Checks {
		checks[i] = domain.PullRequestCheck{Name: c.Name, CommitHash: c.CommitHash, Status: c.Status, URL: c.URL, LogTail: c.LogTail, CreatedAt: now}
	}
	comments := make([]domain.PullRequestComment, len(o.Comments))
	for i, c := range o.Comments {
		comments[i] = domain.PullRequestComment{ID: c.ID, ThreadID: c.ThreadID, ReviewID: c.ReviewID, Author: c.Author, File: c.File, Line: c.Line, Body: c.Body, URL: c.URL, Resolved: c.Resolved, CreatedAt: now, AutoInjectReview: c.AutoInjectReview}
	}
	return m.writer.WritePR(ctx, row, checks, comments)
}
