package systeminstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type UpdateStatus string

const (
	UpdateStatusUnknown       UpdateStatus = "unknown"
	UpdateStatusCurrent       UpdateStatus = "current"
	UpdateStatusBehindLatest  UpdateStatus = "behind_latest"
	UpdateStatusAheadOfLatest UpdateStatus = "ahead_of_latest"
)

type UpdateUnknownReason string

const (
	UpdateReasonOwnershipUnconfirmed UpdateUnknownReason = "ownership_unconfirmed"
	UpdateReasonUnsupportedSource    UpdateUnknownReason = "unsupported_source"
	UpdateReasonVersionUnparseable   UpdateUnknownReason = "version_unparseable"
	UpdateReasonChannelUnconfirmed   UpdateUnknownReason = "channel_unconfirmed"
	UpdateReasonLookupFailed         UpdateUnknownReason = "lookup_failed"
	UpdateReasonDisabled             UpdateUnknownReason = "disabled"
	UpdateReasonBuildUnordered       UpdateUnknownReason = "build_unordered"
)

type UpdateAdvisory struct {
	AgentID           string              `json:"agentId"`
	Status            UpdateStatus        `json:"status" enum:"unknown,current,behind_latest,ahead_of_latest"`
	BinaryPath        string              `json:"binaryPath,omitempty"`
	CurrentVersion    string              `json:"currentVersion,omitempty"`
	LatestVersion     string              `json:"latestVersion,omitempty"`
	Source            string              `json:"source,omitempty"`
	MaintenanceMethod string              `json:"maintenanceMethod,omitempty"`
	Reason            UpdateUnknownReason `json:"reason,omitempty" enum:"ownership_unconfirmed,unsupported_source,version_unparseable,channel_unconfirmed,lookup_failed,disabled,build_unordered"`
	CheckedAt         time.Time           `json:"checkedAt"`
	InUse             *UpdateInUse        `json:"inUse,omitempty"`

	maintenanceCask    bool
	maintenancePackage string
	binaryIdentity     binaryIdentity
	probeFailed        bool
}

const maxConcurrentUpdateChecks = 2

const lookupFailedRetryAfter = 30 * time.Minute

type updateAdvisoryCall struct {
	done     chan struct{}
	advisory UpdateAdvisory
	err      error
}

func (s *Service) UpdateAdvisory(ctx context.Context, target Target, refresh bool) (UpdateAdvisory, error) {
	advisory, err := s.releaseAdvisory(ctx, target, refresh)
	if err != nil {
		return advisory, err
	}
	advisory.InUse = s.updateInUse(ctx, target, advisory)
	return advisory, nil
}

func (s *Service) releaseAdvisory(ctx context.Context, target Target, refresh bool) (UpdateAdvisory, error) {
	if !IsAgentTarget(target) {
		return UpdateAdvisory{}, fmt.Errorf("systeminstall: unknown harness %q", target)
	}
	if err := ctx.Err(); err != nil {
		return UpdateAdvisory{}, err
	}
	now := time.Now().UTC()
	if s.updateChecksDisabled {
		return UpdateAdvisory{AgentID: string(target), Status: UpdateStatusUnknown, Reason: UpdateReasonDisabled, CheckedAt: now, BinaryPath: s.advisoryBinaryPath(ctx, target)}, nil
	}
	s.mu.Lock()
	if cached, found := s.updateAdvisories[target]; found && !refresh && !lookupRetryDue(cached, now) {
		s.mu.Unlock()
		return cached, nil
	}
	call := s.updateAdvisoryCalls[target]
	if refresh {
		call = nil
	}
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
		if s.updateCheckSlots == nil {
			s.updateCheckSlots = make(chan struct{}, maxConcurrentUpdateChecks)
		}
		s.workers.Add(1)
		go s.runUpdateAdvisory(target, call, s.updateCheckSlots)
	}
	s.mu.Unlock()
	select {
	case <-call.done:
		return call.advisory, call.err
	case <-ctx.Done():
		return UpdateAdvisory{}, ctx.Err()
	}
}

func lookupRetryDue(cached UpdateAdvisory, now time.Time) bool {
	return (cached.Reason == UpdateReasonLookupFailed || cached.probeFailed) && now.Sub(cached.CheckedAt) >= lookupFailedRetryAfter
}

