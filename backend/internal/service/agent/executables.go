package agent

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// ScanExecutables 在当前 daemon 主机只读扫描一个 Harness；不会修改已保存的入口。
func (s *Service) ScanExecutables(ctx context.Context, agentID, directory string) (binaryutil.ExecutableScan, error) {
	if _, ok := s.agent(agentID); !ok {
		return binaryutil.ExecutableScan{}, apierr.Invalid("UNKNOWN_AGENT_ID", "Unknown agent adapter: "+agentID, nil)
	}
	result, err := binaryutil.ScanExecutables(ctx, agentID, directory)
	if err != nil {
		return result, apierr.Invalid("EXECUTABLE_SCAN_FAILED", err.Error(), nil)
	}
	return result, nil
}
