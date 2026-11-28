package agentaccounts

import (
	"context"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// connectorsTTL is how long a connector list is reused. Reading one runs the
// provider CLI's health check of every server, which takes seconds, and the
// list only changes when the user adds or signs in to a connector on the
// provider's site; "Check again" reads it at once.
const connectorsTTL = 10 * time.Minute

// connectorLister is a provider whose accounts carry connectors of their own
// provider account (Claude: claude.ai connectors).
type connectorLister interface {
	Connectors(ctx context.Context, home string) ([]domain.AgentConnector, error)
}

// connectorCache holds the last connector reading per account. One mutex per
// account serializes reads, so repeated "Check again" clicks or two open
// settings windows never start the CLI twice at once.
type connectorCache struct {
	mu       sync.Mutex
	readings map[string]domain.AgentConnectorsReading
	inflight map[string]*sync.Mutex
}

func (c *connectorCache) lock(id string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight == nil {
		c.inflight = map[string]*sync.Mutex{}
	}
	m := c.inflight[id]
	if m == nil {
		m = &sync.Mutex{}
		c.inflight[id] = m
	}
	return m
}

func (c *connectorCache) get(id string) (domain.AgentConnectorsReading, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	reading, ok := c.readings[id]
	return reading, ok
}

func (c *connectorCache) put(reading domain.AgentConnectorsReading) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readings == nil {
		c.readings = map[string]domain.AgentConnectorsReading{}
	}
	c.readings[reading.AccountID] = reading
}

// Connectors lists the connectors the account's sessions load, reusing a
// reading younger than connectorsTTL unless refresh is set. An account that
// cannot load connectors at all (a setup token, an API key, a provider with
// none) says why instead, without running anything.
func (s *Service) Connectors(ctx context.Context, accountID string, refresh bool) (domain.AgentConnectorsReading, error) {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return domain.AgentConnectorsReading{}, err
	}
	reading := domain.AgentConnectorsReading{AccountID: account.ID}
	lister, ok := s.providers[account.Provider].(connectorLister)
	switch {
	case !ok:
		reading.Unavailable = domain.AgentConnectorsUnsupported
		return reading, nil
	case account.APIKey():
		reading.Unavailable = domain.AgentConnectorsAPIKey
		return reading, nil
	case account.SetupToken():
		reading.Unavailable = domain.AgentConnectorsSetupToken
		return reading, nil
	}

	requested := s.now()
	inflight := s.connectors.lock(account.ID)
	inflight.Lock()
	defer inflight.Unlock()
	if cached, ok := s.connectors.get(account.ID); ok {
		// A refresh reuses only a read that finished after it was asked
		// for, one it waited on behind the lock: that read is the "check
		// again" it wanted. Any older reading may predate a change.
		if (!refresh && s.now().Sub(cached.CheckedAt) < connectorsTTL) || (refresh && cached.CheckedAt.After(requested)) {
			return cached, nil
		}
	}
	home := account.Home
	if account.Device() {
		home = ""
	}
	connectors, err := lister.Connectors(ctx, home)
	reading.CheckedAt = s.now()
	if err != nil {
		if ctx.Err() != nil {
			return domain.AgentConnectorsReading{}, ctx.Err()
		}
		reading.Error = err.Error()
	}
	reading.Connectors = connectors
	s.connectors.put(reading)
	return reading, nil
}
