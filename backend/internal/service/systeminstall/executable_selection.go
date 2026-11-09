package systeminstall

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// BeginExecutableChange 在保存与缓存发布期间排除安装和启动；已排队的更新须先取消。
// 不停止现有会话，也不让等待空闲的旧安装计划在切换后重新生效。
func (s *Service) BeginExecutableChange(ctx context.Context, harness domain.AgentHarness) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target := Target(harness)
	gate := s.harnessGate(harness)
	if !gate.TryLock() {
		return nil, fmt.Errorf("%s is starting or being updated; retry after it finishes", harness)
	}
	s.mu.Lock()
	job := s.jobs[target]
	if job != nil && activeStatus(job.Status) {
		s.mu.Unlock()
		gate.Unlock()
		return nil, fmt.Errorf("%w: cancel any queued update or wait for it to finish before selecting a CLI", ErrInstallActive)
	}
	if s.changing == nil {
		s.changing = make(map[Target]bool)
	}
	s.changing[target] = true
	s.mu.Unlock()
	return func() {
		s.setHarnessChanging(target, false)
		gate.Unlock()
	}, nil
}
