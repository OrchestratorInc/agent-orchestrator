package agentaccount

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// claudeConnectorPrefix starts every claude.ai connector `claude mcp list`
// prints, ahead of the user's own servers (for example "claude.ai Gmail").
const claudeConnectorPrefix = "claude.ai "

// claudeConnectorsTimeout bounds one `claude mcp list`. It checks every
// server's health, so it is far slower than `auth status`.
const claudeConnectorsTimeout = 90 * time.Second

// Connectors lists the claude.ai connectors home's login loads, read from
// `claude mcp list` with the same environment its sessions launch with.
func (c *Claude) Connectors(ctx context.Context, home string) ([]domain.AgentConnector, error) {
	bin, err := c.Binary(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, claudeConnectorsTimeout)
	defer cancel()
	out, err := c.command(ctx, bin, home, "mcp", "list").Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// The command lists what it found even when one server fails.
		if connectors := parseClaudeMCPList(out); len(connectors) > 0 {
			return connectors, nil
		}
		return nil, err
	}
	return parseClaudeMCPList(out), nil
}

// parseClaudeMCPList reads the claude.ai connectors out of `claude mcp list`.
// Each server is one line, "<name>: <target> - <status>"; a claude.ai
// connector's name starts with "claude.ai ". Other lines (the health-check
// banner, the user's own servers) are skipped.
func parseClaudeMCPList(out []byte) []domain.AgentConnector {
	var connectors []domain.AgentConnector
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, claudeConnectorPrefix) {
			continue
		}
		head, status, found := cutLast(line, " - ")
		if !found {
			continue
		}
		name, _, _ := strings.Cut(head, ": ")
		name = strings.TrimSpace(strings.TrimPrefix(name, claudeConnectorPrefix))
		if name == "" {
			continue
		}
		connectors = append(connectors, domain.AgentConnector{Name: name, Status: claudeConnectorStatus(status)})
	}
	return connectors
}

func claudeConnectorStatus(text string) domain.AgentConnectorStatus {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "needs auth"):
		return domain.AgentConnectorNeedsAuth
	case strings.Contains(lower, "failed") || strings.Contains(text, "✗"):
		return domain.AgentConnectorFailed
	case strings.Contains(lower, "connected"):
		return domain.AgentConnectorConnected
	default:
		return domain.AgentConnectorUnknown
	}
}

// cutLast is strings.Cut at the last sep, so a " - " inside a name or URL
// stays with the head.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}
