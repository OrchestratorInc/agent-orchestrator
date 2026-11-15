package opencode

import "context"

func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return ResolveBinaryForMajor(ctx, 1)
}
