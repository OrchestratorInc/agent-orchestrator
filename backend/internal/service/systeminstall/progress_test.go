package systeminstall

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func trackOutput(chunks ...string) *Progress {
	tracker := newProgressTracker(io.Discard)
	for _, chunk := range chunks {
		_, _ = tracker.Write([]byte(chunk))
	}
	return tracker.Snapshot()
}

func percentOf(progress *Progress) int {
	if progress.Percent == nil {
		return -1
	}
	return *progress.Percent
}

func TestProgressTrackerReadsUVDownloadSizes(t *testing.T) {
	// Captured from `uv tool install mistral-vibe` without a TTY.
	head := "Resolved 98 packages in 4.50s\n" +
		"Downloading pydantic-core (1.9MiB)\n" +
		"Downloading cryptography (3.8MiB)\n" +
		"Downloading mistral-vibe (36.2MiB)\n"
	progress := trackOutput(head)
	if progress.Phase != ProgressDownloading || percentOf(progress) != 0 {
		t.Fatalf("after announcements = %+v, want downloading 0%%", progress)
	}
	progress = trackOutput(head, " Downloaded pydantic-core\n", " Downloaded cryptography\n")
	if got := percentOf(progress); got != 13 {
		t.Fatalf("after the small wheels percent = %d, want 13 (5.7 of 41.9 MiB)", got)
	}
	if progress.Detail != "Downloaded cryptography" {
		t.Fatalf("detail = %q", progress.Detail)
	}
	progress = trackOutput(head, " Downloaded pydantic-core\n", " Downloaded cryptography\n", " Downloaded mistral-vibe\n")
	if got := percentOf(progress); got != 100 {
		t.Fatalf("all wheels downloaded percent = %d, want 100", got)
	}
	progress = trackOutput(head, " Downloaded mistral-vibe\n", "Prepared 98 packages in 49.99s\n", "Installed 98 packages in 175ms\n")
	if progress.Phase != ProgressInstalling || progress.Percent != nil {
		t.Fatalf("after prepare = %+v, want installing with no percent", progress)
	}
}

func TestProgressTrackerCountsHomebrewDependencies(t *testing.T) {
	head := "==> Fetching downloads for: hermes-agent\n" +
		"✔︎ Bottle Manifest hermes-agent (0.22.4)\n" +
		"✔︎ Bottle hermes-agent (0.22.4)\n"
	progress := trackOutput(head)
	if progress.Phase != ProgressDownloading || progress.Percent != nil {
		t.Fatalf("while fetching = %+v, want downloading with no percent", progress)
	}
	deps := head + "==> Installing dependencies for hermes-agent: pillow, rpds-py and tirith\n"
	if progress = trackOutput(deps); progress.Phase != ProgressInstalling || percentOf(progress) != 0 {
		t.Fatalf("dependency list = %+v, want installing 0%%", progress)
	}
	progress = trackOutput(deps,
		"==> Installing hermes-agent dependency: pillow (12.0.0)\n",
		"==> Pouring pillow--12.0.0.arm64_sequoia.bottle.tar.gz\n",
		"==> Installing hermes-agent dependency: rpds-py (0.27.1)\n")
	if got := percentOf(progress); got != 25 {
		t.Fatalf("second of four steps percent = %d, want 25", got)
	}
	progress = trackOutput(deps,
		"==> Installing hermes-agent dependency: pillow (12.0.0)\n",
		"==> Installing hermes-agent dependency: rpds-py (0.27.1)\n",
		"==> Installing hermes-agent dependency: tirith (1.0.0)\n",
		"==> Installing hermes-agent\n",
		"==> Pouring hermes-agent--0.22.4.arm64_sequoia.bottle.tar.gz\n")
	if got := percentOf(progress); got != 75 || progress.Phase != ProgressInstalling {
		t.Fatalf("formula step = %+v, want installing 75%%", progress)
	}
}

