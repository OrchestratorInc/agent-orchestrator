package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	trackergitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/gitlab"
	trackermulti "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/multi"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNewGitLabTracker_PassesAllowedHosts(t *testing.T) {
	t.Setenv("AO_GITLAB_TOKEN", "default-token")

	selfHost := "gitlab.internal.example"
	cfg := config.GitLabConfig{
		AllowedHosts: []string{selfHost},
	}

	tracker, err := newGitLabTracker(cfg)
	if err != nil {
		t.Fatalf("newGitLabTracker: %v", err)
	}

	glTracker, ok := tracker.(*trackergitlab.Tracker)
	if !ok {
		t.Fatalf("expected *trackergitlab.Tracker, got %T", tracker)
	}

	if err := glTracker.ConfigForHost(selfHost); err != nil {
		t.Fatalf("allowlisted host %q was rejected by the tracker: %v", selfHost, err)
	}

	err = glTracker.ConfigForHost("gitlab.evil.example")
	if !errors.Is(err, trackergitlab.ErrHostNotAllowed) {
		t.Fatalf("unconfigured host should be rejected with ErrHostNotAllowed, got: %v", err)
	}
}

func TestNewGitLabTracker_GitLabComStillWorks(t *testing.T) {
	t.Setenv("AO_GITLAB_TOKEN", "default-token")

	cfg := config.GitLabConfig{}
	tracker, err := newGitLabTracker(cfg)
	if err != nil {
		t.Fatalf("newGitLabTracker: %v", err)
	}

	glTracker, ok := tracker.(*trackergitlab.Tracker)
	if !ok {
		t.Fatalf("expected *trackergitlab.Tracker, got %T", tracker)
	}

	if err := glTracker.ConfigForHost(""); err != nil {
		t.Fatalf("gitlab.com (Host: \"\") should not be rejected: %v", err)
	}
}

func TestNewGitLabTracker_HostTokensRoutedCorrectly(t *testing.T) {
	t.Setenv("AO_GITLAB_TOKEN", "default-token")

	selfHost := "gitlab.internal.example"
	cfg := config.GitLabConfig{
		AllowedHosts: []string{selfHost},
		HostTokens: map[string]string{
			selfHost: "self-host-token",
		},
	}

	tracker, err := newGitLabTracker(cfg)
	if err != nil {
		t.Fatalf("newGitLabTracker: %v", err)
	}

	glTracker, ok := tracker.(*trackergitlab.Tracker)
	if !ok {
		t.Fatalf("expected *trackergitlab.Tracker, got %T", tracker)
	}

	if err := glTracker.ConfigForHost(selfHost); err != nil {
		t.Fatalf("self-managed host %q with HostTokens was rejected: %v", selfHost, err)
	}

	if err := glTracker.ConfigForHost(""); err != nil {
		t.Fatalf("gitlab.com should not be rejected: %v", err)
	}

	if err := glTracker.ConfigForHost("gitlab.attacker.example"); err == nil {
		t.Fatalf("unconfigured host should be rejected")
	}
}

func TestNewGitLabTracker_HostTokensCaseInsensitive(t *testing.T) {
	t.Setenv("AO_GITLAB_TOKEN", "default-token")

	selfHost := "GitLab.Internal.Example"
	cfg := config.GitLabConfig{
		AllowedHosts: []string{selfHost},
		HostTokens: map[string]string{
			selfHost: "self-host-token",
		},
	}

	tracker, err := newGitLabTracker(cfg)
	if err != nil {
		t.Fatalf("newGitLabTracker: %v", err)
	}

	glTracker, ok := tracker.(*trackergitlab.Tracker)
	if !ok {
		t.Fatalf("expected *trackergitlab.Tracker, got %T", tracker)
	}

	if err := glTracker.ConfigForHost("gitlab.internal.example"); err != nil {
		t.Fatalf("mixed-case config host should be accepted after lowercasing: %v", err)
	}
}

func TestNewGitLabTracker_UnconfiguredHostRejected(t *testing.T) {
	t.Setenv("AO_GITLAB_TOKEN", "default-token")

	cfg := config.GitLabConfig{
		AllowedHosts: []string{"gitlab.internal.example"},
	}
	tracker, err := newGitLabTracker(cfg)
	if err != nil {
		t.Fatalf("newGitLabTracker: %v", err)
	}

	glTracker, ok := tracker.(*trackergitlab.Tracker)
	if !ok {
		t.Fatalf("expected *trackergitlab.Tracker, got %T", tracker)
	}

	err = glTracker.ConfigForHost("gitlab.attacker.example")
	if !errors.Is(err, trackergitlab.ErrHostNotAllowed) {
		t.Fatalf("unconfigured host should be rejected with ErrHostNotAllowed, got: %v", err)
	}

	if err := glTracker.ConfigForHost("gitlab.internal.example"); err != nil {
		t.Fatalf("configured host should be accepted: %v", err)
	}

	if err := glTracker.ConfigForHost(""); err != nil {
		t.Fatalf("gitlab.com should not be rejected: %v", err)
	}
}

