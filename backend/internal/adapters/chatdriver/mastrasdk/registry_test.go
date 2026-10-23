package mastrasdk_test

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestUnqualifiedMastraSDKCandidateIsNotShipped(t *testing.T) {
	r := registry.Build(nil, nil)
	if r.SupportsChat(domain.HarnessMastraCode) {
		t.Fatal("unqualified SDK bridge is production-registered")
	}
	if _, err := r.Driver(domain.HarnessMastraCode); !errors.Is(err, ports.ErrChatUnsupported) {
		t.Fatal(err)
	}
}
