package qwen

import "context"

func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return p.qwenBinary(ctx)
}