func TestProgressTrackerReadsCarriageReturnPercentages(t *testing.T) {
	// curl's progress bar redraws one line with carriage returns.
	progress := trackOutput("Downloading binary\n", "\r####                        12.5%", "\r############                45.0%", "\r")
	if progress.Phase != ProgressDownloading || percentOf(progress) != 45 {
		t.Fatalf("curl bar = %+v, want downloading 45%%", progress)
	}
	if progress = trackOutput("\x1b[32m⠋\x1b[0m Bottle node (25.0.0)  12.0MB/48.0MB\n"); percentOf(progress) != 25 {
		t.Fatalf("byte ratio = %+v, want 25%%", progress)
	}
	if progress = trackOutput("Downloading binary\n", "Installing binary\n", "Downloading helper\n"); progress.Phase != ProgressInstalling {
		t.Fatalf("phase went back to %q after installing", progress.Phase)
	}
}

func TestProgressTrackerMovesToInstallingWhenAScriptReportsSuccess(t *testing.T) {
	// Captured from https://install.openhands.dev/install.sh without a TTY.
	if progress := trackOutput("[INFO] Starting OpenHands CLI installation...\n"); progress.Phase != "" {
		t.Fatalf("before any download line phase = %q, want none", progress.Phase)
	}
	head := "[INFO] Starting OpenHands CLI installation...\n" +
		"[INFO] Installing to: /tmp/home/.local/bin/openhands\n" +
		"[INFO] Downloading OpenHands CLI v1.14.0...\n"
	if progress := trackOutput(head); progress.Phase != ProgressDownloading {
		t.Fatalf("after the download line phase = %q, want downloading", progress.Phase)
	}
	progress := trackOutput(head, "[SUCCESS] OpenHands CLI v1.14.0 installed successfully!\n", "[INFO] Version information:\n")
	if progress.Phase != ProgressInstalling || progress.Percent != nil {
		t.Fatalf("after the success line = %+v, want installing with no percent", progress)
	}
}

func TestProgressTrackerReportsNoPhaseUntilALineNamesOne(t *testing.T) {
	if progress := trackOutput(); progress != nil {
		t.Fatalf("silent installer = %+v, want no progress", progress)
	}
	// Captured from Aider's official script: lower case, no log-level prefix.
	if progress := trackOutput("downloading uv 0.5.9 aarch64-apple-darwin\n"); progress.Phase != ProgressDownloading {
		t.Fatalf("lower-case download line phase = %q, want downloading", progress.Phase)
	}
	if progress := trackOutput("downloading uv 0.5.9 aarch64-apple-darwin\n", "Installing aider...\n"); progress.Phase != ProgressInstalling {
		t.Fatalf("install line after the download phase = %q, want installing", progress.Phase)
	}
}

func TestProgressTrackerTreatsAScriptTitleAsNoPhase(t *testing.T) {
	// Captured from https://chatgpt.com/codex/install.sh without a TTY.
	head := "==> Installing Codex CLI\n" +
		"==> Detected platform: macOS (Apple Silicon)\n" +
		"==> Resolved version: 0.166.0\n"
	if progress := trackOutput(head); progress.Phase != "" {
		t.Fatalf("after the title phase = %q, want none", progress.Phase)
	}
	if progress := trackOutput(head, "==> Downloading Codex CLI\n"); progress.Phase != ProgressDownloading {
		t.Fatalf("after the download line phase = %q, want downloading", progress.Phase)
	}
	progress := trackOutput(head, "==> Downloading Codex CLI\n",
		"==> Installing standalone package to /tmp/home/.codex/packages/standalone/releases/0.166.0-aarch64-apple-darwin\n")
	if progress.Phase != ProgressInstalling {
		t.Fatalf("after the install step phase = %q, want installing", progress.Phase)
	}
}

