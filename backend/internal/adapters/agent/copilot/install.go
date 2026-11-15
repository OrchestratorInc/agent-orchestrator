package copilot

import "context"

func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return p.copilotBinary(ctx)
}
