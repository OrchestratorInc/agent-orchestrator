package crush

import "context"

func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return p.crushBinary(ctx)
}