func TestProgressTrackerReportsNoPercentForNPM(t *testing.T) {
	progress := trackOutput("npm warn deprecated glob@7.2.3: Glob versions prior to v9 are no longer supported\n")
	if progress.Phase != "" || progress.Percent != nil || progress.Detail == "" {
		t.Fatalf("npm = %+v, want its line with no phase and no percent", progress)
	}
	if progress = trackOutput("\n", "added 2 packages in 2m\n"); progress.Phase != ProgressInstalling || progress.Percent != nil {
		t.Fatalf("npm summary = %+v, want installing with no percent", progress)
	}
}

func TestProgressTrackerTruncatesDetail(t *testing.T) {
	progress := trackOutput(strings.Repeat("é", 400) + "\n")
	if got := len([]rune(progress.Detail)); got != maxProgressDetailRunes {
		t.Fatalf("detail runes = %d, want %d", got, maxProgressDetailRunes)
	}
}

func TestAgentInstallPublishesOutputAndProgressWhileRunning(t *testing.T) {
	store := newInstallJobStoreFake()
	s := newTestService("darwin", "npm")
	s.jobStore = store
	release := make(chan struct{})
	s.commands = commandRunnerFunc(func(_ context.Context, _ []string, stdout, _ io.Writer) error {
		_, _ = io.WriteString(stdout, "Downloading codex (60.0MiB)\n")
		_, _ = io.WriteString(stdout, "\r30.0%")
		<-release
		_, _ = io.WriteString(stdout, "\nadded 2 packages in 3s\n")
		return nil
	})
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.npm/bin/codex", Output: "codex 1.2.3\n"}, nil
	})

	if _, err := s.StartAgent(context.Background(), TargetCodex, "npm"); err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var running Job
	for {
		job, err := s.Status(context.Background(), TargetCodex)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if job.Progress != nil && job.Progress.Percent != nil {
			running = job
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatalf("no live progress while installing; last job = %+v", job)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if running.Status != StatusInstalling || *running.Progress.Percent != 30 || running.Progress.Phase != ProgressDownloading {
		close(release)
		t.Fatalf("running job = %+v progress = %+v", running, running.Progress)
	}
	if !strings.Contains(running.Output, "Downloading codex (60.0MiB)") {
		close(release)
		t.Fatalf("running output = %q, want the installer's output so far", running.Output)
	}
	close(release)
	waitForStatus(t, s, TargetCodex, StatusSucceeded)

	final, err := s.Status(context.Background(), TargetCodex)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if final.Progress != nil {
		t.Fatalf("final progress = %+v, want none once installing ends", final.Progress)
	}
	if strings.Count(final.Output, "Downloading codex") != 1 || !strings.Contains(final.Output, "added 2 packages") || !strings.Contains(final.Output, "codex 1.2.3") {
		t.Fatalf("final output = %q, want installer output once plus verifier output", final.Output)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, record := range store.history {
		if record.Status == string(StatusInstalling) && record.Output != "" {
			t.Fatalf("live output was persisted: %+v", record)
		}
	}
}

func TestAgentInstallPublishesNoPhaseWhileTheInstallerIsSilent(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.jobStore = newInstallJobStoreFake()
	release := make(chan struct{})
	s.commands = commandRunnerFunc(func(_ context.Context, _ []string, _, _ io.Writer) error {
		<-release
		return nil
	})
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.npm/bin/codex", Output: "codex 1.2.3\n"}, nil
	})

	if _, err := s.StartAgent(context.Background(), TargetCodex, "npm"); err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	// Past several publish ticks, so a default phase would have been published.
	time.Sleep(3 * progressPublishInterval)
	job, err := s.Status(context.Background(), TargetCodex)
	close(release)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if job.Status != StatusInstalling || job.Progress != nil {
		t.Fatalf("silent install = status %q progress %+v, want installing with no progress", job.Status, job.Progress)
	}
	waitForStatus(t, s, TargetCodex, StatusSucceeded)
}
