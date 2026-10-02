package session

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type retryCapabilityCommander struct {
	*controlCommander
	available bool
	observed  domain.AccountsManagerSwitch
}

func (c *retryCapabilityCommander) AccountsManagerSwitchCanRetry(op domain.AccountsManagerSwitch) bool {
	c.observed = op
	return c.available
}

func TestAccountsManagerControlRetryCapability(t *testing.T) {
	svc, store, manager := controlServiceFixture()
	if svc.AccountSwitchCanRetry(store.op) {
		t.Fatal("unsupported manager advertised retry")
	}
	reader := &retryCapabilityCommander{controlCommander: manager}
	svc.manager = reader
	for _, available := range []bool{false, true} {
		reader.available = available
		if got := svc.AccountSwitchCanRetry(store.op); got != available || reader.observed != store.op || len(manager.calls) != 0 {
			t.Fatal("service changed observed operation or performed a mutation")
		}
	}
}
