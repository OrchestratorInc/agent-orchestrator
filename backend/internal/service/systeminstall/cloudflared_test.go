package systeminstall

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type managedBinaryInstallerFunc func(context.Context, ports.ManagedBinaryDownload) (string, error)

func (f managedBinaryInstallerFunc) InstallManagedBinary(ctx context.Context, download ports.ManagedBinaryDownload) (string, error) {
	return f(ctx, download)
}

// newDownloadService is a Service that can download AO's pinned cloudflared.
// install counts every download attempt.
func newDownloadService(goos, goarch string, install func(ports.ManagedBinaryDownload) (string, error), found ...string) (*Service, *atomic.Int32) {
	s := newTestService(goos, found...)
	s.goarch = goarch
	calls := &atomic.Int32{}
	s.managedBinaries = managedBinaryInstallerFunc(func(_ context.Context, download ports.ManagedBinaryDownload) (string, error) {
		calls.Add(1)
		return install(download)
	})
	return s, calls
}

func TestPlanCloudflaredDownloadsThePinnedRelease(t *testing.T) {
	tests := []struct {
		goos, goarch, asset, member, name string
	}{
		{"darwin", "arm64", "cloudflared-darwin-arm64.tgz", "cloudflared", "cloudflared"},
		{"darwin", "amd64", "cloudflared-darwin-amd64.tgz", "cloudflared", "cloudflared"},
		{"linux", "amd64", "cloudflared-linux-amd64", "", "cloudflared"},
		{"linux", "arm64", "cloudflared-linux-arm64", "", "cloudflared"},
		{"windows", "amd64", "cloudflared-windows-amd64.exe", "", "cloudflared.exe"},
	}
	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			// A package manager on PATH must not win over the download.
			s, _ := newDownloadService(tt.goos, tt.goarch, nil, "brew", "winget", "apt-get")
			plan := s.planFor(TargetCloudflared)
			if plan.Unsupported || plan.download == nil {
				t.Fatalf("plan = %+v, want the pinned download", plan)
			}
			d := plan.download
			if !strings.HasSuffix(d.URL, "/"+cloudflaredVersion+"/"+tt.asset) || len(d.SHA256) != 64 {
				t.Fatalf("download = %+v, want %s %s with a sha256", d, cloudflaredVersion, tt.asset)
			}
			if d.ArchiveMember != tt.member || d.Name != tt.name {
				t.Fatalf("member, name = %q, %q, want %q, %q", d.ArchiveMember, d.Name, tt.member, tt.name)
			}
		})
	}
}

// No pinned asset means today's package-manager route, unchanged.
func TestPlanCloudflaredWithoutAPinnedAssetUsesThePackageManager(t *testing.T) {
	s, _ := newDownloadService("windows", "arm64", nil, "winget")
	plan := s.planFor(TargetCloudflared)
	if plan.download != nil || strings.Join(plan.Command, " ") != "winget install -e --id Cloudflare.cloudflared" {
		t.Fatalf("plan = %+v, want the winget fallback", plan)
	}
}

func TestCloudflaredDownloadLandsAndReportsSuccess(t *testing.T) {
	s, _ := newDownloadService("darwin", "arm64", func(download ports.ManagedBinaryDownload) (string, error) {
		return "/Users/test/.ao/bin/" + download.Name, nil
	})
	succeeded := make(chan Target, 1)
	s.SetOnSucceeded(func(target Target) { succeeded <- target })

	job, err := s.Start(context.Background(), TargetCloudflared)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if job.Status != StatusRunning || job.Method != cloudflaredDownloadMethod {
		t.Fatalf("job = %+v, want a running download the desktop can label", job)
	}
	waitForStatus(t, s, TargetCloudflared, StatusSucceeded)
	final, _ := s.Status(context.Background(), TargetCloudflared)
	if final.ExpectedDestination != "/Users/test/.ao/bin/cloudflared" {
		t.Fatalf("ExpectedDestination = %q", final.ExpectedDestination)
	}
	select {
	case <-succeeded:
	case <-time.After(time.Second):
		t.Fatal("success callback did not run")
	}
}

