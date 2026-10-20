package strands

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentRuntimeLaunchEnv = (*Plugin)(nil)

// AugmentRuntimeLaunchEnv fences Strands to the same native home validated by
// command construction and prevents user/project MCP server discovery.
func (p *Plugin) AugmentRuntimeLaunchEnv(env map[string]string, dataDir string, _ domain.SessionID, _ string) {
	p.nativeHomeMu.RLock()
	home := p.nativeHome
	p.nativeHomeMu.RUnlock()
	if home != "" {
		env["HOME"] = home
		env["USERPROFILE"] = home
	}
	env["STRANDS_CLI_MCP_DISCOVERY"] = "off"
	if dataDir != "" {
		env["NODE_OPTIONS"] = withNodePreload(env["NODE_OPTIONS"], nodePreloadPath(dataDir))
	}
}

// withNodePreload adds AO's Strands preload ahead of any NODE_OPTIONS the
// launch already carries, falling back to the daemon's own value.
func withNodePreload(current, path string) string {
	if current == "" {
		current = os.Getenv("NODE_OPTIONS")
	}
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed // Windows drive paths need file:///C:/...
	}
	preload := `--import "` + (&url.URL{Scheme: "file", Path: slashed}).String() + `"`
	if strings.Contains(current, preload) {
		return current
	}
	return strings.TrimSpace(preload + " " + current)
}
