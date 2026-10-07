package systeminstall

import (
	"context"
	"errors"
	"fmt"
	"runtime"
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
	// UpdateReasonDisabled means update checks are turned off
	// (AO_HARNESS_UPDATE_CHECKS=off).
	UpdateReasonDisabled UpdateUnknownReason = "disabled"
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
	if s.updateChecksDisabled {
		return UpdateAdvisory{AgentID: string(target), Status: UpdateStatusUnknown, Reason: UpdateReasonDisabled, CheckedAt: now}, nil
	}
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
	var planner requestPlanner
	plannerReady := false
	recordedMethod := ""
	if s.ownsInstallation != nil && s.managedVersion != nil {
		if built, err := s.newRequestPlanner(ctx); err == nil {
			planner, plannerReady = built, true
			if job.Status == StatusSucceeded {
				recordedMethod = job.Method
			}
			plans = built.agentMethodPlans(target, AgentOperationInstall)
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
	if plannerReady {
		s.refreshDerivedOwner(ctx, planner, target, verified.ResolvedPath, &source)
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
	} else if vendorMaintainable(target, verified.ResolvedPath) {
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
		if !officialChannelFits(official.kind, layout) && !(npmInstallerTargets[target] && layout == layoutNPM) {
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

// vendorMaintainable reports whether the harness can update the binary
// sessions run through its own self-update command. A release lookup is not
// installation ownership: binaries inside a package manager or version manager
// belong to that tool, and harnesses without a self-update command have no
// vendor maintenance path. The command runs from the resolved binary itself,
// so it updates that copy however the user placed it.
func vendorMaintainable(target Target, resolvedPath string) bool {
	_, ok := vendorUpdateCommands[target]
	return ok && resolvedPath != "" && vendorLayout(target, packageLayout(resolvedPath))
}

// vendorLayout reports whether a binary in layout belongs to the vendor
// installer: outside every package tool, or in npm's layout for installers
// that install their own release tarball through npm.
func vendorLayout(target Target, layout string) bool {
	return layout == "" || npmInstallerTargets[target] && layout == layoutNPM
}

// refreshDerivedOwner reads the owner from the binary's own location when no
// listed method owns it, and keeps it only once the package manager confirms
// the package installed that binary. A derived npm owner is never kept for a
// harness whose vendor installer uses npm with a tarball the registry lacks.
func (s *Service) refreshDerivedOwner(ctx context.Context, planner requestPlanner, target Target, binaryPath string, source *Plan) {
	owner, ok := deriveOwnerFromPath(binaryPath)
	if !ok || owner.method == "npm" && npmInstallerTargets[target] {
		s.setDerivedOwner(target, nil)
		return
	}
	if source.Package != "" {
		if source.Method == owner.method && packageWithoutLatest(source.Package) == owner.pkg && source.PackageCask == owner.cask {
			s.setDerivedOwner(target, &owner)
		} else {
			s.setDerivedOwner(target, nil)
		}
		return
	}
	plan, ok := planner.derivedOwnerPlan(target, owner)
	if !ok || plan.Package == "" {
		s.setDerivedOwner(target, nil)
		return
	}
	if owned, err := s.ownsInstallation(ctx, binaryPath, owner.method, owner.pkg, owner.cask); err != nil || !owned {
		s.setDerivedOwner(target, nil)
		return
	}
	*source = plan
	s.setDerivedOwner(target, &owner)
}
