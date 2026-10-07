package systeminstall

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// UpdateStatus describes observed version availability, not whether AO knows
// an update command. Unknown is deliberately distinct from current.
type UpdateStatus string

const (
	// UpdateStatusUnknown means AO could not establish update availability.
	UpdateStatusUnknown UpdateStatus = "unknown"
	// UpdateStatusCurrent means the installed release matches the package source.
	UpdateStatusCurrent UpdateStatus = "current"
	// UpdateStatusBehindLatest means a newer package release is available.
	UpdateStatusBehindLatest UpdateStatus = "behind_latest"
)

// UpdateUnknownReason gives a stable diagnostic category when Status is unknown.
type UpdateUnknownReason string

const (
	// UpdateReasonOwnershipUnconfirmed means AO could not prove which installer owns the binary.
	UpdateReasonOwnershipUnconfirmed UpdateUnknownReason = "ownership_unconfirmed"
	// UpdateReasonUnsupportedSource means the harness has no supported release source.
	UpdateReasonUnsupportedSource UpdateUnknownReason = "unsupported_source"
	// UpdateReasonVersionUnparseable means a reported version could not be compared safely.
	UpdateReasonVersionUnparseable UpdateUnknownReason = "version_unparseable"
	// UpdateReasonChannelUnconfirmed means AO could not match the installed release channel.
	UpdateReasonChannelUnconfirmed UpdateUnknownReason = "channel_unconfirmed"
	// UpdateReasonLookupFailed means the latest-version lookup failed.
	UpdateReasonLookupFailed UpdateUnknownReason = "lookup_failed"
	// UpdateReasonBuildUnordered means both releases share a version but
	// carry different build identifiers that have no defined order.
	UpdateReasonBuildUnordered UpdateUnknownReason = "build_unordered"
)

// UpdateAdvisory is the daemon's non-mutating comparison for one harness.
type UpdateAdvisory struct {
	AgentID           string              `json:"agentId"`
	Status            UpdateStatus        `json:"status"`
	CurrentVersion    string              `json:"currentVersion,omitempty"`
	LatestVersion     string              `json:"latestVersion,omitempty"`
	Source            string              `json:"source,omitempty"`
	MaintenanceMethod string              `json:"maintenanceMethod,omitempty"`
	Reason            UpdateUnknownReason `json:"reason,omitempty"`
	CheckedAt         time.Time           `json:"checkedAt"`
}

type updateAdvisoryCall struct {
	done     chan struct{}
	advisory UpdateAdvisory
	err      error
}

