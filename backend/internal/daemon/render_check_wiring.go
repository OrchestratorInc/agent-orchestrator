package daemon

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// renderCheckBroker is the slice of the browser-runtime broker a render check needs.
type renderCheckBroker interface {
	Execute(ctx context.Context, id domain.SessionID, action string, args map[string]interface{}) (browserruntime.Result, error)
}

// renderCheckViaDesktop sends a render check to the desktop app. "__render-check"
// is internal to this wiring: it is not in the service/browser allowlist, so
// `ao browser` cannot send it. No desktop app, or no window for it to load the
// page in, means the check is unavailable rather than failed.
func renderCheckViaDesktop(broker renderCheckBroker) chatsvc.RenderCheck {
	return func(ctx context.Context, id domain.SessionID, args map[string]any) (any, error) {
		result, err := broker.Execute(ctx, id, "__render-check", args)
		var commandErr browserruntime.CommandError
		if errors.Is(err, browserruntime.ErrUnavailable) ||
			(errors.As(err, &commandErr) && commandErr.Code == "BROWSER_TARGET_UNAVAILABLE") {
			return nil, chatsvc.ErrRenderCheckUnavailable
		}
		return result.Value, err
	}
}
