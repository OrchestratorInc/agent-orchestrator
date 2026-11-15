package vibe

import "context"

func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return p.vibeBinary(ctx)
}
