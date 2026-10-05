package sessionmanager

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type cancellationCueRunner struct {
	entered  chan struct{}
	publish  chan struct{}
	mu       sync.Mutex
	closed   bool
	closeErr error
}

func (r *cancellationCueRunner) RunStartupCue(_ context.Context, _ shellterm.RunStartupCueInput) (shellterm.StartupCueCommandResult, error) {
	close(r.entered)
	<-r.publish
	return shellterm.StartupCueCommandResult{Terminal: shellterm.ShellTerminal{HandleID: "startup-terminal"}, Marker: "done"}, nil
}

func (r *cancellationCueRunner) GetOutput(context.Context, string, int) (string, error) {
	return "setup output", nil
}

func (r *cancellationCueRunner) CloseShellTerminal(context.Context, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closeErr != nil {
		return r.closeErr
	}
	r.closed = true
	return nil
}

func TestStartupCueCancelWaitsForTerminalShutdown(t *testing.T) {
	for _, closeFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "shutdown_failed"}[closeFails], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			st := sqlitetest.MustOpenAt(t, t.TempDir())
			if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "project", RegisteredAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "project", Mode: domain.SessionModeTUI, Kind: domain.KindWorker, CreatedAt: time.Now(), UpdatedAt: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.ClaimStartupCue(ctx, rec.ID, domain.StartupCueRun{State: "pending", Command: "setup"}); err != nil {
				t.Fatal(err)
			}
			m := New(Deps{Store: st, Logger: slog.New(slog.DiscardHandler)})
			runner := &cancellationCueRunner{entered: make(chan struct{}), publish: make(chan struct{})}
			if closeFails {
				runner.closeErr = errors.New("terminal still running")
			}
			m.SetStartupCueRunner(runner)
			released := make(chan struct{}, 1)
			m.startStartupCue(rec.ID, domain.ProjectRecord{ID: "project"}, t.TempDir(), func(context.Context) error {
				runner.mu.Lock()
				stopped := runner.closed
				runner.mu.Unlock()
				if !stopped {
					t.Error("delivery released before terminal shutdown")
				}
				released <- struct{}{}
				return nil
			})
			select {
			case <-runner.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			result := make(chan error, 1)
			go func() { _, err := m.CancelStartupCue(ctx, rec.ID); result <- err }()
			// Wait for cancellation admission before allowing the terminal to
			// publish. This reproduces the missing-persisted-handle race.
			for {
				m.startupCueExecMu.Lock()
				requested := m.startupCueExec[rec.ID] != nil && m.startupCueExec[rec.ID].cancelRequested
				m.startupCueExecMu.Unlock()
				if requested {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Millisecond):
				}
			}
			select {
			case <-released:
				t.Fatal("released before terminal publication")
			default:
			}
			close(runner.publish)
			select {
			case err := <-result:
				if (err != nil) != closeFails {
					t.Fatalf("cancel error = %v, closeFails=%v", err, closeFails)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			stored, _, err := st.GetSession(ctx, rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.StartupCue.TerminalHandle != "startup-terminal" {
				t.Fatalf("handle not persisted: %+v", stored.StartupCue)
			}
			if closeFails {
				if !stored.StartupCue.HoldsInput() || stored.ProvisionState != domain.SessionProvisionFailed {
					t.Fatalf("unsafe shutdown failure: %+v", stored)
				}
				select {
				case <-released:
					t.Fatal("released despite shutdown failure")
				default:
				}
			} else if stored.StartupCue.State != "cancelled" || stored.StartupCue.HoldsInput() {
				t.Fatalf("cancel result: %+v", stored.StartupCue)
			}
		})
	}
}
