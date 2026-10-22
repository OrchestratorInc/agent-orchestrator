package systeminstall

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func openCodeV2HomebrewPlan() Plan {
	return Plan{
		Target: TargetOpencodeV2, Method: "homebrew", Package: "anomalyco/tap/opencode-v2",
		Prepare:     [][]string{{"brew", "unlink", "opencode"}},
		Command:     []string{"brew", "install", "anomalyco/tap/opencode-v2"},
		Cleanup:     [][]string{{"brew", "unlink", "opencode-v2"}, {"brew", "link", "opencode"}},
		alsoChanges: []Target{TargetOpencode},
	}
}

// An OpenCode 1 launch or restore while OpenCode 2's Homebrew install has
// OpenCode 1 unlinked waits until its link is back, and OpenCode 1's own
// install is refused meanwhile.
func TestOpenCodeV1LaunchWaitsWhileV2HomebrewInstallHasItUnlinked(t *testing.T) {
	s := newTestService("darwin")
	var mu sync.Mutex
	var events []string
	record := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}
	installing := make(chan struct{})
	finishInstall := make(chan struct{})
	s.installCommands = installCommandRunnerFunc(func(_ context.Context, command ports.InstallCommand, _, _ io.Writer) error {
		argv := strings.Join(command.Argv, " ")
		if argv == "brew install anomalyco/tap/opencode-v2" {
			close(installing)
			<-finishInstall
		}
		record(argv)
		return nil
	})
	job := &Job{Target: TargetOpencodeV2, Method: "homebrew", Status: StatusInstalling}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runAgentOperation(context.Background(), openCodeV2HomebrewPlan(), AgentOperationInstall, job, nil)
	}()
	<-installing

	if _, err := s.StartAgent(context.Background(), TargetOpencode, ""); !errors.Is(err, ErrInstallActive) {
		t.Fatalf("OpenCode 1 install while unlinked = %v, want ErrInstallActive", err)
	}

	launched := make(chan bool, 1)
	go func() {
		release, ok := s.TryBeginHarnessUse(domain.AgentHarness(TargetOpencode))
		if ok {
			record("launch opencode")
			release()
		}
		launched <- ok
	}()
	select {
	case <-launched:
		t.Fatal("OpenCode 1 launch started while its Homebrew link was removed")
	case <-time.After(300 * time.Millisecond):
	}

	close(finishInstall)
	select {
	case ok := <-launched:
		if !ok {
			t.Fatal("OpenCode 1 launch was refused instead of waiting for the install")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("OpenCode 1 launch still waiting after the install finished")
	}
	<-done

	mu.Lock()
	defer mu.Unlock()
	link := slices.Index(events, "brew link opencode")
	launch := slices.Index(events, "launch opencode")
	if link < 0 || launch < link {
		t.Fatalf("events = %v, want the launch only after OpenCode 1 is linked again", events)
	}
}

// An OpenCode 1 install request and an OpenCode 2 run reserving OpenCode 1
// that arrive together: exactly one is admitted, never both, whichever wins.
func TestOpenCodeV1InstallAndV2HoldAreMutuallyExclusive(t *testing.T) {
	for iteration := 0; iteration < 50; iteration++ {
		s := newTestService("darwin", "brew")
		unblock := make(chan struct{})
		s.installCommands = installCommandRunnerFunc(func(context.Context, ports.InstallCommand, io.Writer, io.Writer) error {
			<-unblock // keep an admitted OpenCode 1 install active
			return nil
		})
		start := make(chan struct{})
		var installErr, holdErr error
		var release func()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, installErr = s.StartAgent(context.Background(), TargetOpencode, "")
		}()
		go func() {
			defer wg.Done()
			<-start
			release, holdErr = s.holdOtherHarnesses([]Target{TargetOpencode})
		}()
		close(start)
		wg.Wait()

		if (installErr == nil) == (holdErr == nil) {
			t.Fatalf("iteration %d: install err=%v, hold err=%v; want exactly one admitted", iteration, installErr, holdErr)
		}
		for _, err := range []error{installErr, holdErr} {
			if err != nil && !errors.Is(err, ErrInstallActive) {
				t.Fatalf("iteration %d: refusal = %v, want ErrInstallActive", iteration, err)
			}
		}
		release()
		close(unblock)
		s.workers.Wait()
	}
}

// A cleanup step that runs until its deadline must not leave the next one,
// which restores OpenCode 1, without time.
func TestPlanCleanupStepsHaveIndependentDeadlines(t *testing.T) {
	s := newTestService("darwin")
	s.cleanupStepLimit = 100 * time.Millisecond
	var relinkCtxErr error
	relinked := false
	s.installCommands = installCommandRunnerFunc(func(ctx context.Context, command ports.InstallCommand, _, _ io.Writer) error {
		switch strings.Join(command.Argv, " ") {
		case "brew unlink opencode-v2":
			<-ctx.Done()
			return ctx.Err()
		case "brew link opencode":
			relinked = true
			relinkCtxErr = ctx.Err()
		}
		return nil
	})
	job := &Job{Target: TargetOpencodeV2, Method: "homebrew", Status: StatusInstalling}
	s.runAgentOperation(context.Background(), openCodeV2HomebrewPlan(), AgentOperationInstall, job, nil)
	if !relinked || relinkCtxErr != nil {
		t.Fatalf("OpenCode 1 relink ran=%v ctxErr=%v, want it run with a live deadline", relinked, relinkCtxErr)
	}
	if job.Status != StatusFailed || !strings.Contains(job.Error, "brew unlink opencode-v2") {
		t.Fatalf("job = %+v, want the hung unlink reported", job)
	}
}

// The OpenCode 2 run fails, changing nothing, when OpenCode 1 cannot be held.
func TestOpenCodeV2HomebrewRunFailsWhenOpenCode1IsBusy(t *testing.T) {
	s := newTestService("darwin")
	s.jobs[TargetOpencode] = &Job{Target: TargetOpencode, Status: StatusInstalling}
	ran := 0
	s.installCommands = installCommandRunnerFunc(func(context.Context, ports.InstallCommand, io.Writer, io.Writer) error {
		ran++
		return nil
	})
	job := &Job{Target: TargetOpencodeV2, Method: "homebrew", Status: StatusInstalling}
	s.runAgentOperation(context.Background(), openCodeV2HomebrewPlan(), AgentOperationInstall, job, nil)
	if ran != 0 || job.Status != StatusFailed || !strings.Contains(job.Error, "opencode") {
		t.Fatalf("ran %d commands, job = %+v; want none run and a failure naming OpenCode 1", ran, job)
	}
	if s.harnessChanging(TargetOpencode) {
		t.Fatal("OpenCode 1 still marked changing after the refused run")
	}
}