func TestNewMultiTracker_WithGitLabConfig(t *testing.T) {
	t.Setenv("AO_GITLAB_TOKEN", "default-token")
	t.Setenv("AO_GITHUB_TOKEN", "")

	selfHost := "gitlab.internal.example"
	cfg := config.GitLabConfig{
		AllowedHosts: []string{selfHost},
		HostTokens: map[string]string{
			selfHost: "self-host-token",
		},
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tracker := newMultiTracker(cfg, log)
	if tracker == nil {
		t.Fatal("newMultiTracker = nil, want non-nil when GitLab token is available")
	}

	_, err := tracker.Get(context.Background(), domain.TrackerID{
		Provider: domain.TrackerProviderGitLab,
		Native:   "group/project#1",
		Host:     "gitlab.attacker.example",
	})
	if !errors.Is(err, trackergitlab.ErrHostNotAllowed) {
		t.Fatalf("multi-tracker Get with unconfigured host: err = %v, want ErrHostNotAllowed", err)
	}

	mt, ok := tracker.(*trackermulti.Tracker)
	if !ok {
		t.Fatalf("expected *trackermulti.Tracker, got %T", tracker)
	}
	_ = mt
}

type stubTracker struct{}

func (stubTracker) Get(context.Context, domain.TrackerID) (domain.Issue, error) {
	return domain.Issue{}, nil
}

func (stubTracker) List(context.Context, domain.TrackerRepo, domain.ListFilter) ([]domain.Issue, error) {
	return nil, nil
}

func (stubTracker) Preflight(context.Context) error { return nil }

var _ ports.Tracker = stubTracker{}

func TestLazyTracker_ConstructionDeferredAndStickyOnlyOnSuccess(t *testing.T) {
	builds := 0
	succeed := false
	lt := &lazyTracker{
		name:   "github",
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		build: func() (ports.Tracker, error) {
			builds++
			if !succeed {
				return nil, errors.New("no token")
			}
			return stubTracker{}, nil
		},
	}

	if builds != 0 {
		t.Fatal("build ran before first tracker use")
	}
	if err := lt.Preflight(context.Background()); err == nil {
		t.Fatal("Preflight err = nil, want build failure")
	}
	if builds != 1 {
		t.Fatalf("builds = %d, want 1", builds)
	}
	if err := lt.Preflight(context.Background()); err == nil {
		t.Fatal("Preflight err = nil, want build failure")
	}
	if builds != 2 {
		t.Fatalf("builds = %d, want 2 (failures must not be sticky)", builds)
	}

	succeed = true
	if err := lt.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if builds != 3 {
		t.Fatalf("builds = %d, want 3", builds)
	}
	if err := lt.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if builds != 3 {
		t.Fatalf("builds = %d, want 3 (a successful construction must be reused)", builds)
	}
}

func TestWiring_NewMultiTracker_DoesNotProbeGHCLIAtBoot(t *testing.T) {
	t.Setenv("AO_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("AO_GITLAB_TOKEN", "")
	t.Setenv("GITLAB_TOKEN", "")

	marker := filepath.Join(t.TempDir(), "gh-called")
	t.Setenv("GH_MARKER", marker)
	binDir := t.TempDir()
	writeRecordingGHExecutable(t, binDir)
	t.Setenv("PATH", binDir)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tracker := newMultiTracker(config.GitLabConfig{}, log)
	if tracker == nil {
		t.Fatal("newMultiTracker = nil, want non-nil: the GitHub slot is lazily constructed and always present")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("gh CLI invoked during newMultiTracker (marker exists); stat: %v", err)
	}

	_, _ = tracker.Get(context.Background(), domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "acme/demo#1"})
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("gh CLI not invoked on first tracker use; stat marker: %v", err)
	}
}

func writeRecordingGHExecutable(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		content := "@echo off\r\necho called >> %GH_MARKER%\r\nexit /b 1\r\n"
		if err := os.WriteFile(filepath.Join(dir, "gh.cmd"), []byte(content), 0o755); err != nil {
			t.Fatalf("write fake gh.cmd: %v", err)
		}
		return
	}
	content := "#!/bin/sh\necho called >> \"$GH_MARKER\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(content), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
}