func (s *Service) runUpdateAdvisory(target Target, call *updateAdvisoryCall, slots chan struct{}) {
	defer s.workers.Done()
	var advisory UpdateAdvisory
	var err error
	select {
	case slots <- struct{}{}:
		advisory, err = s.computeUpdateAdvisory(s.backgroundContext, target)
		<-slots
	case <-s.backgroundContext.Done():
		err = s.backgroundContext.Err()
	}
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
		advisory.BinaryPath = s.advisoryBinaryPath(ctx, target)
		return advisory, nil
	}
	verified, err := s.verifier.Verify(ctx, target)
	advisory.BinaryPath = displayBinaryPath(verified.ResolvedPath)
	advisory.binaryIdentity = identifyBinary(verified.ResolvedPath)
	if err != nil {
		advisory.Reason = UpdateReasonOwnershipUnconfirmed
		advisory.probeFailed = true
		return advisory, nil //nolint:nilerr // An unverified binary cannot establish update availability.
	}
	layout := packageLayout(verified.ResolvedPath)
	sources := advisoryPackageSources(plans, recordedMethod, layout)
	var source Plan
	managedCopy := s.isManagedPath(verified.ResolvedPath)
	managedOfficial := false
	if record, ok := s.readManagedRecord(target); ok && managedCopy {
		managedOfficial = record.Method == "official-installer"
		for _, candidate := range plans {
			if candidate.Method == record.Method && packageWithoutLatest(candidate.Package) == record.Package {
				source = candidate
				break
			}
		}
	}
	for _, candidate := range sources {
		if source.Package != "" {
			break
		}
		owned, err := s.ownsInstallation(ctx, verified.ResolvedPath, candidate.Method, packageWithoutLatest(candidate.Package), candidate.PackageCask)
		if err == nil && owned {
			source = candidate
			break
		}
	}
	if plannerReady && !managedCopy {
		s.refreshDerivedOwner(ctx, planner, target, verified.ResolvedPath, &source)
	}
	if source.Package == "" && s.officialVersion == nil {
		if layout != "" {
			advisory.Reason = UpdateReasonOwnershipUnconfirmed
		}
		return advisory, nil
	}
	scheme := versionSchemeFor(target)
	current, ok := scheme.find(installedVersionText(target, verified.ResolvedPath, verified.Output))
	if !ok {
		advisory.Reason = UpdateReasonVersionUnparseable
		advisory.probeFailed = true
		return advisory, nil
	}
	advisory.CurrentVersion = current.display
	if source.Package != "" {
		advisory.MaintenanceMethod = source.Method
		advisory.maintenanceCask = source.PackageCask
		advisory.maintenancePackage = source.Package
	} else if managedOfficial || verified.ResolvedPath != "" && vendorLayout(target, layout) && s.officialManagedUpdate(plans) {
		advisory.MaintenanceMethod = "official-installer"
	} else if home, _ := os.UserHomeDir(); vendorMaintainable(s.goos, target, verified.ResolvedPath, home) {
		advisory.MaintenanceMethod = "official-installer"
	}
	var latest string
	if source.Package != "" {
		advisory.Source = source.Method
		latest, err = s.managedVersion(ctx, source)
	} else {
		official, ok := officialSourceFor(target, s.goos, runtime.GOARCH)
		if !ok {
			return advisory, nil
		}
		if !officialChannelFits(official.kind, layout) && (!npmInstallerTargets[target] || layout != layoutNPM) {
			advisory.Reason = UpdateReasonOwnershipUnconfirmed
			return advisory, nil
		}
		advisory.Source = officialReleaseSource
		latest, err = s.officialVersion(ctx, target)
	}
	if err != nil {
		if errors.Is(err, errLatestUnparseable) {
			advisory.Reason = UpdateReasonVersionUnparseable
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
		advisory.Status = UpdateStatusAheadOfLatest
		advisory.Reason = ""
	}
	return advisory, nil
}

func vendorMaintainable(goos string, target Target, resolvedPath, home string) bool {
	_, ok := vendorUpdateCommands[target]
	if installerUpdateTargets[target] {
		_, ok = kimiInstallRoot(goos, resolvedPath, home)
	}
	return ok && resolvedPath != "" && vendorLayout(target, packageLayout(resolvedPath))
}

func vendorLayout(target Target, layout string) bool {
	return layout == "" || npmInstallerTargets[target] && layout == layoutNPM
}

func (s *Service) refreshDerivedOwner(ctx context.Context, planner requestPlanner, target Target, binaryPath string, source *Plan) {
	owner, ok := deriveOwnerFromPath(binaryPath)
	if !ok || owner.method == "npm" && npmInstallerTargets[target] {
		s.setDerivedOwner(target, nil)
		return
	}
	if source.Package != "" {
		if source.Method == owner.method && packageWithoutLatest(source.Package) == owner.pkg && source.PackageCask == owner.cask {
			s.setDerivedOwner(target, &owner)
			return
		}
		if source.Method != owner.method {
			s.setDerivedOwner(target, nil)
			return
		}
		*source = Plan{}
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

func (s *Service) advisoryBinaryPath(ctx context.Context, target Target) string {
	resolver, ok := s.verifier.(interface {
		Resolve(context.Context, Target) (string, error)
	})
	if !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, defaultVerifyTimeout)
	defer cancel()
	path, err := resolver.Resolve(ctx, target)
	if err != nil {
		return ""
	}
	return displayBinaryPath(path)
}

func displayBinaryPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && path != "" {
		if rel, err := filepath.Rel(home, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.Join("~", rel)
		}
	}
	return path
}

type binaryIdentity struct {
	path     string
	resolved string
	size     int64
	modTime  int64
}

func identifyBinary(path string) binaryIdentity {
	if path == "" {
		return binaryIdentity{}
	}
	identity := binaryIdentity{path: path}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return identity
	}
	identity.resolved = resolved
	if info, err := os.Stat(resolved); err == nil {
		identity.size = info.Size()
		identity.modTime = info.ModTime().UnixNano()
	}
	return identity
}

func (s *Service) forgetReplacedBinary(target Target) {
	s.mu.Lock()
	advisory, ok := s.updateAdvisories[target]
	s.mu.Unlock()
	if !ok || advisory.binaryIdentity.path == "" {
		return
	}
	if identifyBinary(advisory.binaryIdentity.path) == advisory.binaryIdentity {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.updateAdvisories[target]; ok && current.binaryIdentity == advisory.binaryIdentity {
		delete(s.updateAdvisories, target)
		delete(s.derivedOwners, target)
	}
}
