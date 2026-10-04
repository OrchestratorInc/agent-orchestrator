package daemon

import (
	"context"
	"io"
	"log/slog"
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
		warmInstalledHarnessUpdates(context.Background(), ready,
			func(context.Context) (agentsvc.Readiness, error) {
				close(readinessCalled)
				return agentsvc.Readiness{Agents: []domain.AgentReadinessSnapshot{
					{ID: "codex", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled}},
					{ID: "claude-code", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationNotInstalled}},
					{ID: "cursor", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationUnknown}},
					{ID: "unregistered", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled}},
				}}, nil
			},
			func(_ context.Context, target systeminstall.Target) (systeminstall.UpdateAdvisory, error) {
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
	go func() {
		defer close(done)
		warmInstalledHarnessUpdates(ctx, ready,
			func(context.Context) (agentsvc.Readiness, error) {
				t.Fatal("readiness must not be read after shutdown")
				return agentsvc.Readiness{}, nil
			},
			func(context.Context, systeminstall.Target) (systeminstall.UpdateAdvisory, error) {
				t.Fatal("update check must not start after shutdown")
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
}
