package zcode

import "context"

// ResolveBinary resolves the executable path for the plugin (on the reference
// layout, the wrapper at ~/.local/bin/zcode), satisfying
// ports.AgentBinaryResolver so readiness and install checks probe the adapter's
// own resolution instead of shelling out.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return p.zcodeBinary(ctx)
}
