package session

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// Only a link Windows refused for want of Developer Mode names that setting;
// any other unshared folder gets a message that holds on every platform.
func TestMapSessionErrorNamesDeveloperModeOnlyWhenWindowsNeedsIt(t *testing.T) {
	var apiErr *apierr.Error

	devMode := fmt.Errorf("%w: link projects: %w: %w", ports.ErrAgentAccountUnshared, ports.ErrAgentAccountNeedsDeveloperMode, errors.New("privilege not held"))
	if !errors.As(mapSessionError(devMode), &apiErr) {
		t.Fatalf("mapSessionError(developer mode) is not an API error")
	}
	if apiErr.Code != "AGENT_ACCOUNT_UNSHARED" || !strings.Contains(apiErr.Message, "Windows Developer Mode") || apiErr.Details["windowsDeveloperMode"] != true {
		t.Fatalf("developer mode error = %+v", apiErr)
	}

	other := fmt.Errorf("%w: link projects: permission denied", ports.ErrAgentAccountUnshared)
	if !errors.As(mapSessionError(other), &apiErr) {
		t.Fatalf("mapSessionError(unshared) is not an API error")
	}
	if apiErr.Code != "AGENT_ACCOUNT_UNSHARED" || strings.Contains(apiErr.Message, "Developer Mode") || apiErr.Details["windowsDeveloperMode"] != nil {
		t.Fatalf("unshared error = %+v", apiErr)
	}
}
