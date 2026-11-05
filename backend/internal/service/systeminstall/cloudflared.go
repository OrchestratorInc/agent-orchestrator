package systeminstall

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// cloudflaredVersion is the connector release AO downloads itself, the same
// one the macOS app bundles (frontend/scripts/prepare-cloudflared.mjs): the
// runner reads its log format, so one release runs everywhere. Bumping it
// means re-pinning every digest here and there; a test holds the two together.
const cloudflaredVersion = "2026.10.2"

// PinnedCloudflared is cloudflaredVersion parsed, for checking a managed copy.
var PinnedCloudflared, _ = mobilebridge.ParseCloudflaredVersion(cloudflaredVersion)

const (
	cloudflaredReleaseURL     = "https://github.com/cloudflare/cloudflared/releases/download/" + cloudflaredVersion + "/"
	cloudflaredDownloadMethod = "download"
)

// cloudflaredDownloads pins one asset per platform, keyed by GOOS/GOARCH. Each
// digest is the SHA-256 of the asset as served, GitHub's asset digest. The
// Linux and Windows values equal the checksums in Cloudflare's release notes;
// for the darwin .tgz assets the notes list the extracted binary instead
// (4be2a4c0… amd64, 823f993e… arm64), and the archive digests equal the macOS
// bundle's.
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

// cloudflaredDownloadWaits spaces the tries of a failing download. A laptop
// that turns remote access back on at boot can ask before its network is up.
var cloudflaredDownloadWaits = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}

// DownloadCloudflaredInBackground starts AO's pinned cloudflared download when
// remote access finds no usable connector, and calls onInstalled once the
// binary has landed. Later calls do nothing while that download runs or once
// it landed, so a connector that keeps resolving as missing cannot become a
// download loop. A download that failed every try is started again by the
// next call, which only a user action or a daemon start makes, unless it got
// the wrong bytes.
func (s *Service) DownloadCloudflaredInBackground(onInstalled func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onCloudflaredInstalled = onInstalled
	if s.cloudflaredAutoStarted {
		return
	}
	if job, ok := s.jobs[TargetCloudflared]; ok && job.Status == StatusRunning {
		return // An install the user started reports to onInstalled too.
	}
	// Only the download runs unasked. A package manager can prompt, update
	// itself first or need root, so that stays behind the desktop's button.
	if plan, ok := s.cloudflaredDownloadPlan(); ok {
		s.cloudflaredAutoStarted = true
		s.startPlanLocked(TargetCloudflared, plan)
	}
}

// CloudflaredInstalling reports whether an install of cloudflared is running,
// started by remote access or by the user; the connector starts when it lands.
func (s *Service) CloudflaredInstalling() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[TargetCloudflared]
	return ok && job.Status == StatusRunning
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
	// Every try fits in one install timeout, so a slow link cannot keep the
	// job running for an hour.
	ctx, cancel := context.WithTimeout(parent, s.installTimeout)
	defer cancel()
	for try := 0; ; try++ {
		path, err = s.managedBinaries.InstallManagedBinary(ctx, download)
		// A rejected download, the wrong bytes, would be the same again.
		if err == nil || ctx.Err() != nil || errors.Is(err, ports.ErrDownloadRejected) || try >= len(s.downloadWaits) {
			break
		}
		select {
		case <-ctx.Done():
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
		// Wrong bytes would be the same again, so remote access does not
		// download them again; the user's Install offers the package manager.
		s.cloudflaredAutoStarted = errors.Is(err, ports.ErrDownloadRejected)
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Could not download cloudflared %s: %v", cloudflaredVersion, err)
		s.mu.Unlock()
		return
	}
	job.Status = StatusSucceeded
	// The download works again, so it is what the user's next Start offers.
	s.cloudflaredDownloadFailed.Store(false)
	job.ExpectedDestination = path
	job.Output = fmt.Sprintf("cloudflared %s installed at %s (sha256 %s verified)", cloudflaredVersion, path, download.SHA256)
	onSucceeded := s.onSucceeded
	s.mu.Unlock()
	if onSucceeded != nil {
		onSucceeded(TargetCloudflared)
	}
}
