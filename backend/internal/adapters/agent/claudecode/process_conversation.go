package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentProcessConversationReader = (*Plugin)(nil)

// ProcessConversationID reads the entry Claude Code keeps for each running
// interactive process at <config>/sessions/<pid>.json.
func (p *Plugin) ProcessConversationID(ctx context.Context, pid int) (string, bool, error) {
	dir, err := p.NativeSessionConfigDir(ctx, nil)
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "sessions", strconv.Itoa(pid)+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("claude-code: read session of process %d: %w", pid, err)
	}
	var entry struct {
		PID       int    `json:"pid"`
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(data, &entry) != nil || entry.PID != pid || !isUUID(entry.SessionID) {
		return "", false, nil //nolint:nilerr // an unparsable or foreign entry names no conversation
	}
	return entry.SessionID, true, nil
}
