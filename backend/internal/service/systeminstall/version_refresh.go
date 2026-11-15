package systeminstall

import (
	"context"
	"io"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type HarnessUpdateObserver interface {
	MarkHarnessUpdated(ctx context.Context, harness domain.AgentHarness, version string, at time.Time) error
	CountStaleBinarySessions(ctx context.Context) (map[domain.AgentHarness]int64, error)
}

const harnessUpdateObserverTimeout = 5 * time.Second

func (s *Service) SetHarnessUpdateObserver(observer HarnessUpdateObserver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateObserver = observer
}

func (s *Service) noteHarnessUpdated(target Target, version string) {
	s.mu.Lock()
	observer := s.updateObserver
	s.mu.Unlock()
	if observer == nil || version == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.backgroundContext), harnessUpdateObserverTimeout)
	defer cancel()
	_ = observer.MarkHarnessUpdated(ctx, domain.AgentHarness(target), version, time.Now().UTC())
}

func (s *Service) deferHomebrewCleanup(target Target, formula string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deferredCleanups == nil {
		s.deferredCleanups = make(map[Target]string)
	}
	s.deferredCleanups[target] = formula
}

func (s *Service) RunDeferredCleanups(ctx context.Context) {
	s.mu.Lock()
	pending := make(map[Target]string, len(s.deferredCleanups))
	for target, formula := range s.deferredCleanups {
		pending[target] = formula
	}
	observer := s.updateObserver
	s.mu.Unlock()
	if len(pending) == 0 || observer == nil {
		return
	}
	processes, err := s.snapshotManagedProcesses(ctx)
	if err != nil {
		return
	}
	for target, formula := range pending {
		if ctx.Err() != nil {
			continue
		}
		gate := s.harnessGate(domain.AgentHarness(target))
		if !gate.TryLock() {
			continue
		}
		stale, err := observer.CountStaleBinarySessions(ctx)
		if err != nil || stale[domain.AgentHarness(target)] > 0 {
			gate.Unlock()
			continue
		}
		if _, reviewers, err := s.harnessInUse(ctx, target); err != nil || len(reviewers) > 0 {
			gate.Unlock()
			continue
		}
		s.mu.Lock()
		active := false
		if job, ok := s.jobs[target]; ok {
			active = activeStatus(job.Status)
		}
		s.mu.Unlock()
		if !active {
			if used, probeErr := s.brewCleanupHasProcess(ctx, target, formula, processes); probeErr != nil || used {
				gate.Unlock()
				continue
			}
			argv := []string{"brew", "cleanup", formula}
			env := []string{"CI=1", "NONINTERACTIVE=1", "HOMEBREW_NO_AUTO_UPDATE=1"}
			if s.installCommands != nil {
				err = s.installCommands.RunInstall(ctx, ports.InstallCommand{Argv: argv, Env: env}, io.Discard, io.Discard)
			} else {
				err = s.commands.Run(ctx, argv, io.Discard, io.Discard)
			}
			if err == nil {
				s.mu.Lock()
				if s.deferredCleanups[target] == formula {
					delete(s.deferredCleanups, target)
				}
				s.mu.Unlock()
			}
		}
		gate.Unlock()
	}
}
