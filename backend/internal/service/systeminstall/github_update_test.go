package systeminstall

import (
	"context"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestGitHubExistingInstallationPlansUpgrade(t *testing.T) {
	for _, tc := range []struct {
		goos        string
		manager     string
		command     []string
		prepare     [][]string
		unsupported bool
	}{
		{"darwin", "brew", []string{"brew", "upgrade", "gh"}, [][]string{{"brew", "update"}}, false},
		{"windows", "winget", []string{"winget", "upgrade", "-e", "--id", "GitHub.cli", "--source", "winget", "--silent", "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity"}, nil, false},
		{"linux", "apt-get", []string{"apt-get", "install", "--only-upgrade", "-y", "gh"}, [][]string{{"sudo", "apt-get", "update"}}, true},
		{"linux", "dnf", []string{"dnf", "upgrade", "-y", "gh"}, nil, true},
		{"linux", "pacman", []string{"pacman", "-S", "--noconfirm", "github-cli"}, nil, true},
		{"linux", "zypper", []string{"zypper", "update", "-y", "gh"}, nil, true},
		{"linux", "apk", []string{"apk", "upgrade", "gh"}, nil, true},
	} {
		t.Run(tc.manager, func(t *testing.T) {
			s := newTestService(tc.goos, "gh", tc.manager)
			plan := s.planGH()
			if !reflect.DeepEqual(plan.Command, tc.command) || !reflect.DeepEqual(plan.Prepare, tc.prepare) || plan.Unsupported != tc.unsupported {
				t.Fatalf("plan=%+v", plan)
			}
		})
	}
}

func TestGitHubUpdateRefreshesHomebrewBeforeUpgrade(t *testing.T) {
	s := newTestService("darwin", "gh", "brew")
	var commands [][]string
	s.installCommands = installCommandRunnerFunc(func(_ context.Context, cmd ports.InstallCommand, _, _ io.Writer) error {
		commands = append(commands, append([]string(nil), cmd.Argv...))
		return nil
	})
	if _, err := s.Start(context.Background(), TargetGH); err != nil {
		t.Fatal(err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// Wait through Status rather than canceling the update via Close.
	for {
		job, err := s.Status(closeCtx, TargetGH)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != StatusRunning {
			if job.Status != StatusSucceeded {
				t.Fatalf("job=%+v", job)
			}
			break
		}
		if closeCtx.Err() != nil {
			t.Fatal(closeCtx.Err())
		}
		time.Sleep(time.Millisecond)
	}
	if err := s.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(commands, [][]string{{"brew", "update"}, {"brew", "upgrade", "gh"}}) {
		t.Fatalf("commands=%v", commands)
	}
}
