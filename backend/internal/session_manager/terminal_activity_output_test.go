package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type plainActivityDetector struct{}

func (plainActivityDetector) DetectTerminalActivity(string) (domain.ActivityState, bool) {
	return domain.ActivityIdle, true
}

type renderedActivityDetector struct{ plainActivityDetector }

func (renderedActivityDetector) RequiresRenderedActivity() bool { return true }

type styledUnavailableRuntime struct{ *fakeRuntime }

func (styledUnavailableRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return "", ports.ErrStyledTerminalOutputUnavailable
}

// Hermes tells its dim placeholder from a typed draft only by styling, so the
// drain and message delivery must hand it the rendered screen the activity
// observer already uses.
func TestTerminalActivityOutputFollowsDetectorRenderingNeed(t *testing.T) {
	mgr, _, rt := newChatManager(&recordingLauncher{})
	handle := ports.RuntimeHandle{ID: "mer-1"}
	ctx := context.Background()
	for _, tt := range []struct {
		name     string
		runtime  runtimeController
		detector ports.TerminalActivityDetector
		want     string
	}{
		{"rendered detector", &styledScreenRuntime{fakeRuntime: rt, screen: "styled"}, renderedActivityDetector{}, "styled"},
		{"plain detector", &styledScreenRuntime{fakeRuntime: rt, screen: "styled"}, plainActivityDetector{}, "plain"},
		{"host without a rendered screen", styledUnavailableRuntime{fakeRuntime: rt}, renderedActivityDetector{}, "plain"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rt.outputs = []string{"plain"}
			mgr.runtime = tt.runtime
			got, err := mgr.terminalActivityOutput(ctx, tt.detector, handle, 40)
			if err != nil || got != tt.want {
				t.Fatalf("terminalActivityOutput = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
