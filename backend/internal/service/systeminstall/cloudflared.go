package systeminstall

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const cloudflaredVersion = "2026.10.2"

var PinnedCloudflared, _ = mobilebridge.ParseCloudflaredVersion(cloudflaredVersion)

const (
	cloudflaredReleaseURL     = "https://github.com/cloudflare/cloudflared/releases/download/" + cloudflaredVersion + "/"
	cloudflaredDownloadMethod = "download"
)

var cloudflaredDownloads = map[string]ports.ManagedBinaryDownload{
	"darwin/amd64": {
		URL:           cloudflaredReleaseURL + "cloudflared-darwin-amd64.tgz",
		SHA256:        "b64efd5a2772fc124b855f60eb3d33ba1a4f2ba67b19ddc58d32b705d42b82c9",
		ArchiveMember: "cloudflared",
	},
	"darwin/arm64": {
		URL:           cloudflaredReleaseURL + "cloudflared-darwin-arm64.tgz",
		SHA256:        "529a07089ac2d46762578389230fa148e5cb6ce2689200fc006956864042d17a",
		ArchiveMember: "cloudflared",
	},
	"linux/amd64": {
		URL:    cloudflaredReleaseURL + "cloudflared-linux-amd64",
		SHA256: "35fbdd2beb378436c7da377d7358cea8e33f9c94a6705d1d8036bf9b0d6d8005",
	},
	"linux/arm64": {
		URL:    cloudflaredReleaseURL + "cloudflared-linux-arm64",
		SHA256: "b7e55b4d9702c2ab5f8348f780bb1db044527fc555367714c78e0595750a4665",
	},
	"windows/amd64": {
		URL:    cloudflaredReleaseURL + "cloudflared-windows-amd64.exe",
		SHA256: "12c7c887f8aaab779aef1dfa9b60673530a5adb88ecc55dd6df8f56e0b70f199",
	},
}

var cloudflaredDownloadWaits = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}

func (s *Service) DownloadCloudflaredInBackground(onInstalled func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onCloudflaredInstalled = onInstalled
	if s.cloudflaredAutoStarted {
		return
	}
	if job, ok := s.jobs[TargetCloudflared]; ok && job.Status == StatusRunning {
		return
	}
	if plan, ok := s.cloudflaredDownloadPlan(); ok {
		s.cloudflaredAutoStarted = true
		s.startPlanLocked(TargetCloudflared, plan)
	}
}

func (s *Service) CloudflaredInstalling() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[TargetCloudflared]
	return ok && job.Status == StatusRunning
}

func (s *Service) cloudflaredDownloadPlan() (Plan, bool) {
	download, ok := cloudflaredDownloads[s.goos+"/"+s.goarch]
	if !ok || s.managedBinaries == nil {
		return Plan{}, false
	}
	download.Name = "cloudflared"
	if s.goos == "windows" {
		download.Name += ".exe"
	}
	return Plan{Target: TargetCloudflared, Method: cloudflaredDownloadMethod, download: &download}, true
}

func (s *Service) runCloudflaredDownload(parent context.Context, download ports.ManagedBinaryDownload, job *Job) {
	var path string
	var err error
	ctx, cancel := context.WithTimeout(parent, s.installTimeout)
	defer cancel()
	for try := 0; ; try++ {
		path, err = s.managedBinaries.InstallManagedBinary(ctx, download)
		if err == nil || ctx.Err() != nil || errors.Is(err, ports.ErrDownloadRejected) || try >= len(s.downloadWaits) {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(s.downloadWaits[try]):
		}
	}
	if err == nil && parent.Err() == nil {
		s.mu.Lock()
		onInstalled := s.onCloudflaredInstalled
		s.mu.Unlock()
		if onInstalled != nil {
			onInstalled()
		}
	}
	now := time.Now()

	s.mu.Lock()
	job.FinishedAt = &now
	if parent.Err() != nil {
		job.Status = StatusInterrupted
		job.Error = "daemon shutdown interrupted the install"
		s.mu.Unlock()
		return
	}
	if err != nil {
		s.cloudflaredDownloadFailed.Store(true)
		s.cloudflaredAutoStarted = errors.Is(err, ports.ErrDownloadRejected)
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Could not download cloudflared %s: %v", cloudflaredVersion, err)
		s.mu.Unlock()
		return
	}
	job.Status = StatusSucceeded
	s.cloudflaredDownloadFailed.Store(false)
	job.ExpectedDestination = path
	job.Output = fmt.Sprintf("cloudflared %s installed at %s (sha256 %s verified)", cloudflaredVersion, path, download.SHA256)
	onSucceeded := s.onSucceeded
	s.mu.Unlock()
	if onSucceeded != nil {
		onSucceeded(TargetCloudflared)
	}
}
