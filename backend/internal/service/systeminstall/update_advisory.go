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
	// UpdateStatusAheadOfLatest means the installed release is newer than the
	// published latest, e.g. a prerelease. Updates install latest, so none is
	// offered: it would be a downgrade.
	UpdateStatusAheadOfLatest UpdateStatus = "ahead_of_latest"
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
	// UpdateReasonChannelUnconfirmed means the installed and latest releases
	// are prereleases of different channels, which have no order.
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
	Status            UpdateStatus        `json:"status" enum:"unknown,current,behind_latest,ahead_of_latest"`
	BinaryPath        string              `json:"binaryPath,omitempty"`
	CurrentVersion    string              `json:"currentVersion,omitempty"`
	LatestVersion     string              `json:"latestVersion,omitempty"`
	Source            string              `json:"source,omitempty"`
	MaintenanceMethod string              `json:"maintenanceMethod,omitempty"`
	Reason            UpdateUnknownReason `json:"reason,omitempty" enum:"ownership_unconfirmed,unsupported_source,version_unparseable,channel_unconfirmed,lookup_failed,disabled,build_unordered"`
	CheckedAt         time.Time           `json:"checkedAt"`

	// probeFailed marks an unknown result whose version probe failed or
	// printed no version, which a busy machine or slow CLI start can cause
	// once. It is retried like a failed release lookup.
	probeFailed bool
}

// maxConcurrentUpdateChecks caps checks running at once, whoever starts them.
// Each check launches the harness CLI to read its version (up to ~240 MB for a
// Node-based CLI while it runs), so an unbounded burst across every installed
// harness could briefly take over a gigabyte.
const maxConcurrentUpdateChecks = 2

// lookupFailedRetryAfter is how long a result whose release lookup or version
// probe failed is served before a read checks again. Other results wait for
// the daemon's next periodic pass: their cause, such as a harness without a
// release feed, does not clear on its own.
const lookupFailedRetryAfter = 30 * time.Minute

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
//
// A cached result is served however old it is: the daemon's periodic pass and
// an explicit refresh are the only things that check again. A harness with no
// result yet (first request, or after an install or update job) is checked
// once on demand, as is one whose release lookup or version probe failed over
// lookupFailedRetryAfter ago. A refresh re-checks while other readers keep
// getting the previous result until the new one replaces it.
func (s *Service) UpdateAdvisory(ctx context.Context, target Target, refresh bool) (UpdateAdvisory, error) {
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
		// An explicit click needs evidence gathered after the click. An older
		// in-flight read may finish for its callers, but may not cache over it.
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
	// Keep the adapter-selected path even when its version probe fails. This is
	// display-only evidence; ownership and commands continue using the raw path.
	advisory.BinaryPath = displayBinaryPath(verified.ResolvedPath)
	if err != nil {
		advisory.Reason = UpdateReasonOwnershipUnconfirmed
		advisory.probeFailed = true
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
		advisory.probeFailed = true
		return advisory, nil
	}
	advisory.CurrentVersion = current.display
	if source.Package != "" {
		advisory.MaintenanceMethod = source.Method
	} else if home, _ := os.UserHomeDir(); vendorMaintainable(s.goos, target, verified.ResolvedPath, home) {
		advisory.MaintenanceMethod = "official-installer"
	}
	var latest string
	if source.Package != "" {
		advisory.Source = source.Method
		latest, err = s.managedVersion(ctx, source)
	} else {
		// No package manager provably owns the binary, so compare against the
		// vendor's own release channel, unless the binary sits in another
		// tool's layout whose channel may differ from the vendor's.
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

// vendorMaintainable reports whether the harness can update the binary
// sessions run through its own self-update command or a re-run of its
// installer. A release lookup is not installation ownership: binaries inside a
// package manager or version manager belong to that tool, and harnesses
// without either path have no vendor maintenance path. The command runs from
// the resolved binary itself, and the installer is bound to its directory, so
// either updates that copy however the user placed it. Kimi's installer is
// bound only to its default install (bindKimiInstallerUpdate), so a kimi
// elsewhere gets manual-update guidance instead of an Update that is refused.
func vendorMaintainable(goos string, target Target, resolvedPath, home string) bool {
	_, ok := vendorUpdateCommands[target]
	if installerUpdateTargets[target] {
		_, ok = kimiInstallRoot(goos, resolvedPath, home)
	}
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
			return
		}
		if source.Method != owner.method {
			s.setDerivedOwner(target, nil)
			return
		}
		// The same manager claims a different package: a listed formula matched
		// only by name (another tap's opencode, say), and the binary's own
		// receipt names the package that actually installed it. Never keep the
		// listed one, whose releases and upgrade belong to the other package.
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

// advisoryBinaryPath preserves local Health details when release checks are
// disabled or unsupported, without invoking a binary or querying a feed.
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
