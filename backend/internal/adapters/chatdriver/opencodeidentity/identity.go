package opencodeidentity

import (
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const prefix = "ao-harness:"

func Encode(harness domain.AgentHarness, providerID string) string {
	if providerID == "" {
		return ""
	}
	return prefix + string(harness) + ":" + providerID
}

func Decode(expected domain.AgentHarness, stored string) (string, error) {
	value, tagged := strings.CutPrefix(stored, prefix)
	if !tagged {
		if expected == domain.HarnessOpenCode && stored != "" {
			return stored, nil
		}
		return "", fmt.Errorf("%w: provider conversation has no %s harness identity", ports.ErrChatResumeFailed, expected)
	}
	harness, providerID, found := strings.Cut(value, ":")
	if !found || providerID == "" {
		return "", fmt.Errorf("%w: invalid OpenCode provider conversation identity", ports.ErrChatResumeFailed)
	}
	if domain.AgentHarness(harness) != expected {
		return "", fmt.Errorf("%w: provider conversation belongs to harness %q, not %q", ports.ErrChatResumeFailed, harness, expected)
	}
	return providerID, nil
}
