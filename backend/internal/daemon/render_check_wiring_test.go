package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type fakeRenderCheckBroker struct {
	result browserruntime.Result
	err    error
	action string
}

func (f *fakeRenderCheckBroker) Execute(_ context.Context, _ domain.SessionID, action string, _ map[string]interface{}) (browserruntime.Result, error) {
	f.action = action
	return f.result, f.err
}

func TestRenderCheckViaDesktopMapsMissingDesktopToUnavailable(t *testing.T) {
	failed := browserruntime.CommandError{Code: "BROWSER_COMMAND_FAILED", Message: "did not load"}
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"no runtime connected", browserruntime.ErrUnavailable, chatsvc.ErrRenderCheckUnavailable},
		{"no desktop window", browserruntime.CommandError{Code: "BROWSER_TARGET_UNAVAILABLE", Message: "AO window is unavailable"}, chatsvc.ErrRenderCheckUnavailable},
		{"page failed to load", failed, failed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broker := &fakeRenderCheckBroker{err: tc.err}
			_, err := renderCheckViaDesktop(broker)(context.Background(), "proj-1", map[string]any{"url": "u", "width": 720})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if broker.action != "__render-check" {
				t.Fatalf("action = %q, want __render-check", broker.action)
			}
		})
	}
}

func TestRenderCheckViaDesktopReturnsTheDesktopResult(t *testing.T) {
	broker := &fakeRenderCheckBroker{result: browserruntime.Result{Value: map[string]any{"data": "iVBORw0KGgo="}}}
	got, err := renderCheckViaDesktop(broker)(context.Background(), "proj-1", nil)
	if err != nil || got.(map[string]any)["data"] != "iVBORw0KGgo=" {
		t.Fatalf("got=%v err=%v", got, err)
	}
}
