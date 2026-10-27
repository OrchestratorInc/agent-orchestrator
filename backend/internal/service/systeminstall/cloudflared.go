package systeminstall

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// cloudflaredVersion is the connector release AO downloads itself. Bumping it
// means re-pinning every digest in cloudflaredDownloads.
const cloudflaredVersion = "2026.10.3"

const (
	cloudflaredReleaseURL     = "https://github.com/cloudflare/cloudflared/releases/download/" + cloudflaredVersion + "/"
	cloudflaredDownloadMethod = "download"
)

// cloudflaredDownloads pins one asset per platform, keyed by GOOS/GOARCH. Each
// digest is the SHA-256 of the asset as served: streamed from the release and
// matched against GitHub's asset digest. The Linux and Windows values equal
// the checksums in Cloudflare's release notes; for the darwin .tgz assets the
// notes list the extracted binary instead (947e2942… amd64, f249139e… arm64),
// which is what these archives were checked to contain.
var cloudflaredDownloads = map[string]ports.ManagedBinaryDownload{
	"darwin/amd64": {
		URL:           cloudflaredReleaseURL + "cloudflared-darwin-amd64.tgz",
		SHA256:        "81b7b85f2376815c4f59b1aa323887b62e16ce8b67555c2e1577dc23f2a82000",
		ArchiveMember: "cloudflared",
	},
	"darwin/arm64": {
		URL:           cloudflaredReleaseURL + "cloudflared-darwin-arm64.tgz",
		SHA256:        "1165d14b6f6360a3b8c283d7d1100024d6ec7cd9bc9e32519e96150645f3ec55",
		ArchiveMember: "cloudflared",
	},
	"linux/amd64": {
		URL:    cloudflaredReleaseURL + "cloudflared-linux-amd64",
		SHA256: "053b80b0a23031a038fdbf3c073f59f82b8f1f152aa67971d7383ffafc0dbab0",
	},
	"linux/arm64": {
		URL:    cloudflaredReleaseURL + "cloudflared-linux-arm64",
		SHA256: "89297ebfb9e8c99c0dc013a1140601453cf3798d8306c5591651f4a3f5210be8",
	},
	"windows/amd64": {
		URL:    cloudflaredReleaseURL + "cloudflared-windows-amd64.exe",
		SHA256: "76b2805253d0d6ffacb092a7acfbee99e7af6917835c0c5410e23f3ef5052da2",
	},
}

// cloudflaredDownloadWaits spaces the tries of a failing download. A laptop
// that turns remote access back on at boot can ask before its network is up.
var cloudflaredDownloadWaits = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}

// DownloadCloudflaredInBackground starts AO's pinned cloudflared download when
// remote access finds no usable connector, and calls onInstalled once the
// binary has landed. Later calls do nothing while that download runs or once
// it landed, so a connector that keeps resolving as missing cannot become a
// download loop. A download that failed every try is started again by the
// next call, which only a user action or a daemon start makes.
func (s *Service) DownloadCloudflaredInBackground(onInstalled func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cloudflaredAutoStarted {
		return
	}
	s.cloudflaredAutoStarted = true
	s.onCloudflaredInstalled = onInstalled
	if job, ok := s.jobs[TargetCloudflared]; ok && job.Status == StatusRunning {
		return // A download the user started reports to onInstalled too.
	}
	// Only the download runs unasked. A package manager can prompt, update
	// itself first or need root, so that stays behind the desktop's button.
	if plan, ok := s.cloudflaredDownloadPlan(); ok {
		s.startPlanLocked(TargetCloudflared, plan)
	}
}

// cloudflaredDownloadPlan is the pinned download for this platform, when AO
// has one and a downloader.
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

// runCloudflaredDownload installs the pinned connector and records the
// outcome onto job, like run, trying again after each of downloadWaits. A
// download that failed every try switches planCloudflared to the
// package-manager fallback, so the user's next Start offers that instead of
// the same download.
func (s *Service) runCloudflaredDownload(parent context.Context, download ports.ManagedBinaryDownload, job *Job) {
	var path string
	var err error
	for try := 0; ; try++ {
		ctx, cancel := context.WithTimeout(parent, s.installTimeout)
		path, err = s.managedBinaries.InstallManagedBinary(ctx, download)
		cancel()
		if err == nil || parent.Err() != nil || try >= len(s.downloadWaits) {
			break
		}
		select {
		case <-parent.Done():
		case <-time.After(s.downloadWaits[try]):
		}
	}
	if err == nil && parent.Err() == nil {
		// The connector starts before the job reads as done, so a client that
		// sees the download finish already sees the connector.
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
		s.cloudflaredAutoStarted = false
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Could not download cloudflared %s: %v", cloudflaredVersion, err)
		s.mu.Unlock()
		return
	}
	job.Status = StatusSucceeded
	job.ExpectedDestination = path
	job.Output = fmt.Sprintf("cloudflared %s installed at %s (sha256 %s verified)", cloudflaredVersion, path, download.SHA256)
	onSucceeded := s.onSucceeded
	s.mu.Unlock()
	if onSucceeded != nil {
		onSucceeded(TargetCloudflared)
	}
}
