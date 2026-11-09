package agent

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// verifyExitedTestLogin 在测试完成外部凭据变更后模拟子进程退出，并等待并发自动验证结束。
func verifyExitedTestLogin(t *testing.T, manager *codexAccountManager, operationID string) (domain.CodexAccountLoginOperation, error) {
	t.Helper()
	terminal := manager.terminal.(*fakeCodexLoginTerminal)
	terminal.mu.Lock()
	terminal.childExited = true
	terminal.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		result, err := manager.verifyLogin(context.Background(), operationID)
		terminal.mu.Lock()
		closed := len(terminal.closed) > 0
		terminal.mu.Unlock()
		if err != nil || result.Status != domain.CodexAccountLoginVerifying && (result.Status != domain.CodexAccountLoginCompleted || closed) {
			return result, err
		}
		if time.Now().After(deadline) {
			t.Fatal("native login verification did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}
