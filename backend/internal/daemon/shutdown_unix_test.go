//go:build !windows

package daemon

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/testingevidence"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	testingsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/testing"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type shutdownTarget struct {
	ports.TestingTargetEnvironment
	stop func(context.Context) error
}

func (target shutdownTarget) ReadLogs(context.Context, domain.TestTargetIdentity, domain.TestReadLogsRequest) (domain.TestLogResult, error) {
	return domain.TestLogResult{Text: "target log"}, nil
}

func (target shutdownTarget) Stop(ctx context.Context, _ domain.TestTargetIdentity) (ports.TestingCleanupResult, error) {
	return ports.TestingCleanupResult{State: domain.TestCleanupComplete}, target.stop(ctx)
}

func TestShutdownSignalsAllowTestingCloseToFinish(t *testing.T) {
	if mode := os.Getenv("AO_SHUTDOWN_HELPER"); mode != "" {
		ctx, cancelWorkers, stopSignals := daemonContext()
		defer stopSignals()
		defer cancelWorkers()
		input := bufio.NewReader(os.Stdin)
		store, err := sqlite.OpenPreMigrated(os.Getenv("AO_SHUTDOWN_HELPER_DATA"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := store.Close(); err != nil {
				t.Error(err)
			}
		})
		dir, now := t.TempDir(), time.Now().UTC()
		if err := store.UpsertProject(context.Background(), domain.ProjectRecord{ID: "testing", Path: dir, RegisteredAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateTestRun(context.Background(), domain.TestRunRecord{ID: "run", ProjectID: "testing", IssueSnapshot: `{}`, RecipeSnapshot: `{}`, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		attempt, err := store.CreateTestAttempt(context.Background(), domain.TestAttemptRecord{ID: "attempt", RunID: "run", CreatedAt: now, Deadline: now.Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		attempt.Phase, attempt.Target = domain.TestAttemptActive, domain.TestTargetIdentity{ID: "target"}
		if err := store.UpdateTestAttempt(context.Background(), attempt); err != nil {
			t.Fatal(err)
		}
		closed := false
		svc := testingsvc.New(testingsvc.Deps{
			Store: store, Evidence: testingevidence.New(dir, store),
			Target: shutdownTarget{stop: func(context.Context) error {
				rec, _, err := store.GetTestAttempt(context.Background(), attempt.ID)
				if err != nil {
					return err
				}
				if rec.CleanupState != domain.TestCleanupRunning {
					return fmt.Errorf("cleanup did not enter running: %+v", rec)
				}
				fmt.Println("cleanup running")
				_, err = input.ReadString('\n')
				return err
			}},
			CloseDesktop: func(context.Context) error { closed = true; return nil },
		})
		fmt.Println("ready")
		if mode == "http" {
			if _, err := input.ReadString('\n'); err != nil {
				t.Fatal(err)
			}
		} else {
			<-ctx.Done()
		}
		// Run cancels workers before joining the testing service's cleanup.
		cancelWorkers()
		if ctx.Err() != context.Canceled {
			t.Fatal("shutdown did not cancel workers", ctx.Err())
		}
		// A cleanup started by cancellation must survive the subsequent Close too.
		if _, err := svc.Cancel(context.Background(), attempt.ID); err != nil {
			t.Fatal(err)
		}
		if err := svc.Close(); err != nil || !closed {
			t.Fatal("testing cleanup did not finish", err)
		}
		rec, _, err := store.GetTestAttempt(context.Background(), attempt.ID)
		if err != nil || rec.Phase != domain.TestAttemptFinished || rec.Outcome != domain.TestOutcomeCancelled || rec.CancelledAt == nil || rec.CleanupState != domain.TestCleanupComplete {
			t.Fatal("shutdown did not persist terminal cleanup", rec, err)
		}
		fmt.Println("cleanup complete")
		return
	}

	for _, tc := range []struct {
		name  string
		first syscall.Signal
		last  syscall.Signal
	}{
		{name: "SIGINT then SIGTERM", first: syscall.SIGINT, last: syscall.SIGTERM},
		{name: "SIGINT repeated", first: syscall.SIGINT, last: syscall.SIGINT},
		{name: "SIGTERM repeated", first: syscall.SIGTERM, last: syscall.SIGTERM},
		{name: "HTTP then SIGINT", last: syscall.SIGINT},
		{name: "HTTP then SIGTERM", last: syscall.SIGTERM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Migrate once in the parent, not in every race-instrumented child.
			dataDir := t.TempDir()
			store, err := sqlitetest.Open(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestShutdownSignalsAllowTestingCloseToFinish$")
			mode := "http"
			if tc.first != 0 {
				mode = strconv.Itoa(int(tc.first))
			}
			cmd.Env = append(os.Environ(), "AO_SHUTDOWN_HELPER="+mode, "AO_SHUTDOWN_HELPER_DATA="+dataDir)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cancel()
				_ = input.Close()
				_ = cmd.Wait()
			})
			reader := bufio.NewReader(output)
			read := func(want string) {
				t.Helper()
				line, err := reader.ReadString('\n')
				if err != nil || line != want+"\n" {
					rest, _ := io.ReadAll(reader)
					waitErr := cmd.Wait()
					t.Fatalf("wanted %q, got %q: %v; helper exit: %v\n%s%s", want, line, err, waitErr, rest, stderr.String())
				}
			}
			read("ready")
			if tc.first == 0 {
				_, err = input.Write([]byte("shutdown\n"))
			} else {
				err = cmd.Process.Signal(tc.first)
			}
			if err != nil {
				t.Fatal(err)
			}
			read("cleanup running")
			if err := cmd.Process.Signal(tc.last); err != nil {
				t.Fatal(err)
			}
			if _, err := input.Write([]byte("finish\n")); err != nil {
				t.Fatal(err)
			}
			read("cleanup complete")
			if err := cmd.Wait(); err != nil {
				t.Fatalf("shutdown helper exited before cleanup finished: %v\n%s", err, stderr.String())
			}
		})
	}
}
