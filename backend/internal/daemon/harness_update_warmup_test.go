package daemon

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	agentsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/agent"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systeminstall"
)

func TestHarnessUpdateWarmupWaitsForReadinessAndChecksOnlyInstalledHarnesses(t *testing.T) {
	ready := make(chan struct{})
	readinessCalled := make(chan struct{})
	checked := make(chan systeminstall.Target, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchInstalledHarnessUpdates(context.Background(), ready, 0,
			func(context.Context) (agentsvc.Readiness, error) {
				close(readinessCalled)
				return agentsvc.Readiness{Agents: []domain.AgentReadinessSnapshot{
					{ID: "codex", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled}},
					{ID: "claude-code", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationNotInstalled}},
					{ID: "cursor", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationUnknown}},
					{ID: "unregistered", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled}},
				}}, nil
			},
			func(_ context.Context, target systeminstall.Target, _ bool) (systeminstall.UpdateAdvisory, error) {
				checked <- target
				return systeminstall.UpdateAdvisory{}, nil
			},
			slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	select {
	case <-readinessCalled:
		t.Fatal("update warm-up read readiness before background checks completed")
	case <-time.After(30 * time.Millisecond):
	}
	close(ready)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("update warm-up did not finish")
	}
	select {
	case target := <-checked:
		if target != systeminstall.TargetCodex {
			t.Fatalf("checked %q, want only codex", target)
		}
	default:
		t.Fatal("installed Codex was not checked")
	}
	select {
	case extra := <-checked:
		t.Fatalf("unexpected update check for %q", extra)
	default:
	}
}

func TestHarnessUpdateWarmupStopsIfDaemonExitsBeforeReadinessCompletes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan struct{})
	readinessCalled := make(chan struct{}, 1)
	checkCalled := make(chan struct{}, 1)
	go func() {
		defer close(done)
		watchInstalledHarnessUpdates(ctx, ready, time.Hour,
			func(context.Context) (agentsvc.Readiness, error) {
				readinessCalled <- struct{}{}
				return agentsvc.Readiness{}, nil
			},
			func(context.Context, systeminstall.Target, bool) (systeminstall.UpdateAdvisory, error) {
				checkCalled <- struct{}{}
				return systeminstall.UpdateAdvisory{}, nil
			},
			slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("update warm-up did not stop on shutdown")
	}
	select {
	case <-readinessCalled:
		t.Fatal("readiness was read after shutdown")
	default:
	}
	select {
	case <-checkCalled:
		t.Fatal("update check started after shutdown")
	default:
	}
}

func TestHarnessUpdateWatchRechecksEveryIntervalWithFreshLookups(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	close(ready)
	type pass struct {
		target  systeminstall.Target
		refresh bool
	}
	passes := make(chan pass, 16)
	installed := []domain.AgentReadinessSnapshot{{ID: "codex", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled}}}
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchInstalledHarnessUpdates(ctx, ready, 20*time.Millisecond,
			func(context.Context) (agentsvc.Readiness, error) {
				mu.Lock()
				defer mu.Unlock()
				return agentsvc.Readiness{Agents: append([]domain.AgentReadinessSnapshot(nil), installed...)}, nil
			},
			func(_ context.Context, target systeminstall.Target, refresh bool) (systeminstall.UpdateAdvisory, error) {
				passes <- pass{target, refresh}
				return systeminstall.UpdateAdvisory{}, nil
			},
			slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	next := func() pass {
		t.Helper()
		select {
		case p := <-passes:
			return p
		case <-time.After(2 * time.Second):
			t.Fatal("no update check arrived")
			return pass{}
		}
	}
	if first := next(); first.target != systeminstall.TargetCodex || first.refresh {
		t.Fatalf("startup pass = %+v, want a cached codex check", first)
	}
	if second := next(); second.target != systeminstall.TargetCodex || !second.refresh {
		t.Fatalf("periodic pass = %+v, want a fresh codex check", second)
	}
	// A harness installed after startup joins the next pass.
	mu.Lock()
	installed = append(installed, domain.AgentReadinessSnapshot{ID: "claude-code", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled}})
	mu.Unlock()
	seen := map[systeminstall.Target]bool{}
	for deadline := time.Now().Add(2 * time.Second); !seen[systeminstall.TargetClaudeCode] && time.Now().Before(deadline); {
		seen[next().target] = true
	}
	if !seen[systeminstall.TargetClaudeCode] {
		t.Fatal("a newly installed harness was never checked")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("periodic update checks did not stop on shutdown")
	}
}
