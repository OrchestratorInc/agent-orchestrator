package copilot

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentInterfaceHandoff             = (*Plugin)(nil)
	_ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
)

func (p *Plugin) NativeConversationID(
	ctx context.Context,
	session ports.SessionRef,
	currentMode domain.SessionMode,
	providerConversationID string,
) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if currentMode == domain.SessionModeChat {
		id := strings.TrimSpace(providerConversationID)
		return id, id != "", nil
	}
	id := strings.TrimSpace(session.Metadata[ports.MetadataKeyAgentSessionID])
	return id, id != "", nil
}

func (p *Plugin) NativeConversationExists(
	ctx context.Context,
	_ ports.SessionRef,
	nativeConversationID string,
	env map[string]string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	parsed, err := uuid.Parse(strings.TrimSpace(nativeConversationID))
	if err != nil {
		return false, nil
	}
	home := strings.TrimSpace(env["COPILOT_HOME"])
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return false, fmt.Errorf("copilot: resolve session-state root: %w", err)
		}
		home = copilotHomeDir(userHome)
	}
	events := filepath.Join(home, "session-state", parsed.String(), "events.jsonl")
	file, err := os.Open(events) //nolint:gosec // path built from a validated UUID
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("copilot: open %s: %w", events, err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), `"type":"user.message"`) {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("copilot: read %s: %w", events, err)
	}
	return false, nil
}