// A failed or refused download must not leave remote access stuck: the next
// Start hands the user today's package-manager route instead of the same
// download again.
func TestCloudflaredDownloadFailureFallsBackToThePackageManager(t *testing.T) {
	s, calls := newDownloadService("linux", "amd64", func(ports.ManagedBinaryDownload) (string, error) {
		return "", errors.New("cloudflared checksum mismatch")
	}, "apt-get")

	if _, err := s.Start(context.Background(), TargetCloudflared); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, s, TargetCloudflared, StatusFailed)
	failed, _ := s.Status(context.Background(), TargetCloudflared)
	if failed.Method != cloudflaredDownloadMethod || !strings.Contains(failed.Error, "checksum mismatch") {
		t.Fatalf("failed job = %+v, want the download's error", failed)
	}

	next, err := s.Start(context.Background(), TargetCloudflared)
	if err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if next.Status != StatusUnsupported || next.Command != "sudo apt-get install -y cloudflared" {
		t.Fatalf("second job = %+v, want the manual apt-get fallback", next)
	}
	if calls.Load() != 1 {
		t.Fatalf("download attempts = %d, want 1", calls.Load())
	}
}

// Remote access asks for the connector every time it finds none. That must
// start one download, not one per request, and report its landing once.
func TestDownloadCloudflaredInBackgroundRunsOnce(t *testing.T) {
	release := make(chan struct{})
	s, calls := newDownloadService("darwin", "arm64", func(ports.ManagedBinaryDownload) (string, error) {
		<-release
		return "/Users/test/.ao/bin/cloudflared", nil
	})
	installed := make(chan struct{}, 4)
	onInstalled := func() { installed <- struct{}{} }

	s.DownloadCloudflaredInBackground(onInstalled)
	s.DownloadCloudflaredInBackground(onInstalled)
	if job, _ := s.Status(context.Background(), TargetCloudflared); job.Status != StatusRunning {
		t.Fatalf("status = %q, want the download running for the desktop to show", job.Status)
	}
	close(release)
	waitForStatus(t, s, TargetCloudflared, StatusSucceeded)
	s.DownloadCloudflaredInBackground(onInstalled)

	select {
	case <-installed:
	case <-time.After(time.Second):
		t.Fatal("onInstalled did not run after the download landed")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if calls.Load() != 1 || len(installed) != 0 {
		t.Fatalf("downloads = %d, extra onInstalled = %d, want 1 and 0", calls.Load(), len(installed))
	}
}

// Only the download runs unasked. Where there is nothing to download, the
// trigger must not fall through to running a package manager.
func TestDownloadCloudflaredInBackgroundNeverRunsAPackageManager(t *testing.T) {
	s, _ := newDownloadService("darwin", "386", nil, "brew")
	s.DownloadCloudflaredInBackground(func() { t.Error("onInstalled ran without a download") })
	s.mu.Lock()
	_, started := s.jobs[TargetCloudflared]
	s.mu.Unlock()
	if started {
		t.Fatal("the automatic trigger started a package-manager install")
	}
}

// Where there is nothing to download, remote access waits on the install the
// user starts instead, and that install must start the connector as well.
func TestPackageManagerCloudflaredStartsTheWaitingConnector(t *testing.T) {
	s, _ := newDownloadService("darwin", "386", nil, "brew", "cloudflared")
	s.commands = testCommandRunner(func(ctx context.Context, _ []string) *exec.Cmd {
		return exec.CommandContext(ctx, "true")
	})
	installed := make(chan struct{}, 1)
	s.DownloadCloudflaredInBackground(func() { installed <- struct{}{} })

	if _, err := s.Start(context.Background(), TargetCloudflared); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, s, TargetCloudflared, StatusSucceeded)
	select {
	case <-installed:
	case <-time.After(time.Second):
		t.Fatal("the connector waiting on cloudflared was not started")
	}
}