// UpdateAdvisory probes the adapter-selected binary and checks the npm or
// Homebrew package that owns it, whether or not AO installed it. Binaries no
// package manager owns are compared with the vendor's release channel. A
// harness with neither, and failed probes, remain unknown; they never
// masquerade as up-to-date.
func (s *Service) UpdateAdvisory(ctx context.Context, target Target, refresh ...bool) (UpdateAdvisory, error) {
	if !IsAgentTarget(target) {
		return UpdateAdvisory{}, fmt.Errorf("systeminstall: unknown harness %q", target)
	}
	if err := ctx.Err(); err != nil {
		return UpdateAdvisory{}, err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	if len(refresh) > 0 && refresh[0] {
		delete(s.updateAdvisories, target)
		// An explicit click needs evidence gathered after the click. An older
		// in-flight read may finish for its callers, but may not cache over it.
		delete(s.updateAdvisoryCalls, target)
	}
	cached, found := s.updateAdvisories[target]
	if found {
		ttl := time.Hour
		if cached.Status == UpdateStatusUnknown {
			ttl = 5 * time.Minute
		}
		if now.Sub(cached.CheckedAt) < ttl {
			s.mu.Unlock()
			return cached, nil
		}
	}
	call := s.updateAdvisoryCalls[target]
	if call == nil {
		if s.stopping {
			s.mu.Unlock()
			return UpdateAdvisory{}, context.Canceled
		}
		call = &updateAdvisoryCall{done: make(chan struct{})}
		if s.updateAdvisoryCalls == nil {
			s.updateAdvisoryCalls = make(map[Target]*updateAdvisoryCall)
		}
		s.updateAdvisoryCalls[target] = call
		s.workers.Add(1)
		go s.runUpdateAdvisory(target, call)
	}
	s.mu.Unlock()
	select {
	case <-call.done:
		return call.advisory, call.err
	case <-ctx.Done():
		return UpdateAdvisory{}, ctx.Err()
	}
}

func (s *Service) runUpdateAdvisory(target Target, call *updateAdvisoryCall) {
	defer s.workers.Done()
	advisory, err := s.computeUpdateAdvisory(s.backgroundContext, target)
	s.mu.Lock()
	call.advisory, call.err = advisory, err
	if s.updateAdvisoryCalls[target] == call {
		delete(s.updateAdvisoryCalls, target)
		if err == nil && s.backgroundContext.Err() == nil {
			if s.updateAdvisories == nil {
				s.updateAdvisories = make(map[Target]UpdateAdvisory)
			}
			s.updateAdvisories[target] = advisory
		}
	}
	close(call.done)
	s.mu.Unlock()
}

func (s *Service) computeUpdateAdvisory(ctx context.Context, target Target) (UpdateAdvisory, error) {
	advisory := UpdateAdvisory{AgentID: string(target), Status: UpdateStatusUnknown, Reason: UpdateReasonUnsupportedSource, CheckedAt: time.Now().UTC()}
	job, err := s.Status(ctx, target)
	if err != nil {
		return advisory, err
	}
	if s.verifier == nil {
		return advisory, nil
	}
	var plans []Plan
	recordedMethod := ""
	if s.ownsInstallation != nil && s.managedVersion != nil {
		if planner, err := s.newRequestPlanner(ctx); err == nil {
			if job.Status == StatusSucceeded {
				recordedMethod = job.Method
			}
			plans = planner.agentMethodPlans(target, AgentOperationInstall)
		}
	}
	if len(plans) == 0 && s.officialVersion == nil {
		return advisory, nil
	}
	verified, err := s.verifier.Verify(ctx, target)
	if err != nil {
		advisory.Reason = UpdateReasonOwnershipUnconfirmed
		return advisory, nil //nolint:nilerr // An unverified binary cannot establish update availability.
	}
	layout := packageLayout(verified.ResolvedPath)
	sources := advisoryPackageSources(plans, recordedMethod, layout)
	// The binary's package-manager root decides the source, so harnesses the
	// user installed outside AO are covered as well as AO-installed ones.
	var source Plan
	for _, candidate := range sources {
		owned, err := s.ownsInstallation(ctx, verified.ResolvedPath, candidate.Method, packageWithoutLatest(candidate.Package), candidate.PackageCask)
		if err == nil && owned {
			source = candidate
			break
		}
	}
	if source.Package == "" && s.officialVersion == nil {
		if layout != "" {
			advisory.Reason = UpdateReasonOwnershipUnconfirmed
		}
		return advisory, nil
	}
	scheme := versionSchemeFor(target)
	current, ok := scheme.find(verified.Output)
	if !ok {
		advisory.Reason = UpdateReasonVersionUnparseable
		return advisory, nil
	}
	advisory.CurrentVersion = current.display
	if source.Package != "" {
		advisory.MaintenanceMethod = source.Method
	} else if nativeMaintenanceMethod(target, verified, current) != "" {
		advisory.MaintenanceMethod = "official-installer"
	}
	var latest string
	if source.Package != "" {
		advisory.Source = source.Method
		var result managedVersionResult
		result, err = s.managedVersion(ctx, source, current)
		latest = result.Latest
	} else {
		// No package manager provably owns the binary, so compare against the
		// vendor's own release channel, unless the binary sits in another
		// tool's layout whose channel may differ from the vendor's.
		official, ok := officialSourceFor(target, s.goos, runtime.GOARCH)
		if !ok {
			return advisory, nil
		}
		if !officialChannelFits(official.kind, layout) {
			advisory.Reason = UpdateReasonOwnershipUnconfirmed
			return advisory, nil
		}
		advisory.Source = officialReleaseSource
		if len(current.prerelease) != 0 {
			advisory.Reason = UpdateReasonChannelUnconfirmed
			return advisory, nil
		}
		latest, err = s.officialVersion(ctx, target)
	}
	if err != nil {
		if errors.Is(err, errUpdateChannelUnconfirmed) {
			advisory.Reason = UpdateReasonChannelUnconfirmed
		} else if errors.Is(err, errNoOfficialSource) {
			advisory.Reason = UpdateReasonUnsupportedSource
		} else {
			advisory.Reason = UpdateReasonLookupFailed
		}
		return advisory, nil //nolint:nilerr // A failed latest-version lookup is not an update verdict.
	}
	parsedLatest, ok := scheme.parse(latest)
	if !ok {
		advisory.Reason = UpdateReasonVersionUnparseable
		return advisory, nil
	}
	comparison, versionsComparable := scheme.compare(current, parsedLatest)
	if !versionsComparable {
		advisory.Reason = UpdateReasonChannelUnconfirmed
		if unorderedBuilds(current, parsedLatest) {
			advisory.Reason = UpdateReasonBuildUnordered
		}
		return advisory, nil
	}
	advisory.LatestVersion = parsedLatest.display
	switch {
	case comparison < 0:
		advisory.Status = UpdateStatusBehindLatest
		advisory.Reason = ""
	case comparison == 0:
		advisory.Status = UpdateStatusCurrent
		advisory.Reason = ""
	default:
		advisory.Reason = UpdateReasonChannelUnconfirmed
	}
	return advisory, nil
}

// A release lookup is not installation ownership. Only recognise the native
// Claude install's resolved, versioned payload here; generic PATH binaries and
// binaries inside package managers must not become maintenance candidates.
func nativeMaintenanceMethod(target Target, verified VerifyResult, current updateVersion) string {
	if target != TargetClaudeCode || packageLayout(verified.ResolvedPath) != "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(verified.ResolvedPath)
	if err != nil {
		return ""
	}
	path := strings.ReplaceAll(filepath.ToSlash(resolved), `\`, "/")
	if strings.HasSuffix(path, "/.local/share/claude/versions/"+current.display) {
		return "official-installer"
	}
	return ""
}
